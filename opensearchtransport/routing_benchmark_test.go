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

// Reference benchmark data (Apple M4 Pro, arm64, 2026-03-04):
//
//	Policy/Router                                            ns/op    B/op  allocs/op
//	------------------------------------------------------  ------  -----  ---------
//	ConnectionPool/Single                                      148      0       0
//	ConnectionPool/Status_3Nodes                               171     16       0
//	Policy/NullPolicy                                          144      0       0
//	Policy/RoundRobin                                          149      0       0
//	Policy/RolePolicy_Match                                    153      0       0
//	Policy/RolePolicy_NoMatch                                  147      0       0
//	Policy/CoordinatorPolicy_Match                             151      0       0
//	Policy/MuxPolicy                                           181      0       0
//	Policy/IfEnabledPolicy                                     148      0       0
//	Policy/PolicyChain_3Policies                               154      0       0
//	Policy/IndexRouter_3Nodes                                  348      0       0
//	Policy/IndexRouter_10Nodes                                 464      0       0
//	Policy/IndexRouter_NoIndex                                 182      0       0
//	Router/RoundRobin                                          154      0       0
//	Router/DefaultRouter                                       159      0       0
//	Router/MuxRouter                                           158      0       0
//	Router/DefaultRouter/IndexSearch                            438      0       0
//	RouterOperations/MuxRouter/Matched/Search                  160      0       0
//	RouterOperations/DefaultRouter/Matched/Search              259      0       0
//	RouterOperations/MuxRouter/Matched/Bulk                    161      0       0
//	RouterOperations/DefaultRouter/Matched/Bulk                231      0       0
//	RouterOperations/MuxRouter/Matched/Get                     160      0       0
//	RouterOperations/DefaultRouter/Matched/Get                 452      0       0
//	RouterOperations/MuxRouter/Matched/IndexSearch             161      0       0
//	RouterOperations/DefaultRouter/Matched/IndexSearch         431      0       0
//	RouterOperations/MuxRouter/Unmatched/*                    ~160      0       0
//	RouterOperations/DefaultRouter/Unmatched/PUT               445      0       0
//	RouterOperations/DefaultRouter/Unmatched/Delete            444      0       0
//	RouterOperations/DefaultRouter/Unmatched/ClusterHealth     187      0       0
//
// Key observations:
//   - All policies and routers evaluate at 0 allocs/op. Latency ranges from ~144 ns
//     (pass-through) to ~464 ns (IndexRouter with rendezvous hashing over 10 nodes).
//   - Route matching uses a custom trie (routeTrie) with zero-allocation
//     path lookup. Literal segments match before wildcards.
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
// Each benchmark measures policy.Eval() to get the full cost
// of policy-based routing. No actual HTTP requests are made - we're measuring
// the routing decision overhead only.
func BenchmarkPolicy(b *testing.B) {
	connections := []*Connection{
		createBenchBaseConnection("http://bench-coord-1:9200", "bench-coord-1"), // coordinator
		createBenchBaseConnection("http://bench-data-1:9200", "bench-data-1", RoleData),
		createBenchBaseConnection("http://bench-data-2:9200", "bench-data-2", RoleData),
		createBenchBaseConnection("http://bench-ingest-1:9200", "bench-ingest-1", RoleIngest),
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
			hop, err := policy.Eval(b.Context(), req)
			require.NoError(b, err)
			require.Nil(b, hop.Conn)
		}
	})

	b.Run("RoundRobin", func(b *testing.B) {
		policy := NewRoundRobinPolicy()
		configureBenchPolicy(policy, connections)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			hop, err := policy.Eval(b.Context(), req)
			require.NoError(b, err)
			require.NotNil(b, hop.Conn)
		}
	})

	b.Run("RolePolicy_Match", func(b *testing.B) {
		policy, _ := NewRolePolicy(RoleData)
		configureBenchPolicy(policy, connections)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			hop, err := policy.Eval(b.Context(), req)
			require.NoError(b, err)
			require.NotNil(b, hop.Conn)
		}
	})

	b.Run("RolePolicy_NoMatch", func(b *testing.B) {
		policy, _ := NewRolePolicy(RoleWarm) // No warm nodes
		configureBenchPolicy(policy, connections)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			hop, err := policy.Eval(b.Context(), req)
			require.NoError(b, err)
			require.Nil(b, hop.Conn) // No match returns nil conn
		}
	})

	b.Run("CoordinatorPolicy_Match", func(b *testing.B) {
		policy := NewCoordinatorPolicy()
		configureBenchPolicy(policy, connections)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			hop, err := policy.Eval(b.Context(), req)
			require.NoError(b, err)
			require.NotNil(b, hop.Conn)
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
			hop, err := policy.Eval(b.Context(), req)
			require.NoError(b, err)
			require.NotNil(b, hop.Conn)
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
			hop, err := policy.Eval(b.Context(), req)
			require.NoError(b, err)
			require.Nil(b, hop.Conn) // NullPolicy returns nil
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
			hop, err := chain.Eval(b.Context(), req)
			require.NoError(b, err)
			require.NotNil(b, hop.Conn)
		}
	})

	b.Run("IndexRouter_3Nodes", func(b *testing.B) {
		scoredConns := []*Connection{
			createBenchConnection("http://bench-data-1:9200", "bench-data-1", 1*time.Millisecond, 500.0, RoleData),
			createBenchConnection("http://bench-data-2:9200", "bench-data-2", 1*time.Millisecond, 300.0, RoleData),
			createBenchConnection("http://bench-data-3:9200", "bench-data-3", 2*time.Millisecond, 100.0, RoleData),
		}
		policy := NewIndexRouter(indexSlotCacheConfig{})
		_ = policy.DiscoveryUpdate(scoredConns, nil, nil)

		indexReq := &http.Request{
			Method: http.MethodPost,
			URL:    &url.URL{Path: "/my-index/_search"},
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			hop, err := policy.Eval(b.Context(), indexReq)
			require.NoError(b, err)
			require.NotNil(b, hop.Conn)
		}
	})

	b.Run("IndexRouter_10Nodes", func(b *testing.B) {
		scoredConns := make([]*Connection, 10)
		for i := range scoredConns {
			id := "bench-data-" + string(rune('a'+i))
			rtt := time.Duration(500+i*200) * time.Microsecond
			load := float64(100 + i*50)
			scoredConns[i] = createBenchConnection("http://"+id+":9200", id, rtt, load, RoleData)
		}
		policy := NewIndexRouter(indexSlotCacheConfig{})
		_ = policy.DiscoveryUpdate(scoredConns, nil, nil)

		indexReq := &http.Request{
			Method: http.MethodPost,
			URL:    &url.URL{Path: "/my-index/_search"},
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			hop, err := policy.Eval(b.Context(), indexReq)
			require.NoError(b, err)
			require.NotNil(b, hop.Conn)
		}
	})

	b.Run("IndexRouter_NoIndex", func(b *testing.B) {
		scoredConns := []*Connection{
			createBenchConnection("http://bench-data-1:9200", "bench-data-1", 1*time.Millisecond, 100.0, RoleData),
			createBenchConnection("http://bench-data-2:9200", "bench-data-2", 1*time.Millisecond, 100.0, RoleData),
		}
		policy := NewIndexRouter(indexSlotCacheConfig{})
		_ = policy.DiscoveryUpdate(scoredConns, nil, nil)

		sysReq := &http.Request{
			Method: http.MethodGet,
			URL:    &url.URL{Path: "/_cluster/health"},
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			hop, err := policy.Eval(b.Context(), sysReq)
			require.NoError(b, err)
			require.NotNil(b, hop.Conn) // No index -> scores all connections by RTT + congestion
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
		createBenchBaseConnection("http://bench-coord-1:9200", "bench-coord-1"),
		createBenchBaseConnection("http://bench-data-1:9200", "bench-data-1", RoleData),
		createBenchBaseConnection("http://bench-data-2:9200", "bench-data-2", RoleData),
		createBenchBaseConnection("http://bench-ingest-1:9200", "bench-ingest-1", RoleIngest),
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

	b.Run("RoundRobinRouter", func(b *testing.B) {
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

	b.Run("DefaultRouter/IndexSearch", func(b *testing.B) {
		scoredConns := []*Connection{
			createBenchConnection("http://bench-data-1:9200", "bench-data-1", 1*time.Millisecond, 100.0, RoleData),
			createBenchConnection("http://bench-data-2:9200", "bench-data-2", 1*time.Millisecond, 200.0, RoleData),
			createBenchConnection("http://bench-data-3:9200", "bench-data-3", 2*time.Millisecond, 100.0, RoleData),
		}
		router := NewDefaultRouter()
		configureBenchRouter(router, scoredConns)

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
// "unmatched" (falls through to round-robin).
//
// No actual HTTP requests are performed - only the routing decision based on HTTP method and path.
func BenchmarkRouterOperations(b *testing.B) {
	connections := []*Connection{
		createBenchBaseConnection("http://bench-coord-1:9200", "bench-coord-1"),
		createBenchBaseConnection("http://bench-data-1:9200", "bench-data-1", RoleData),
		createBenchBaseConnection("http://bench-ingest-1:9200", "bench-ingest-1", RoleIngest),
	}

	scoredConns := []*Connection{
		createBenchConnection("http://bench-data-1:9200", "bench-data-1", 1*time.Millisecond, 100.0, RoleData),
		createBenchConnection("http://bench-data-2:9200", "bench-data-2", 1*time.Millisecond, 100.0, RoleData),
		createBenchConnection("http://bench-ingest-1:9200", "bench-ingest-1", 1*time.Millisecond, 100.0, RoleIngest),
	}

	// Matched operations: these have registered routes in the MuxPolicy.
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
	// The MuxPolicy falls through to round-robin.
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

		b.Run("DefaultRouter/Matched/"+op.name, func(b *testing.B) {
			router := NewDefaultRouter()
			configureBenchRouter(router, scoredConns)

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

		b.Run("DefaultRouter/Unmatched/"+op.name, func(b *testing.B) {
			router := NewDefaultRouter()
			configureBenchRouter(router, scoredConns)

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
// These are the building blocks used by the routing system: rendezvous hashing,
// path extraction, RTT sorting, and connection scoring. Understanding their
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
			conns[i] = createBenchConnection("http://"+id+":9200", id, 1*time.Millisecond, 100.0, RoleData)
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
			conns[i] = createBenchConnection("http://"+id+":9200", id, rtt, 100.0, RoleData)
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
			conns[i] = createBenchConnection("http://"+id+":9200", id, rtt, 100.0, RoleData)
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

	b.Run("CalcConnScore", func(b *testing.B) {
		conn := createBenchConnection("http://bench-data-1:9200", "bench-data-1", 1*time.Millisecond, 500.0, RoleData)
		info := &shardNodeInfo{Primaries: 1, Replicas: 2}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = calcConnScore(conn, shardCostForReads.forNode(info), "", true)
		}
	})

	b.Run("CalcConnScore_NilInfo", func(b *testing.B) {
		conn := createBenchConnection("http://bench-data-1:9200", "bench-data-1", 1*time.Millisecond, 500.0, RoleData)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = calcConnScore(conn, shardCostForReads.forNode(nil), "", true)
		}
	})

	b.Run("SortConnectionsByRTT_5Nodes", func(b *testing.B) {
		template := make([]*Connection, 5)
		for i := range template {
			id := "bench-node-" + string(rune('a'+i))
			rtt := time.Duration(5-i) * time.Millisecond // Reverse order
			template[i] = createBenchConnection("http://"+id+":9200", id, rtt, 100.0, RoleData)
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
			template[i] = createBenchConnection("http://"+id+":9200", id, rtt, 100.0, RoleData)
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
