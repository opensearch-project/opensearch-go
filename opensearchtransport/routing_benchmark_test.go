// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport //nolint:testpackage // Requires internal access to Connection, pools, policies

import (
	"context"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Reference benchmark data (Apple M4 Pro, arm64, 2025-02-25):
//
//	Policy/Router                                  ns/op    B/op  allocs/op
//	─────────────────────────────────────────────  ──────  ─────  ─────────
//	ConnectionPool/Single                            149      0       0
//	ConnectionPool/Status_3Nodes                     169     16       0
//	Policy/NullPolicy                                145      0       0
//	Policy/RoundRobin                                316      0       0
//	Policy/RolePolicy_Match                          325      0       0
//	Policy/RolePolicy_NoMatch                        145      0       0
//	Policy/CoordinatorPolicy_Match                   326      0       0
//	Policy/MuxPolicy                                 375      0       0
//	Policy/IfEnabledPolicy                           149      0       0
//	Policy/PolicyChain_3Policies                     320      0       0
//	Policy/IndexAffinityPolicy_3Nodes                566      0       0
//	Policy/IndexAffinityPolicy_10Nodes               583      0       0
//	Policy/IndexAffinityPolicy_NoIndex               148      0       0
//	Router/RoundRobin                                155      0       0
//	Router/DefaultRouter                             162      0       0
//	Router/MuxRouter                                 158      0       0
//	Router/SmartRouter                               504     16       1
//	RouterOperations/MuxRouter/Matched/Search        159      0       0
//	RouterOperations/SmartRouter/Matched/Search      226      0       0
//	RouterOperations/MuxRouter/Matched/Bulk          155      0       0
//	RouterOperations/SmartRouter/Matched/Bulk        218      0       0
//	RouterOperations/MuxRouter/Matched/Get           157      0       0
//	RouterOperations/SmartRouter/Matched/Get         520     48       2
//	RouterOperations/MuxRouter/Matched/IndexSearch   155      0       0
//	RouterOperations/SmartRouter/Matched/IndexSearch 489     16       1
//	RouterOperations/MuxRouter/Unmatched/*           ~158     0       0
//	RouterOperations/SmartRouter/Unmatched/PUT      1368    801      35
//	RouterOperations/SmartRouter/Unmatched/Delete   1214    705      31
//	RouterOperations/SmartRouter/Unmatched/Health    769    320      18
//
// Key observations:
//   - All policies evaluate at 0 allocs/op. Latency ranges from ~145 ns (pass-through)
//     to ~580 ns (index affinity with rendezvous hashing over 10 nodes).
//   - SmartRouter matched paths (Search, Bulk) achieve 0 allocs. The Get path has
//     2 allocs from Go's http.ServeMux wildcard capture and sync.Pool miss rate.
//   - SmartRouter unmatched paths (PUT, DELETE, system endpoints) show 18-35 allocs
//     from Go's http.ServeMux.matchingMethods() enumerating allowed methods for 405
//     responses. This is internal to net/http and does not affect matched routes.
//
// Run with: go test -run '^$' -bench 'Benchmark(ConnectionPool|Policy|Router)' -benchmem ./opensearchtransport/

// BenchmarkConnectionPool measures baseline connection pool performance.
//
// These benchmarks measure the overhead of selecting a connection from a pool.
// No actual network I/O is performed - we're only measuring the routing decision logic.
func BenchmarkConnectionPool(b *testing.B) {
	b.Run("Single", func(b *testing.B) {
		conn := &Connection{
			URL: &url.URL{Scheme: "http", Host: "bench1:9200"},
			ID:  "bench1",
		}
		pool := &singleServerPool{connection: conn}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			c, err := pool.Next()
			require.NoError(b, err)
			require.NotNil(b, c)
		}
	})

	b.Run("Status_3Nodes", func(b *testing.B) {
		connections := []*Connection{
			{URL: &url.URL{Scheme: "http", Host: "bench1:9200"}, ID: "bench1"},
			{URL: &url.URL{Scheme: "http", Host: "bench2:9200"}, ID: "bench2"},
			{URL: &url.URL{Scheme: "http", Host: "bench3:9200"}, ID: "bench3"},
		}
		pool := NewConnectionPool(connections, nil)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			c, err := pool.Next()
			require.NoError(b, err)
			require.NotNil(b, c)
		}
	})
}

// BenchmarkPolicy measures the overhead of individual policy evaluation.
//
// Each benchmark measures policy.Eval() + pool.Next() to get the full cost
// of policy-based routing. No actual HTTP requests are made - we're measuring
// the routing decision overhead only.
func BenchmarkPolicy(b *testing.B) {
	connections := []*Connection{
		createBenchConnection("http://bench-coord-1:9200", "bench-coord-1"), // coordinator
		createBenchConnection("http://bench-data-1:9200", "bench-data-1", RoleData),
		createBenchConnection("http://bench-data-2:9200", "bench-data-2", RoleData),
		createBenchConnection("http://bench-ingest-1:9200", "bench-ingest-1", RoleIngest),
	}

	req := &http.Request{
		Method: http.MethodPost,
		URL:    &url.URL{Path: "/_search"},
	}

	b.Run("NullPolicy", func(b *testing.B) {
		policy := NewNullPolicy()

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			pool, err := policy.Eval(b.Context(), req)
			require.NoError(b, err)
			require.Nil(b, pool)
		}
	})

	b.Run("RoundRobin", func(b *testing.B) {
		policy := NewRoundRobinPolicy()
		configureBenchPolicy(policy, connections)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			pool, err := policy.Eval(b.Context(), req)
			require.NoError(b, err)
			require.NotNil(b, pool)
			c, err := pool.Next()
			require.NoError(b, err)
			require.NotNil(b, c)
		}
	})

	b.Run("RolePolicy_Match", func(b *testing.B) {
		policy, _ := NewRolePolicy(RoleData)
		configureBenchPolicy(policy, connections)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			pool, err := policy.Eval(b.Context(), req)
			require.NoError(b, err)
			require.NotNil(b, pool)
			c, err := pool.Next()
			require.NoError(b, err)
			require.NotNil(b, c)
		}
	})

	b.Run("RolePolicy_NoMatch", func(b *testing.B) {
		policy, _ := NewRolePolicy(RoleWarm) // No warm nodes
		configureBenchPolicy(policy, connections)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			pool, err := policy.Eval(b.Context(), req)
			require.NoError(b, err)
			require.Nil(b, pool) // No match returns nil pool
		}
	})

	b.Run("CoordinatorPolicy_Match", func(b *testing.B) {
		policy := NewCoordinatorPolicy()
		configureBenchPolicy(policy, connections)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			pool, err := policy.Eval(b.Context(), req)
			require.NoError(b, err)
			require.NotNil(b, pool)
			c, err := pool.Next()
			require.NoError(b, err)
			require.NotNil(b, c)
		}
	})

	b.Run("MuxPolicy", func(b *testing.B) {
		dataPolicy, _ := NewRolePolicy(RoleData)
		configureBenchPolicy(dataPolicy, connections)

		routes := []Route{
			mustNewRouteMux("POST /_search", dataPolicy),
			mustNewRouteMux("POST /_bulk", dataPolicy),
			mustNewRouteMux("GET /{index}/_doc/{id}", dataPolicy),
		}
		policy := NewMuxPolicy(routes)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			pool, err := policy.Eval(b.Context(), req)
			require.NoError(b, err)
			require.NotNil(b, pool)
			c, err := pool.Next()
			require.NoError(b, err)
			require.NotNil(b, c)
		}
	})

	b.Run("IfEnabledPolicy", func(b *testing.B) {
		policy := NewIfEnabledPolicy(
			func(ctx context.Context, req *http.Request) bool { return true },
			NewNullPolicy(),
			NewNullPolicy(),
		)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			pool, err := policy.Eval(b.Context(), req)
			require.NoError(b, err)
			require.Nil(b, pool) // NullPolicy returns nil
		}
	})

	b.Run("PolicyChain_3Policies", func(b *testing.B) {
		coordPolicy := NewCoordinatorPolicy()
		configureBenchPolicy(coordPolicy, connections)

		dataPolicy, _ := NewRolePolicy(RoleData)
		configureBenchPolicy(dataPolicy, connections)

		roundRobin := NewRoundRobinPolicy()
		configureBenchPolicy(roundRobin, connections)

		chain := NewRouter(coordPolicy, dataPolicy, roundRobin).(*PolicyChain)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			pool, err := chain.Eval(b.Context(), req)
			require.NoError(b, err)
			require.NotNil(b, pool)
			c, err := pool.Next()
			require.NoError(b, err)
			require.NotNil(b, c)
		}
	})

	b.Run("IndexAffinityPolicy_3Nodes", func(b *testing.B) {
		affinityConns := []*Connection{
			createBenchAffinityConnection("http://bench-data-1:9200", "bench-data-1", 1*time.Millisecond, 500.0, RoleData),
			createBenchAffinityConnection("http://bench-data-2:9200", "bench-data-2", 1*time.Millisecond, 300.0, RoleData),
			createBenchAffinityConnection("http://bench-data-3:9200", "bench-data-3", 2*time.Millisecond, 100.0, RoleData),
		}
		policy := NewIndexAffinityPolicy(indexSlotCacheConfig{})
		_ = policy.DiscoveryUpdate(affinityConns, nil, nil)

		indexReq := &http.Request{
			Method: http.MethodPost,
			URL:    &url.URL{Path: "/my-index/_search"},
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			pool, err := policy.Eval(b.Context(), indexReq)
			require.NoError(b, err)
			require.NotNil(b, pool)
			c, err := pool.Next()
			require.NoError(b, err)
			require.NotNil(b, c)
		}
	})

	b.Run("IndexAffinityPolicy_10Nodes", func(b *testing.B) {
		affinityConns := make([]*Connection, 10)
		for i := range affinityConns {
			id := "bench-data-" + string(rune('a'+i))
			rtt := time.Duration(500+i*200) * time.Microsecond
			load := float64(100 + i*50)
			affinityConns[i] = createBenchAffinityConnection("http://"+id+":9200", id, rtt, load, RoleData)
		}
		policy := NewIndexAffinityPolicy(indexSlotCacheConfig{})
		_ = policy.DiscoveryUpdate(affinityConns, nil, nil)

		indexReq := &http.Request{
			Method: http.MethodPost,
			URL:    &url.URL{Path: "/my-index/_search"},
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			pool, err := policy.Eval(b.Context(), indexReq)
			require.NoError(b, err)
			require.NotNil(b, pool)
			c, err := pool.Next()
			require.NoError(b, err)
			require.NotNil(b, c)
		}
	})

	b.Run("IndexAffinityPolicy_NoIndex", func(b *testing.B) {
		affinityConns := []*Connection{
			createBenchAffinityConnection("http://bench-data-1:9200", "bench-data-1", 1*time.Millisecond, 100.0, RoleData),
			createBenchAffinityConnection("http://bench-data-2:9200", "bench-data-2", 1*time.Millisecond, 100.0, RoleData),
		}
		policy := NewIndexAffinityPolicy(indexSlotCacheConfig{})
		_ = policy.DiscoveryUpdate(affinityConns, nil, nil)

		sysReq := &http.Request{
			Method: http.MethodGet,
			URL:    &url.URL{Path: "/_cluster/health"},
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			pool, err := policy.Eval(b.Context(), sysReq)
			require.NoError(b, err)
			require.Nil(b, pool) // No index -> pass-through
		}
	})
}

// BenchmarkRouter measures end-to-end router performance.
//
// These benchmarks measure router.Route() which includes policy chain evaluation,
// policy matching, and connection selection. No actual HTTP requests are performed -
// we're only measuring the routing decision overhead.
func BenchmarkRouter(b *testing.B) {
	connections := []*Connection{
		createBenchConnection("http://bench-coord-1:9200", "bench-coord-1"),
		createBenchConnection("http://bench-data-1:9200", "bench-data-1", RoleData),
		createBenchConnection("http://bench-data-2:9200", "bench-data-2", RoleData),
		createBenchConnection("http://bench-ingest-1:9200", "bench-ingest-1", RoleIngest),
	}

	req := &http.Request{
		Method: http.MethodPost,
		URL:    &url.URL{Path: "/_search"},
	}

	b.Run("RoundRobin", func(b *testing.B) {
		router := NewRouter(NewRoundRobinPolicy())
		configureBenchRouter(router, connections)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			conn, err := router.Route(b.Context(), req)
			require.NoError(b, err)
			require.NotNil(b, conn)
		}
	})

	b.Run("DefaultRouter", func(b *testing.B) {
		router := NewRoundRobinRouter()
		configureBenchRouter(router, connections)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			conn, err := router.Route(b.Context(), req)
			require.NoError(b, err)
			require.NotNil(b, conn)
		}
	})

	b.Run("MuxRouter", func(b *testing.B) {
		router := NewMuxRouter()
		configureBenchRouter(router, connections)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			conn, err := router.Route(b.Context(), req)
			require.NoError(b, err)
			require.NotNil(b, conn)
		}
	})

	b.Run("SmartRouter", func(b *testing.B) {
		affinityConns := []*Connection{
			createBenchAffinityConnection("http://bench-data-1:9200", "bench-data-1", 1*time.Millisecond, 100.0, RoleData),
			createBenchAffinityConnection("http://bench-data-2:9200", "bench-data-2", 1*time.Millisecond, 200.0, RoleData),
			createBenchAffinityConnection("http://bench-data-3:9200", "bench-data-3", 2*time.Millisecond, 100.0, RoleData),
		}
		router := NewSmartRouter()
		configureBenchRouter(router, affinityConns)

		indexReq := &http.Request{
			Method: http.MethodPost,
			URL:    &url.URL{Path: "/my-index/_search"},
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			conn, err := router.Route(b.Context(), indexReq)
			require.NoError(b, err)
			require.NotNil(b, conn)
		}
	})
}

// BenchmarkRouterOperations measures routing overhead for different HTTP operations.
//
// Operations are split into "matched" (route exists in MuxPolicy) and
// "unmatched" (falls through to round-robin). This distinction matters because
// Go's http.ServeMux has significant allocation overhead on the fallthrough path
// when it enumerates allowed methods for 405 responses.
//
// No actual HTTP requests are performed - only the routing decision based on HTTP method and path.
func BenchmarkRouterOperations(b *testing.B) {
	connections := []*Connection{
		createBenchConnection("http://bench-coord-1:9200", "bench-coord-1"),
		createBenchConnection("http://bench-data-1:9200", "bench-data-1", RoleData),
		createBenchConnection("http://bench-ingest-1:9200", "bench-ingest-1", RoleIngest),
	}

	affinityConns := []*Connection{
		createBenchAffinityConnection("http://bench-data-1:9200", "bench-data-1", 1*time.Millisecond, 100.0, RoleData),
		createBenchAffinityConnection("http://bench-data-2:9200", "bench-data-2", 1*time.Millisecond, 100.0, RoleData),
		createBenchAffinityConnection("http://bench-ingest-1:9200", "bench-ingest-1", 1*time.Millisecond, 100.0, RoleIngest),
	}

	// Matched operations: these have registered routes in the MuxPolicy.
	// The route is found on the first ServeMux lookup.
	matchedOps := []struct {
		name   string
		method string
		path   string
	}{
		{"Search", "POST", "/_search"},
		{"Bulk", "POST", "/_bulk"},
		{"Get", "GET", "/myindex/_doc/1"},
		{"IndexSearch", "POST", "/myindex/_search"},
	}

	// Unmatched operations: no route registered for this method+path combination.
	// The MuxPolicy falls through to round-robin. On the fallthrough path,
	// Go's ServeMux enumerates allowed methods (matchingMethods), which
	// allocates heavily for wildcard patterns.
	unmatchedOps := []struct {
		name   string
		method string
		path   string
	}{
		{"Index_PUT", "PUT", "/myindex/_doc/1"},
		{"Delete", "DELETE", "/myindex/_doc/1"},
		{"ClusterHealth", "GET", "/_cluster/health"},
	}

	for _, op := range matchedOps {
		b.Run("MuxRouter/Matched/"+op.name, func(b *testing.B) {
			router := NewMuxRouter()
			configureBenchRouter(router, connections)

			req := &http.Request{
				Method: op.method,
				URL:    &url.URL{Path: op.path},
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				conn, err := router.Route(b.Context(), req)
				require.NoError(b, err)
				require.NotNil(b, conn)
			}
		})

		b.Run("SmartRouter/Matched/"+op.name, func(b *testing.B) {
			router := NewSmartRouter()
			configureBenchRouter(router, affinityConns)

			req := &http.Request{
				Method: op.method,
				URL:    &url.URL{Path: op.path},
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				conn, err := router.Route(b.Context(), req)
				require.NoError(b, err)
				require.NotNil(b, conn)
			}
		})
	}

	for _, op := range unmatchedOps {
		b.Run("MuxRouter/Unmatched/"+op.name, func(b *testing.B) {
			router := NewMuxRouter()
			configureBenchRouter(router, connections)

			req := &http.Request{
				Method: op.method,
				URL:    &url.URL{Path: op.path},
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				conn, err := router.Route(b.Context(), req)
				require.NoError(b, err)
				require.NotNil(b, conn)
			}
		})

		b.Run("SmartRouter/Unmatched/"+op.name, func(b *testing.B) {
			router := NewSmartRouter()
			configureBenchRouter(router, affinityConns)

			req := &http.Request{
				Method: op.method,
				URL:    &url.URL{Path: op.path},
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				conn, err := router.Route(b.Context(), req)
				require.NoError(b, err)
				require.NotNil(b, conn)
			}
		})
	}
}

// BenchmarkRoutingPrimitives measures the cost of low-level routing operations.
//
// These are the building blocks used by the affinity system: rendezvous hashing,
// path extraction, RTT sorting, and affinity scoring. Understanding their
// individual costs helps identify optimization targets.
func BenchmarkRoutingPrimitives(b *testing.B) {
	b.Run("ExtractIndexFromPath", func(b *testing.B) {
		paths := []string{
			"/my-index/_search",
			"/my-index/_doc/abc123",
			"/_cluster/health",
			"/very-long-index-name-with-dashes-2024-01-15/_search",
			"/",
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = extractIndexFromPath(paths[i%len(paths)])
		}
	})

	b.Run("RendezvousTopK_3of5", func(b *testing.B) {
		conns := make([]*Connection, 5)
		for i := range conns {
			id := "bench-node-" + string(rune('a'+i))
			conns[i] = createBenchAffinityConnection("http://"+id+":9200", id, 1*time.Millisecond, 100.0, RoleData)
		}
		var jitter atomic.Int64

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = rendezvousTopK("my-index", "", conns, 3, &jitter, nil, nil)
		}
	})

	b.Run("RendezvousTopK_5of10", func(b *testing.B) {
		conns := make([]*Connection, 10)
		for i := range conns {
			id := "bench-node-" + string(rune('a'+i))
			rtt := time.Duration(500+i*200) * time.Microsecond
			conns[i] = createBenchAffinityConnection("http://"+id+":9200", id, rtt, 100.0, RoleData)
		}
		var jitter atomic.Int64

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = rendezvousTopK("my-index", "", conns, 5, &jitter, nil, nil)
		}
	})

	b.Run("RendezvousTopK_5of10_WithShardNodes", func(b *testing.B) {
		conns := make([]*Connection, 10)
		shardNodes := make(map[string]struct{})
		for i := range conns {
			id := "bench-node-" + string(rune('a'+i))
			rtt := time.Duration(500+i*200) * time.Microsecond
			conns[i] = createBenchAffinityConnection("http://"+id+":9200", id, rtt, 100.0, RoleData)
			if i < 4 { // First 4 nodes host shards
				shardNodes[id] = struct{}{}
			}
		}
		var jitter atomic.Int64

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = rendezvousTopK("my-index", "", conns, 5, &jitter, shardNodes, nil)
		}
	})

	b.Run("AffinityScore", func(b *testing.B) {
		conn := createBenchAffinityConnection("http://bench-data-1:9200", "bench-data-1", 1*time.Millisecond, 500.0, RoleData)
		info := &shardNodeInfo{Primaries: 1, Replicas: 2}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = affinityScore(conn, info, &shardCostForReads)
		}
	})

	b.Run("AffinityScore_NilInfo", func(b *testing.B) {
		conn := createBenchAffinityConnection("http://bench-data-1:9200", "bench-data-1", 1*time.Millisecond, 500.0, RoleData)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = affinityScore(conn, nil, &shardCostForReads)
		}
	})

	b.Run("SortConnectionsByRTT_5Nodes", func(b *testing.B) {
		template := make([]*Connection, 5)
		for i := range template {
			id := "bench-node-" + string(rune('a'+i))
			rtt := time.Duration(5-i) * time.Millisecond // Reverse order
			template[i] = createBenchAffinityConnection("http://"+id+":9200", id, rtt, 100.0, RoleData)
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			conns := make([]*Connection, len(template))
			copy(conns, template)
			sortConnectionsByRTT(conns)
		}
	})

	b.Run("SortConnectionsByRTT_10Nodes", func(b *testing.B) {
		template := make([]*Connection, 10)
		for i := range template {
			id := "bench-node-" + string(rune('a'+i))
			rtt := time.Duration(10-i) * time.Millisecond // Reverse order
			template[i] = createBenchAffinityConnection("http://"+id+":9200", id, rtt, 100.0, RoleData)
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			conns := make([]*Connection, len(template))
			copy(conns, template)
			sortConnectionsByRTT(conns)
		}
	})
}
