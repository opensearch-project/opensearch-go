// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil/mockhttp"
)

// testNodesResponse builds a /_nodes/http JSON response from a map of nodeID → nodeInfo.
func testNodesResponse(t *testing.T, nodes map[string]nodeInfo) []byte {
	t.Helper()
	type nodesStats struct {
		Total      int `json:"total"`
		Successful int `json:"successful"`
		Failed     int `json:"failed"`
	}
	resp := struct {
		NodesStats  nodesStats          `json:"_nodes"`
		ClusterName string              `json:"cluster_name"`
		Nodes       map[string]nodeInfo `json:"nodes"`
	}{
		NodesStats:  nodesStats{Total: len(nodes), Successful: len(nodes)},
		ClusterName: "test-cluster",
		Nodes:       nodes,
	}
	b, err := json.Marshal(resp)
	require.NoError(t, err)
	return b
}

// newResolverTestTransport creates a mock transport that serves
// /_nodes/http with the given nodes response and / as a health check.
func newResolverTestTransport(t *testing.T, nodesJSON []byte) http.RoundTripper {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/_nodes/http", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(nodesJSON)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
	return mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Result(), nil
	})
}

// testSeedURL returns a parsed http://localhost:9200 URL.
func testSeedURL(t *testing.T) *url.URL {
	t.Helper()
	u, err := url.Parse("http://localhost:9200")
	require.NoError(t, err)
	return u
}

// ---------------------------------------------------------------------------
// TestAddressResolver: per-node resolver only (no runner)
// ---------------------------------------------------------------------------

func TestAddressResolver(t *testing.T) {
	t.Parallel()

	// Table-driven: resolver protocol (4-case return semantics).
	type wantMetrics struct {
		calls    int
		rewrites int
		errors   int
	}

	protocolTests := []struct {
		name     string
		inAddrs  map[string]string // node-name → publish_address
		resolver AddressResolverFunc
		wantPort map[string]string // node-name → expected port
		wantErr  bool
		metrics  wantMetrics
	}{
		{
			name:    "rewrite all to new port",
			inAddrs: map[string]string{"alpha": "10.0.0.1:9200", "beta": "10.0.0.2:9200"},
			resolver: func(_ context.Context, node NodeInfo) (*url.URL, error) {
				u := *node.URL
				u.Host = net.JoinHostPort(node.URL.Hostname(), "9201")
				return &u, nil
			},
			wantPort: map[string]string{"alpha": "9201", "beta": "9201"},
			metrics:  wantMetrics{calls: 2, rewrites: 2, errors: 0},
		},
		{
			name:    "nil return keeps default",
			inAddrs: map[string]string{"alpha": "10.0.0.1:9200", "beta": "10.0.0.2:9200"},
			resolver: func(_ context.Context, _ NodeInfo) (*url.URL, error) {
				return nil, nil //nolint:nilnil // testing (nil, nil) protocol case
			},
			wantPort: map[string]string{"alpha": "9200", "beta": "9200"},
			metrics:  wantMetrics{calls: 2, rewrites: 0, errors: 0},
		},
		{
			name:    "identical URL return is not a rewrite",
			inAddrs: map[string]string{"alpha": "10.0.0.1:9200"},
			resolver: func(_ context.Context, node NodeInfo) (*url.URL, error) {
				clone := *node.URL
				return &clone, nil
			},
			wantPort: map[string]string{"alpha": "9200"},
			metrics:  wantMetrics{calls: 1, rewrites: 0, errors: 0},
		},
		{
			name:    "nil URL with error skips node",
			inAddrs: map[string]string{"alpha": "10.0.0.1:9200", "beta": "10.0.0.2:9200"},
			resolver: func(_ context.Context, node NodeInfo) (*url.URL, error) {
				if node.Name == "alpha" {
					return nil, fmt.Errorf("probe failed for %q", node.Name)
				}
				return nil, nil //nolint:nilnil // testing (nil, nil) protocol case
			},
			wantPort: map[string]string{"beta": "9200"},
			metrics:  wantMetrics{calls: 2, rewrites: 0, errors: 1},
		},
		{
			name:    "non-nil URL with error keeps node",
			inAddrs: map[string]string{"alpha": "10.0.0.1:9200", "beta": "10.0.0.2:9200"},
			resolver: func(_ context.Context, node NodeInfo) (*url.URL, error) {
				u := *node.URL
				u.Host = net.JoinHostPort(node.URL.Hostname(), "9201")
				return &u, fmt.Errorf("partial failure for %q", node.Name)
			},
			wantPort: map[string]string{"alpha": "9201", "beta": "9201"},
			metrics:  wantMetrics{calls: 2, rewrites: 2, errors: 2},
		},
		{
			name:    "all errors returns combined error",
			inAddrs: map[string]string{"alpha": "10.0.0.1:9200", "beta": "10.0.0.2:9200"},
			resolver: func(_ context.Context, node NodeInfo) (*url.URL, error) {
				return nil, fmt.Errorf("unreachable: %q", node.Name)
			},
			wantPort: map[string]string{},
			wantErr:  true,
			metrics:  wantMetrics{calls: 2, rewrites: 0, errors: 2},
		},
		{
			name:    "mixed outcomes: rewrite, keep, skip",
			inAddrs: map[string]string{"rewrite-me": "10.0.0.1:9200", "keep-me": "10.0.0.2:9200", "skip-me": "10.0.0.3:9200"},
			resolver: func(_ context.Context, node NodeInfo) (*url.URL, error) {
				switch node.Name {
				case "rewrite-me":
					u := *node.URL
					u.Host = net.JoinHostPort(node.URL.Hostname(), "9201")
					return &u, nil
				case "skip-me":
					return nil, errors.New("probe failed")
				default:
					return nil, nil //nolint:nilnil // testing (nil, nil) protocol case
				}
			},
			wantPort: map[string]string{"rewrite-me": "9201", "keep-me": "9200"},
			metrics:  wantMetrics{calls: 3, rewrites: 1, errors: 1},
		},
	}

	for _, tt := range protocolTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			inputNodes := make(map[string]nodeInfo, len(tt.inAddrs))
			for name, addr := range tt.inAddrs {
				inputNodes["id-"+name] = nodeInfo{
					Name:  name,
					Roles: []string{"data"},
					HTTP:  nodeInfoHTTP{PublishAddress: addr},
				}
			}
			nodesJSON := testNodesResponse(t, inputNodes)

			tp, err := New(Config{
				URLs:                []*url.URL{testSeedURL(t)},
				Transport:           newResolverTestTransport(t, nodesJSON),
				EnableMetrics:       true,
				HealthCheck:         NoOpHealthCheck,
				AddressResolver:     tt.resolver,
				MaxAddressResolvers: 1, // serial for deterministic behavior
			})
			require.NoError(t, err)

			nodes, err := tp.getNodesInfo(t.Context())
			if tt.wantErr {
				require.ErrorIs(t, err, ErrAllResolversFailed)
				return
			}
			require.NoError(t, err)
			require.Len(t, nodes, len(tt.wantPort))

			gotPorts := make(map[string]string, len(nodes))
			for _, n := range nodes {
				gotPorts[n.Name] = n.url.Port()
			}
			require.Equal(t, tt.wantPort, gotPorts)

			for _, n := range nodes {
				inputAddr := tt.inAddrs[n.Name]
				_, inputPort, _ := net.SplitHostPort(inputAddr)
				if n.url.Port() != inputPort {
					require.True(t, n.rewritten, "node %q should be marked rewritten", n.Name)
				} else {
					require.False(t, n.rewritten, "node %q should not be marked rewritten", n.Name)
				}
			}

			m, _ := tp.Metrics()
			require.Equal(t, tt.metrics.calls, m.AddressResolverCalls, "calls")
			require.Equal(t, tt.metrics.rewrites, m.AddressResolverRewrites, "rewrites")
			require.Equal(t, tt.metrics.errors, m.AddressResolverErrors, "errors")
		})
	}

	// Standalone infrastructure tests for the built-in handler.

	twoNodes := map[string]nodeInfo{
		"node-1": {
			Name:       "alpha",
			Roles:      []string{"data", "ingest"},
			HTTP:       nodeInfoHTTP{PublishAddress: "10.0.0.1:9200"},
			Attributes: map[string]any{"zone": "us-east-1a"},
		},
		"node-2": {
			Name:  "beta",
			Roles: []string{"data"},
			HTTP:  nodeInfoHTTP{PublishAddress: "10.0.0.2:9200"},
		},
	}
	nodesJSON := testNodesResponse(t, twoNodes)

	t.Run("nil resolver preserves default behavior", func(t *testing.T) {
		t.Parallel()

		tp, err := New(Config{
			URLs:        []*url.URL{testSeedURL(t)},
			Transport:   newResolverTestTransport(t, nodesJSON),
			HealthCheck: NoOpHealthCheck,
		})
		require.NoError(t, err)

		nodes, err := tp.getNodesInfo(t.Context())
		require.NoError(t, err)
		require.Len(t, nodes, 2)

		for _, n := range nodes {
			require.Equal(t, "9200", n.url.Port())
			require.False(t, n.rewritten)
		}
	})

	t.Run("resolver receives correct NodeInfo fields", func(t *testing.T) {
		t.Parallel()

		var captured []NodeInfo
		tp, err := New(Config{
			URLs:        []*url.URL{testSeedURL(t)},
			Transport:   newResolverTestTransport(t, nodesJSON),
			HealthCheck: NoOpHealthCheck,
			AddressResolver: func(_ context.Context, node NodeInfo) (*url.URL, error) {
				captured = append(captured, node)
				return nil, nil //nolint:nilnil // testing (nil, nil) protocol case
			},
			MaxAddressResolvers: 1,
		})
		require.NoError(t, err)

		_, err = tp.getNodesInfo(t.Context())
		require.NoError(t, err)
		require.Len(t, captured, 2)

		byName := make(map[string]NodeInfo, 2)
		for _, ni := range captured {
			byName[ni.Name] = ni
		}

		alpha := byName["alpha"]
		require.Equal(t, "alpha", alpha.Name)
		require.Contains(t, alpha.Roles, "data")
		require.Contains(t, alpha.Roles, "ingest")
		require.Equal(t, "10.0.0.1:9200", alpha.PublishAddress)
		require.NotNil(t, alpha.URL)
		require.Equal(t, "http", alpha.URL.Scheme)
		require.Equal(t, "us-east-1a", alpha.Attributes["zone"])

		beta := byName["beta"]
		require.Equal(t, "beta", beta.Name)
		require.Contains(t, beta.Roles, "data")
		require.Equal(t, "10.0.0.2:9200", beta.PublishAddress)
	})

	t.Run("unlimited concurrency", func(t *testing.T) {
		t.Parallel()

		var concurrent atomic.Int32
		var maxConcurrent atomic.Int32

		tp, err := New(Config{
			URLs:        []*url.URL{testSeedURL(t)},
			Transport:   newResolverTestTransport(t, nodesJSON),
			HealthCheck: NoOpHealthCheck,
			AddressResolver: func(_ context.Context, _ NodeInfo) (*url.URL, error) {
				cur := concurrent.Add(1)
				for {
					prev := maxConcurrent.Load()
					if cur <= prev || maxConcurrent.CompareAndSwap(prev, cur) {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
				concurrent.Add(-1)
				return nil, nil //nolint:nilnil // testing (nil, nil) protocol case
			},
			MaxAddressResolvers: -1,
		})
		require.NoError(t, err)

		_, err = tp.getNodesInfo(t.Context())
		require.NoError(t, err)

		require.Equal(t, int32(2), maxConcurrent.Load(), "both resolvers should run concurrently")
	})

	t.Run("serial with MaxAddressResolvers=1", func(t *testing.T) {
		t.Parallel()

		var concurrent atomic.Int32
		var maxConcurrent atomic.Int32

		tp, err := New(Config{
			URLs:        []*url.URL{testSeedURL(t)},
			Transport:   newResolverTestTransport(t, nodesJSON),
			HealthCheck: NoOpHealthCheck,
			AddressResolver: func(_ context.Context, _ NodeInfo) (*url.URL, error) {
				cur := concurrent.Add(1)
				for {
					prev := maxConcurrent.Load()
					if cur <= prev || maxConcurrent.CompareAndSwap(prev, cur) {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
				concurrent.Add(-1)
				return nil, nil //nolint:nilnil // testing (nil, nil) protocol case
			},
			MaxAddressResolvers: 1,
		})
		require.NoError(t, err)

		_, err = tp.getNodesInfo(t.Context())
		require.NoError(t, err)

		require.Equal(t, int32(1), maxConcurrent.Load(), "only one resolver should run at a time")
	})

	t.Run("context cancellation stops launching resolvers", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		tp, err := New(Config{
			URLs:        []*url.URL{testSeedURL(t)},
			Transport:   newResolverTestTransport(t, nodesJSON),
			HealthCheck: NoOpHealthCheck,
			AddressResolver: func(_ context.Context, _ NodeInfo) (*url.URL, error) {
				return nil, nil //nolint:nilnil // testing (nil, nil) protocol case
			},
		})
		require.NoError(t, err)

		nodes, err := tp.getNodesInfo(ctx)
		require.NoError(t, err)
		require.LessOrEqual(t, len(nodes), 2)
	})

	t.Run("stalled resolver unblocks on context cancellation", func(t *testing.T) {
		t.Parallel()

		timeout := 100 * time.Millisecond
		ctx, cancel := context.WithTimeout(t.Context(), timeout)
		defer cancel()

		var started atomic.Int32

		tp, err := New(Config{
			URLs:        []*url.URL{testSeedURL(t)},
			Transport:   newResolverTestTransport(t, nodesJSON),
			HealthCheck: NoOpHealthCheck,
			AddressResolver: func(ctx context.Context, _ NodeInfo) (*url.URL, error) {
				started.Add(1)
				<-ctx.Done()
				return nil, ctx.Err()
			},
			MaxAddressResolvers: -1,
		})
		require.NoError(t, err)

		nodes, err := tp.getNodesInfo(ctx)

		require.ErrorIs(t, err, ErrAllResolversFailed)
		require.Empty(t, nodes)
		require.Equal(t, int32(2), started.Load())
	})

	t.Run("full discovery round-trip", func(t *testing.T) {
		t.Parallel()

		tp, err := New(Config{
			URLs:        []*url.URL{testSeedURL(t)},
			Transport:   newResolverTestTransport(t, nodesJSON),
			HealthCheck: NoOpHealthCheck,
			AddressResolver: func(_ context.Context, node NodeInfo) (*url.URL, error) {
				u := *node.URL
				u.Host = net.JoinHostPort(node.URL.Hostname(), "9201")
				return &u, nil
			},
		})
		require.NoError(t, err)

		err = tp.DiscoverNodes(t.Context())
		require.NoError(t, err)

		tp.mu.RLock()
		pool := tp.mu.connectionPool
		tp.mu.RUnlock()
		require.NotNil(t, pool)

		urls := pool.URLs()
		require.Len(t, urls, 2)

		for _, u := range urls {
			require.Equal(t, "9201", u.Port(), "pool URL %q should have rewritten port", u)
		}

		switch p := pool.(type) {
		case *multiServerPool:
			for _, conn := range append(p.mu.ready, p.mu.dead...) {
				require.True(t, conn.Rewritten, "connection %q should be marked Rewritten", conn.URLString)
			}
		case *singleServerPool:
			t.Fatal("expected multiServerPool, got singleServerPool")
		}
	})
}

// ---------------------------------------------------------------------------
// TestAddressResolverRunner: runner mechanics with stub resolver
// ---------------------------------------------------------------------------

// stubRewriteResolver is a trivial resolver that rewrites every node to port 9201.
func stubRewriteResolver(_ context.Context, node NodeInfo) (*url.URL, error) {
	u := *node.URL
	u.Host = net.JoinHostPort(node.URL.Hostname(), "9201")
	return &u, nil
}

func TestAddressResolverRunner(t *testing.T) {
	t.Parallel()

	twoNodes := map[string]nodeInfo{
		"node-1": {
			Name:  "alpha",
			Roles: []string{"data", "ingest"},
			HTTP:  nodeInfoHTTP{PublishAddress: "10.0.0.1:9200"},
		},
		"node-2": {
			Name:  "beta",
			Roles: []string{"data"},
			HTTP:  nodeInfoHTTP{PublishAddress: "10.0.0.2:9200"},
		},
	}
	nodesJSON := testNodesResponse(t, twoNodes)

	t.Run("runner takes precedence over built-in handler", func(t *testing.T) {
		t.Parallel()

		var runnerCalled atomic.Int32
		var builtinCalled atomic.Int32

		tp, err := New(Config{
			URLs:        []*url.URL{testSeedURL(t)},
			Transport:   newResolverTestTransport(t, nodesJSON),
			HealthCheck: NoOpHealthCheck,
			AddressResolver: func(_ context.Context, _ NodeInfo) (*url.URL, error) {
				builtinCalled.Add(1)
				return nil, nil //nolint:nilnil // testing (nil, nil) protocol case
			},
			MaxAddressResolvers: 1,
			AddressResolverRunner: func(_ context.Context, nodes []NodeInfo, _ AddressResolverFunc) ([]ResolvedAddress, error) {
				runnerCalled.Add(1)
				out := make([]ResolvedAddress, len(nodes))
				for i, n := range nodes {
					out[i] = ResolvedAddress{Node: n, URL: n.URL}
				}
				return out, nil
			},
		})
		require.NoError(t, err)

		_, err = tp.getNodesInfo(t.Context())
		require.NoError(t, err)

		require.Equal(t, int32(1), runnerCalled.Load(), "runner should have been called")
		require.Equal(t, int32(0), builtinCalled.Load(),
			"built-in resolver should not be called directly when runner is set")
	})

	t.Run("runner ignores resolve func", func(t *testing.T) {
		t.Parallel()

		var resolverCalled atomic.Int32
		tp, err := New(Config{
			URLs:          []*url.URL{testSeedURL(t)},
			Transport:     newResolverTestTransport(t, nodesJSON),
			EnableMetrics: true,
			HealthCheck:   NoOpHealthCheck,
			AddressResolver: func(_ context.Context, _ NodeInfo) (*url.URL, error) {
				resolverCalled.Add(1)
				return nil, nil //nolint:nilnil // testing (nil, nil) protocol case
			},
			AddressResolverRunner: func(_ context.Context, nodes []NodeInfo, _ AddressResolverFunc) ([]ResolvedAddress, error) {
				out := make([]ResolvedAddress, len(nodes))
				for i, n := range nodes {
					u := *n.URL
					u.Host = net.JoinHostPort(n.URL.Hostname(), "9999")
					out[i] = ResolvedAddress{Node: n, URL: &u}
				}
				return out, nil
			},
		})
		require.NoError(t, err)

		nodes, err := tp.getNodesInfo(t.Context())
		require.NoError(t, err)
		require.Len(t, nodes, 2)

		for _, n := range nodes {
			require.Equal(t, "9999", n.url.Port())
		}
		require.Equal(t, int32(0), resolverCalled.Load())

		m, _ := tp.Metrics()
		require.Equal(t, 0, m.AddressResolverCalls, "wrapper never invoked")
		require.Equal(t, 2, m.AddressResolverRewrites, "client detects rewrites")
	})

	t.Run("runner receives nil resolve when AddressResolver not set", func(t *testing.T) {
		t.Parallel()

		var receivedNil atomic.Int32
		tp, err := New(Config{
			URLs:        []*url.URL{testSeedURL(t)},
			Transport:   newResolverTestTransport(t, nodesJSON),
			HealthCheck: NoOpHealthCheck,
			AddressResolverRunner: func(_ context.Context, nodes []NodeInfo, resolve AddressResolverFunc) ([]ResolvedAddress, error) {
				if resolve == nil {
					receivedNil.Add(1)
				}
				out := make([]ResolvedAddress, len(nodes))
				for i, n := range nodes {
					out[i] = ResolvedAddress{Node: n, URL: n.URL}
				}
				return out, nil
			},
		})
		require.NoError(t, err)

		_, err = tp.getNodesInfo(t.Context())
		require.NoError(t, err)
		require.Equal(t, int32(1), receivedNil.Load(),
			"resolve param should be nil when AddressResolver is not set")
	})

	t.Run("runner context cancellation propagated", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		tp, err := New(Config{
			URLs:        []*url.URL{testSeedURL(t)},
			Transport:   newResolverTestTransport(t, nodesJSON),
			HealthCheck: NoOpHealthCheck,
			AddressResolverRunner: func(ctx context.Context, _ []NodeInfo, _ AddressResolverFunc) ([]ResolvedAddress, error) {
				return nil, ctx.Err()
			},
		})
		require.NoError(t, err)

		_, err = tp.getNodesInfo(ctx)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("runner drops all returns ErrAllResolversFailed", func(t *testing.T) {
		t.Parallel()

		tp, err := New(Config{
			URLs:        []*url.URL{testSeedURL(t)},
			Transport:   newResolverTestTransport(t, nodesJSON),
			HealthCheck: NoOpHealthCheck,
			AddressResolverRunner: func(_ context.Context, _ []NodeInfo, _ AddressResolverFunc) ([]ResolvedAddress, error) {
				return nil, nil
			},
		})
		require.NoError(t, err)

		_, err = tp.getNodesInfo(t.Context())
		require.ErrorIs(t, err, ErrAllResolversFailed)
	})

	t.Run("runner error propagated", func(t *testing.T) {
		t.Parallel()

		runnerErr := errors.New("runner failed")
		tp, err := New(Config{
			URLs:        []*url.URL{testSeedURL(t)},
			Transport:   newResolverTestTransport(t, nodesJSON),
			HealthCheck: NoOpHealthCheck,
			AddressResolverRunner: func(_ context.Context, _ []NodeInfo, _ AddressResolverFunc) ([]ResolvedAddress, error) {
				return nil, runnerErr
			},
		})
		require.NoError(t, err)

		_, err = tp.getNodesInfo(t.Context())
		require.ErrorIs(t, err, runnerErr)
	})

	t.Run("full discovery round-trip with runner", func(t *testing.T) {
		t.Parallel()

		tp, err := New(Config{
			URLs:            []*url.URL{testSeedURL(t)},
			Transport:       newResolverTestTransport(t, nodesJSON),
			HealthCheck:     NoOpHealthCheck,
			AddressResolver: stubRewriteResolver,
			AddressResolverRunner: func(ctx context.Context, nodes []NodeInfo, resolve AddressResolverFunc) ([]ResolvedAddress, error) {
				out := make([]ResolvedAddress, 0, len(nodes))
				for _, n := range nodes {
					u, err := resolve(ctx, n)
					if err != nil {
						continue
					}
					if u == nil {
						u = n.URL
					}
					out = append(out, ResolvedAddress{Node: n, URL: u})
				}
				return out, nil
			},
		})
		require.NoError(t, err)

		err = tp.DiscoverNodes(t.Context())
		require.NoError(t, err)

		tp.mu.RLock()
		pool := tp.mu.connectionPool
		tp.mu.RUnlock()
		require.NotNil(t, pool)

		urls := pool.URLs()
		require.Len(t, urls, 2)
		for _, u := range urls {
			require.Equal(t, "9201", u.Port(), "pool URL %q should have rewritten port", u)
		}

		switch p := pool.(type) {
		case *multiServerPool:
			for _, conn := range append(p.mu.ready, p.mu.dead...) {
				require.True(t, conn.Rewritten, "connection %q should be marked Rewritten", conn.URLString)
			}
		case *singleServerPool:
			t.Fatal("expected multiServerPool, got singleServerPool")
		}
	})
}

// ---------------------------------------------------------------------------
// TestAddressResolverRunnerProtocol: observability conformance for both
// the built-in handler and a custom runner running the same resolver.
// ---------------------------------------------------------------------------

func TestAddressResolverRunnerProtocol(t *testing.T) {
	t.Parallel()

	// passthrough runner delegates every call to the per-node resolver,
	// mirroring the built-in handler's behavior without its concurrency control.
	passthroughRunner := func(ctx context.Context, nodes []NodeInfo, resolve AddressResolverFunc) ([]ResolvedAddress, error) {
		out := make([]ResolvedAddress, 0, len(nodes))
		for _, n := range nodes {
			u, err := resolve(ctx, n)
			switch {
			case u != nil:
				out = append(out, ResolvedAddress{Node: n, URL: u})
			case err == nil:
				out = append(out, ResolvedAddress{Node: n, URL: n.URL})
			}
		}
		return out, nil
	}

	type wantMetrics struct {
		calls    int
		rewrites int
		errors   int
	}

	tests := []struct {
		name            string
		inAddrs         map[string]string
		resolver        AddressResolverFunc
		wantPort        map[string]string
		wantRewriteEvts int
		metrics         wantMetrics
	}{
		{
			name:    "rewrite all",
			inAddrs: map[string]string{"alpha": "10.0.0.1:9200", "beta": "10.0.0.2:9200"},
			resolver: func(_ context.Context, node NodeInfo) (*url.URL, error) {
				u := *node.URL
				u.Host = net.JoinHostPort(node.URL.Hostname(), "9201")
				return &u, nil
			},
			wantPort:        map[string]string{"alpha": "9201", "beta": "9201"},
			wantRewriteEvts: 2,
			metrics:         wantMetrics{calls: 2, rewrites: 2, errors: 0},
		},
		{
			name:    "keep all unchanged",
			inAddrs: map[string]string{"alpha": "10.0.0.1:9200", "beta": "10.0.0.2:9200"},
			resolver: func(_ context.Context, _ NodeInfo) (*url.URL, error) {
				return nil, nil //nolint:nilnil // testing (nil, nil) protocol case
			},
			wantPort:        map[string]string{"alpha": "9200", "beta": "9200"},
			wantRewriteEvts: 0,
			metrics:         wantMetrics{calls: 2, rewrites: 0, errors: 0},
		},
		{
			name:    "partial rewrite with error on one node",
			inAddrs: map[string]string{"alpha": "10.0.0.1:9200", "beta": "10.0.0.2:9200"},
			resolver: func(_ context.Context, node NodeInfo) (*url.URL, error) {
				if node.Name == "alpha" {
					u := *node.URL
					u.Host = net.JoinHostPort(node.URL.Hostname(), "9201")
					return &u, fmt.Errorf("partial for %q", node.Name)
				}
				return nil, nil //nolint:nilnil // testing (nil, nil) protocol case
			},
			wantPort:        map[string]string{"alpha": "9201", "beta": "9200"},
			wantRewriteEvts: 1,
			metrics:         wantMetrics{calls: 2, rewrites: 1, errors: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			inputNodes := make(map[string]nodeInfo, len(tt.inAddrs))
			for name, addr := range tt.inAddrs {
				inputNodes["id-"+name] = nodeInfo{
					Name:  name,
					Roles: []string{"data"},
					HTTP:  nodeInfoHTTP{PublishAddress: addr},
				}
			}
			nodesJSON := testNodesResponse(t, inputNodes)

			// Run the same scenario through both paths.
			paths := []struct {
				label  string
				runner AddressResolverRunnerFunc
			}{
				{label: "built-in handler", runner: nil},
				{label: "custom runner", runner: passthroughRunner},
			}

			for _, p := range paths {
				t.Run(p.label, func(t *testing.T) {
					t.Parallel()

					obs := newRecordingObserver()
					var iface ConnectionObserver = obs

					tp, err := New(Config{
						URLs:                  []*url.URL{testSeedURL(t)},
						Transport:             newResolverTestTransport(t, nodesJSON),
						EnableMetrics:         true,
						HealthCheck:           NoOpHealthCheck,
						AddressResolver:       tt.resolver,
						MaxAddressResolvers:   1,
						AddressResolverRunner: p.runner,
					})
					require.NoError(t, err)
					tp.observer.Store(&iface)

					nodes, err := tp.getNodesInfo(t.Context())
					require.NoError(t, err)
					require.Len(t, nodes, len(tt.wantPort))

					gotPorts := make(map[string]string, len(nodes))
					for _, n := range nodes {
						gotPorts[n.Name] = n.url.Port()
					}
					require.Equal(t, tt.wantPort, gotPorts)

					// Verify rewritten flags.
					for _, n := range nodes {
						inputAddr := tt.inAddrs[n.Name]
						_, inputPort, _ := net.SplitHostPort(inputAddr)
						if n.url.Port() != inputPort {
							require.True(t, n.rewritten, "%s: node %q should be rewritten", p.label, n.Name)
						} else {
							require.False(t, n.rewritten, "%s: node %q should not be rewritten", p.label, n.Name)
						}
					}

					// Verify observer events.
					events := obs.getRewriteEvents()
					require.Len(t, events, tt.wantRewriteEvts, "%s: rewrite events", p.label)
					for _, e := range events {
						require.NotEmpty(t, e.ID)
						require.NotEmpty(t, e.Name)
						require.NotEmpty(t, e.OriginalURL)
						require.NotEmpty(t, e.RewrittenURL)
						require.False(t, e.Timestamp.IsZero())
					}

					// Verify metrics match.
					m, _ := tp.Metrics()
					require.Equal(t, tt.metrics.calls, m.AddressResolverCalls, "%s: calls", p.label)
					require.Equal(t, tt.metrics.rewrites, m.AddressResolverRewrites, "%s: rewrites", p.label)
					require.Equal(t, tt.metrics.errors, m.AddressResolverErrors, "%s: errors", p.label)
				})
			}
		})
	}
}
