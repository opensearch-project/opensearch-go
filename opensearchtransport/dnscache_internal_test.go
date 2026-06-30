// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/dnscache"
	"github.com/stretchr/testify/require"
)

// fakeResolver is a controllable dnscache.DNSResolver. It counts LookupHost
// calls and can be flipped to return an error to simulate a resolver outage.
type fakeResolver struct {
	mu    sync.Mutex
	calls int
	addrs []string
	err   error
}

func (f *fakeResolver) LookupHost(_ context.Context, _ string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.addrs, nil
}

func (f *fakeResolver) LookupAddr(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (f *fakeResolver) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeResolver) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func TestResolveDNSCacheRefresh(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  time.Duration
		want time.Duration
	}{
		{name: "zero_uses_default", cfg: 0, want: defaultDNSCacheRefresh},
		{name: "negative_disables", cfg: -1, want: 0},
		{name: "explicit_positive", cfg: 90 * time.Second, want: 90 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, resolveDNSCacheRefresh(tt.cfg))
		})
	}
}

func TestResolveDNSCacheRefreshEnvOverride(t *testing.T) {
	// Cannot be parallel: t.Setenv forbids it.
	tests := []struct {
		name string
		env  string
		cfg  time.Duration
		want time.Duration
	}{
		{name: "env_overrides_config", env: "45s", cfg: 10 * time.Minute, want: 45 * time.Second},
		{name: "env_disables", env: "-1", cfg: 5 * time.Minute, want: 0},
		{name: "env_integer_seconds", env: "120", cfg: 0, want: 2 * time.Minute},
		{name: "unparseable_env_falls_back_to_config", env: "garbage", cfg: 30 * time.Second, want: 30 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OPENSEARCH_GO_DNS_CACHE_REFRESH", tt.env)
			require.Equal(t, tt.want, resolveDNSCacheRefresh(tt.cfg))
		})
	}
}

// TestCachingDialContextCacheHit verifies that repeated dials to the same host
// resolve through the cache: only the first triggers a LookupHost call.
func TestCachingDialContextCacheHit(t *testing.T) {
	t.Parallel()

	// Spin up a real listener so DialContext can connect to a routable address.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go acceptAndClose(ln)

	_, port, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)

	fr := &fakeResolver{addrs: []string{"127.0.0.1"}}
	r := &dnscache.Resolver{Resolver: fr}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	dial := newDialContextDNSCache(ctx, time.Hour, r, nil)

	for range 3 {
		conn, derr := dial(ctx, "tcp", net.JoinHostPort("cached.example", port))
		require.NoError(t, derr)
		require.NotNil(t, conn)
		_ = conn.Close()
	}

	require.Equal(t, 1, fr.callCount(), "subsequent dials should be served from cache")
}

// TestCachingDialContextServeStale verifies that a refresh which fails (resolver
// outage) keeps the previously resolved address available rather than evicting
// it -- the serve-stale behavior that motivates this feature.
func TestCachingDialContextServeStale(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go acceptAndClose(ln)

	_, port, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)

	fr := &fakeResolver{addrs: []string{"127.0.0.1"}}
	r := &dnscache.Resolver{Resolver: fr}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	dial := newDialContextDNSCache(ctx, time.Hour, r, nil)

	// Prime the cache.
	conn, err := dial(ctx, "tcp", net.JoinHostPort("stale.example", port))
	require.NoError(t, err)
	_ = conn.Close()

	// Simulate the resolver going down, then refresh with the same
	// PersistOnFailure option the production loop uses.
	fr.setErr(errors.New("resolver unreachable"))
	r.RefreshWithOptions(dnscache.ResolverRefreshOptions{ClearUnused: true, PersistOnFailure: true})

	// The cached address must still be dialable despite the failed refresh.
	conn, err = dial(ctx, "tcp", net.JoinHostPort("stale.example", port))
	require.NoError(t, err, "stale entry should survive a failed refresh")
	require.NotNil(t, conn)
	_ = conn.Close()
}

// TestCachingDialContextMetrics verifies the DNS counters: every dial increments
// dnsLookups, the first (cold) lookup per host increments dnsCacheMisses, and a
// resolution error increments dnsLookupErrors.
func TestCachingDialContextMetrics(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go acceptAndClose(ln)

	_, port, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)

	fr := &fakeResolver{addrs: []string{"127.0.0.1"}}
	r := &dnscache.Resolver{Resolver: fr}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	m := &metrics{}
	dial := newDialContextDNSCache(ctx, time.Hour, r, m)

	// Two dials to the same host: one cold miss, one cache hit.
	for range 2 {
		conn, derr := dial(ctx, "tcp", net.JoinHostPort("metrics.example", port))
		require.NoError(t, derr)
		_ = conn.Close()
	}

	require.Equal(t, int64(2), m.dnsLookups.Load(), "every dial counts as a lookup")
	require.Equal(t, int64(1), m.dnsCacheMisses.Load(), "only the cold dial misses the cache")
	require.Equal(t, int64(0), m.dnsLookupErrors.Load())

	// A resolution failure on a new host increments the error counter.
	fr.setErr(errors.New("resolver unreachable"))
	_, derr := dial(ctx, "tcp", net.JoinHostPort("broken.example", port))
	require.Error(t, derr)
	require.Equal(t, int64(1), m.dnsLookupErrors.Load())
}

// TestDNSRefreshLoopStopsOnCancel verifies the refresh goroutine exits when its
// context is cancelled (the path Close uses to reclaim it).
func TestDNSRefreshLoopStopsOnCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	tick := make(chan time.Time)
	var refreshes atomic.Int64

	done := make(chan struct{})
	go func() {
		dnsRefreshLoop(ctx, tick, func() { refreshes.Add(1) })
		close(done)
	}()

	// Drive a couple of refreshes deterministically.
	tick <- time.Time{}
	tick <- time.Time{}

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("dnsRefreshLoop did not exit after context cancellation")
	}

	require.Equal(t, int64(2), refreshes.Load())
}

// TestNewDoesNotModifyCustomTransport verifies a caller-supplied Transport is
// never mutated: its DialContext is left exactly as provided.
func TestNewDoesNotModifyCustomTransport(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("sentinel dialer")
	custom := &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, sentinel
		},
	}

	client, err := New(Config{
		URLs:      []*url.URL{{Scheme: "http", Host: "localhost:9200"}},
		Transport: custom,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	got, ok := client.transport.(*http.Transport)
	require.True(t, ok)
	_, dialErr := got.DialContext(context.Background(), "tcp", "localhost:9200")
	require.ErrorIs(t, dialErr, sentinel, "custom DialContext must be preserved untouched")
}

// TestNewDisabledLeavesDefaultTransport verifies that disabling the cache
// (DNSCacheRefresh < 0) leaves the stock http.DefaultTransport in place rather
// than cloning and rewiring it.
func TestNewDisabledLeavesDefaultTransport(t *testing.T) {
	t.Parallel()

	client, err := New(Config{
		URLs:            []*url.URL{{Scheme: "http", Host: "localhost:9200"}},
		DNSCacheRefresh: -1,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	require.Same(t, http.DefaultTransport, client.transport,
		"disabled cache should leave http.DefaultTransport untouched")
}

// TestNewEnabledClonesDefaultTransport verifies the default path installs a
// caching dialer on a clone, leaving the global http.DefaultTransport unmutated.
func TestNewEnabledClonesDefaultTransport(t *testing.T) {
	t.Parallel()

	client, err := New(Config{
		URLs: []*url.URL{{Scheme: "http", Host: "localhost:9200"}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	require.NotSame(t, http.DefaultTransport, client.transport,
		"enabled cache must clone rather than mutate http.DefaultTransport")

	got, ok := client.transport.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, got.DialContext, "default path should install a caching dialer")
}

// TestCloseStopsDNSResolver verifies that Close cancels the root context the DNS
// refresh goroutine selects on, so closing the client reclaims that goroutine.
// The goroutine's only exit signal is ctx.Done() (see dnsRefreshLoop); asserting
// the context is cancelled is the deterministic end-to-end check for that path.
func TestCloseStopsDNSResolver(t *testing.T) {
	t.Parallel()

	client, err := New(Config{
		URLs: []*url.URL{{Scheme: "http", Host: "localhost:9200"}},
	})
	require.NoError(t, err)

	// Sanity: the caching dialer is installed, so a refresh goroutine is running
	// against client.ctx.
	tr, ok := client.transport.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, tr.DialContext)

	select {
	case <-client.ctx.Done():
		t.Fatal("root context cancelled before Close")
	default:
	}

	require.NoError(t, client.Close())

	select {
	case <-client.ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not cancel the root context feeding the DNS refresh goroutine")
	}
}

// acceptAndClose accepts connections on ln and immediately closes them, so the
// dialer's DialContext succeeds without exercising any protocol.
func acceptAndClose(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_ = conn.Close()
	}
}

// captureRouter is a minimal Router whose configurePolicySettings and
// DiscoveryUpdate hooks record the root context handed to them by New and can be
// flipped to fail. It lets a test force New's post-context error paths and then
// assert the captured context was cancelled (i.e. the DNS-refresh goroutine was
// reclaimed).
type captureRouter struct {
	configErr    error
	discoveryErr error
	configCtx    context.Context //nolint:containedctx // Test captures the root context to assert it was canceled.
	discoveryCtx context.Context //nolint:containedctx // Test captures the root context to assert it was canceled.
}

// configurePolicySettings captures the root context and optionally fails,
// exercising the first error return after New creates the cancellable context.
func (r *captureRouter) configurePolicySettings(config policyConfig) error {
	r.configCtx = config.ctx
	return r.configErr
}

func (r *captureRouter) DiscoveryUpdate(added, _, _ []*Connection) error {
	// added carries the seed connections; New always passes a non-nil ctx via the
	// router's own ctx, so capture it from the policy config path instead. The
	// discovery path itself has no ctx parameter, so reuse the config capture.
	r.discoveryCtx = r.configCtx
	return r.discoveryErr
}

func (r *captureRouter) Route(_ context.Context, _ *http.Request) (NextHop, error) {
	return NextHop{}, nil
}
func (r *captureRouter) OnSuccess(*Connection)       {}
func (r *captureRouter) OnFailure(*Connection) error { return nil }
func (r *captureRouter) CheckDead(_ context.Context, _ HealthCheckFunc) error {
	return nil
}

func (r *captureRouter) RotateStandby(_ context.Context, _ int) (int, error) {
	return 0, nil
}

// TestNewCancelsDNSResolverOnError verifies that the post-context error paths in
// New cancel the root context, reclaiming the DNS-refresh goroutine. Before the
// fix these paths returned without calling cancel, leaking a live goroutine plus
// its 60s ticker for the process lifetime. The captured context being Done after
// New returns an error is the deterministic proof that cancel ran (the goroutine's
// only exit signal is ctx.Done(); see dnsRefreshLoop).
func TestNewCancelsDNSResolverOnError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("forced failure")

	tests := []struct {
		name   string
		router *captureRouter
		ctxOf  func(r *captureRouter) context.Context
	}{
		{
			name:   "configurePolicySettings failure",
			router: &captureRouter{configErr: wantErr},
			ctxOf:  func(r *captureRouter) context.Context { return r.configCtx },
		},
		{
			name:   "DiscoveryUpdate failure",
			router: &captureRouter{discoveryErr: wantErr},
			ctxOf:  func(r *captureRouter) context.Context { return r.discoveryCtx },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client, err := New(Config{
				URLs:   []*url.URL{{Scheme: "http", Host: "localhost:9200"}},
				Router: tt.router,
			})
			require.Error(t, err)
			require.ErrorIs(t, err, wantErr)
			require.Nil(t, client)

			rootCtx := tt.ctxOf(tt.router)
			require.NotNil(t, rootCtx, "router was never handed the root context")

			select {
			case <-rootCtx.Done():
			default:
				t.Fatal("New returned an error without canceling the root context; " +
					"the DNS refresh goroutine leaks")
			}
		})
	}
}
