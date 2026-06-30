// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"time"

	"github.com/rs/dnscache"

	"github.com/opensearch-project/opensearch-go/v5/internal/envvars"
)

// maxParallelDials caps how many resolved addresses the caching dialer races
// concurrently for a multi-A host. A host that resolves to more addresses is
// shuffled and sampled down to this many, bounding fan-out while still spreading
// load and tolerating a dead address.
const maxParallelDials = 3

// ErrDNSNoAddresses is returned by the caching dialer when DNS resolution
// succeeds but yields no usable addresses for the host.
var ErrDNSNoAddresses = errors.New("dns cache: lookup returned no addresses")

// defaultDNSCacheRefresh is the default DNS cache refresh interval. 60s mirrors
// the TTL AWS publishes for managed OpenSearch Service domain endpoints (and the
// JVM networkaddress.cache.ttl value AWS recommends to match it), so the client
// re-resolves about as often as those records are expected to rotate.
const defaultDNSCacheRefresh = 60 * time.Second

// defaultDNSDialTimeout and defaultDNSKeepAlive mirror the dial timeout and
// keep-alive of http.DefaultTransport's net.Dialer so that wrapping the dialer
// with a DNS cache does not regress those defaults.
const (
	defaultDNSDialTimeout = 30 * time.Second
	defaultDNSKeepAlive   = 30 * time.Second
)

// defaultDNSTimeout bounds each cache refresh lookup. refreshRecords re-resolves
// cached hosts sequentially on a single goroutine, so without a per-lookup
// deadline one stuck resolution would stall every later host in that tick. 10s
// matches the timeout Go's own pure resolver applies per query.
const defaultDNSTimeout = 10 * time.Second

// resolveDNSCacheRefresh resolves the effective DNS cache refresh interval from
// the programmatic config value and the OPENSEARCH_GO_DNS_CACHE_REFRESH
// environment override. It follows the standard 0=default, <0=disable,
// >0=explicit convention used throughout New. The environment variable, when
// parseable, takes precedence over the config value.
//
// A return value of 0 means caching is disabled; any positive value is the
// refresh interval.
func resolveDNSCacheRefresh(cfg time.Duration) time.Duration {
	refresh := cfg
	if envVal, ok := os.LookupEnv(envvars.DNSCacheRefresh); ok && envVal != "" {
		if d, ok := parseDuration(envVal); ok {
			refresh = d
		}
	}

	switch {
	case refresh == 0:
		return defaultDNSCacheRefresh
	case refresh < 0:
		return 0 // disabled
	default:
		return refresh
	}
}

// resolveDNSDialTimeout resolves the net.Dialer dial timeout from the config
// value and the OPENSEARCH_GO_DNS_DIAL_TIMEOUT override (env wins when
// parseable). The result feeds net.Dialer.Timeout directly: 0=default, <0=no
// timeout, >0=explicit.
func resolveDNSDialTimeout(cfg time.Duration) time.Duration {
	timeout := cfg
	if envVal, ok := os.LookupEnv(envvars.DNSDialTimeout); ok && envVal != "" {
		if d, ok := parseDuration(envVal); ok {
			timeout = d
		}
	}

	switch {
	case timeout == 0:
		return defaultDNSDialTimeout
	case timeout < 0:
		return 0 // net.Dialer: no dial timeout
	default:
		return timeout
	}
}

// resolveDNSKeepAlive resolves the net.Dialer keep-alive from the config value
// and the OPENSEARCH_GO_DNS_KEEP_ALIVE override (env wins when parseable). The
// result feeds net.Dialer.KeepAlive directly: 0=default, <0=disabled (-1),
// >0=explicit.
func resolveDNSKeepAlive(cfg time.Duration) time.Duration {
	keepAlive := cfg
	if envVal, ok := os.LookupEnv(envvars.DNSKeepAlive); ok && envVal != "" {
		if d, ok := parseDuration(envVal); ok {
			keepAlive = d
		}
	}

	switch {
	case keepAlive == 0:
		return defaultDNSKeepAlive
	case keepAlive < 0:
		return -1 // net.Dialer: disable keep-alive
	default:
		return keepAlive
	}
}

// resolveDNSTimeout resolves the per-lookup DNS timeout from the config value
// and the OPENSEARCH_GO_DNS_TIMEOUT override (env wins when parseable). The
// result feeds dnscache.Resolver.Timeout: 0=default, <0=no timeout, >0=explicit.
func resolveDNSTimeout(cfg time.Duration) time.Duration {
	timeout := cfg
	if envVal, ok := os.LookupEnv(envvars.DNSTimeout); ok && envVal != "" {
		if d, ok := parseDuration(envVal); ok {
			timeout = d
		}
	}

	switch {
	case timeout == 0:
		return defaultDNSTimeout
	case timeout < 0:
		return 0 // dnscache.Resolver: no per-lookup timeout
	default:
		return timeout
	}
}

// dialContextFunc is the signature of http.Transport.DialContext.
type dialContextFunc = func(ctx context.Context, network, addr string) (net.Conn, error)

// dnsCacheSettings holds the resolved DNS-cache tunables. Build it with
// resolveDNSCacheSettings.
type dnsCacheSettings struct {
	refresh     time.Duration // re-resolution cadence; >0 enables caching, 0 disables
	dialTimeout time.Duration // net.Dialer.Timeout
	keepAlive   time.Duration // net.Dialer.KeepAlive
	dnsTimeout  time.Duration // dnscache.Resolver.Timeout (per-lookup deadline)
}

// resolveDNSCacheSettings resolves the raw Config.DNS* values into the effective
// tunables, applying env overrides and defaults. A zero refresh means caching is
// disabled.
func resolveDNSCacheSettings(refreshCfg, dialTimeoutCfg, keepAliveCfg, dnsTimeoutCfg time.Duration) dnsCacheSettings {
	return dnsCacheSettings{
		refresh:     resolveDNSCacheRefresh(refreshCfg),
		dialTimeout: resolveDNSDialTimeout(dialTimeoutCfg),
		keepAlive:   resolveDNSKeepAlive(keepAliveCfg),
		dnsTimeout:  resolveDNSTimeout(dnsTimeoutCfg),
	}
}

// newDialContextDNSCache returns a DialContext function backed by a process-local
// DNS cache. Resolved addresses are cached and re-resolved every refresh
// interval. When the resolver becomes briefly unreachable, the last-known-good
// address continues to be served until the resolver recovers. The refresh
// goroutine exits when ctx is cancelled (the transport root context, cancelled
// by Close).
//
// PersistOnFailure keeps the last successful lookup in the cache across a failed
// refresh, so a transient resolver outage continues returning the previously
// resolved address instead of evicting it. ClearUnused drops entries that were
// not looked up since the previous refresh, bounding cache growth.
//
// When m is non-nil, per-dial DNS counters are recorded: every dial increments
// dnsLookups, a lookup not served from cache increments dnsCacheMisses (via the
// resolver's OnCacheMiss hook), and a resolution error increments
// dnsLookupErrors.
//
// The resolver's per-lookup Timeout is set from dnsTimeout so a hung resolver
// cannot stall a refresh tick: refreshRecords re-resolves cached hosts
// sequentially on the single refresh goroutine, so without a deadline one stuck
// lookup would block every subsequent host in that tick. A non-positive
// dnsTimeout leaves refresh lookups unbounded, matching the "timeouts disabled"
// intent.
func newDialContextDNSCache(ctx context.Context, s dnsCacheSettings, r *dnscache.Resolver, m *metrics) dialContextFunc {
	if s.dnsTimeout > 0 {
		r.Timeout = s.dnsTimeout
	}
	if m != nil {
		r.OnCacheMiss = func() { m.dnsCacheMisses.Add(1) }
	}

	ticker := time.NewTicker(s.refresh)
	go func() {
		defer ticker.Stop()
		dnsRefreshLoop(ctx, ticker.C, func() {
			r.RefreshWithOptions(dnscache.ResolverRefreshOptions{
				ClearUnused:      true,
				PersistOnFailure: true,
			})
		})
	}()

	dialer := &net.Dialer{Timeout: s.dialTimeout, KeepAlive: s.keepAlive}

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}

		if m != nil {
			m.dnsLookups.Add(1)
		}

		ips, err := r.LookupHost(ctx, host)
		if err != nil {
			if m != nil {
				m.dnsLookupErrors.Add(1)
			}
			return nil, err
		}
		if len(ips) == 0 {
			if m != nil {
				m.dnsLookupErrors.Add(1)
			}
			return nil, fmt.Errorf("%w: %q", ErrDNSNoAddresses, host)
		}

		// Common case: a node hostname resolves to a single address. Dial it
		// directly with no goroutines or allocation.
		if len(ips) == 1 {
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0], port))
		}

		// Multiple A records: race up to maxParallelDials of them concurrently and
		// take the first connection that succeeds, canceling the rest. This avoids
		// both ordering bias (otherwise every new connection pins to the first
		// record for the refresh interval) and head-of-line blocking on a dead or
		// blackholed IP, without carving up the dial timeout sequentially.
		return dialParallel(ctx, dialer.DialContext, network, port, ips, maxParallelDials)
	}
}

// dialParallel races up to maxDials of the resolved addresses and returns the
// first connection that succeeds, canceling and closing the losers. When more
// addresses are present than maxDials, a random start offset is chosen and
// maxDials consecutive addresses are raced (with wraparound), so fan-out stays
// bounded while still spreading load across records on each new connection. If
// every dialed address fails it returns the joined per-address errors. It is
// only called with len(ips) > 1.
//
// ips is the resolver's cached backing slice and is only read, never mutated:
// the random-offset selection avoids the in-place shuffle that would race other
// concurrent dials and corrupt the shared cache entry.
func dialParallel(ctx context.Context, dial dialContextFunc, network, port string, ips []string, maxDials int) (net.Conn, error) {
	n := len(ips)
	if maxDials > 0 && n > maxDials {
		n = maxDials
	}

	// Random start so a multi-A host does not always race the same prefix.
	// #nosec G404 -- address selection is load balancing, not security-sensitive
	offset := rand.IntN(len(ips))

	dialCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	type dialResult struct {
		conn net.Conn
		err  error
	}
	// Unbuffered: a goroutine that loses the race blocks on the send until the
	// receiver has returned, at which point dialCtx is cancelled and the
	// goroutine closes its now-orphaned connection instead.
	results := make(chan dialResult)

	for i := 0; i < n; i++ {
		ip := ips[(offset+i)%len(ips)]
		go func(ip string) {
			// Each attempt is independently bounded by the dialer's own timeout:
			// net.Dialer applies Timeout per DialContext call, so a slow address
			// cannot hold up the race beyond that budget.
			conn, err := dial(dialCtx, network, net.JoinHostPort(ip, port))
			if err != nil {
				err = fmt.Errorf("dial %s: %w", net.JoinHostPort(ip, port), err)
			}

			select {
			case results <- dialResult{conn: conn, err: err}:
			case <-dialCtx.Done():
				if conn != nil {
					_ = conn.Close() // a winner was already chosen
				}
			}
		}(ip)
	}

	errs := make([]error, 0, n)
	for i := 0; i < n; i++ {
		res := <-results
		if res.err == nil {
			return res.conn, nil // defer cancel() unblocks and closes the losers
		}
		errs = append(errs, res.err)
	}
	return nil, errors.Join(errs...)
}

// dnsRefreshLoop invokes refresh on every tick until ctx is cancelled. It is
// factored out of newDialContextDNSCache so the refresh cadence and the
// cancellation exit path can be driven deterministically in tests.
func dnsRefreshLoop(ctx context.Context, tick <-chan time.Time, refresh func()) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			refresh()
		}
	}
}
