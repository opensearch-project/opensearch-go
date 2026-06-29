// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"net"
	"os"
	"time"

	"github.com/rs/dnscache"

	"github.com/opensearch-project/opensearch-go/v5/internal/envvars"
)

// defaultDNSCacheRefresh is the default DNS cache refresh interval. 60s mirrors
// the TTL AWS publishes for managed OpenSearch Service domain endpoints (and the
// JVM networkaddress.cache.ttl value AWS recommends to match it), so the client
// re-resolves about as often as those records are expected to rotate.
const defaultDNSCacheRefresh = 60 * time.Second

// dnsDialTimeout and dnsKeepAlive mirror the dial timeout and keep-alive of
// http.DefaultTransport's net.Dialer so that wrapping the dialer with a DNS
// cache does not regress those defaults.
const (
	dnsDialTimeout = 30 * time.Second
	dnsKeepAlive   = 30 * time.Second
)

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

// dialContextFunc is the signature of http.Transport.DialContext.
type dialContextFunc = func(ctx context.Context, network, addr string) (net.Conn, error)

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
func newDialContextDNSCache(ctx context.Context, refresh time.Duration, r *dnscache.Resolver, m *metrics) dialContextFunc {
	if m != nil {
		r.OnCacheMiss = func() { m.dnsCacheMisses.Add(1) }
	}

	ticker := time.NewTicker(refresh)
	go func() {
		defer ticker.Stop()
		dnsRefreshLoop(ctx, ticker.C, func() {
			r.RefreshWithOptions(dnscache.ResolverRefreshOptions{
				ClearUnused:      true,
				PersistOnFailure: true,
			})
		})
	}()

	dialer := &net.Dialer{Timeout: dnsDialTimeout, KeepAlive: dnsKeepAlive}

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
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}

		var conn net.Conn
		for _, ip := range ips {
			conn, err = dialer.DialContext(ctx, network, net.JoinHostPort(ip, port))
			if err == nil {
				break
			}
		}

		return conn, err
	}
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
