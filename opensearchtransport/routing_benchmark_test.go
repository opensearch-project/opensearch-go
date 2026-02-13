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
	"testing"

	"github.com/stretchr/testify/require"
)

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
		pool := &singleConnectionPool{connection: conn}

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
		router := NewDefaultRouter()
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
		router := NewSmartRouter()
		configureBenchRouter(router, connections)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			conn, err := router.Route(b.Context(), req)
			require.NoError(b, err)
			require.NotNil(b, conn)
		}
	})
}

// BenchmarkRouterOperations measures routing overhead for different HTTP operations.
//
// This shows how SmartRouter routing decisions vary by operation type (search vs bulk vs index).
// No actual HTTP requests are performed - only the routing decision based on HTTP method and path.
func BenchmarkRouterOperations(b *testing.B) {
	connections := []*Connection{
		createBenchConnection("http://bench-coord-1:9200", "bench-coord-1"),
		createBenchConnection("http://bench-data-1:9200", "bench-data-1", RoleData),
		createBenchConnection("http://bench-ingest-1:9200", "bench-ingest-1", RoleIngest),
	}

	operations := []struct {
		name   string
		method string
		path   string
	}{
		{"Search", "POST", "/_search"},
		{"Bulk", "POST", "/_bulk"},
		{"Index", "PUT", "/myindex/_doc/1"},
		{"Get", "GET", "/myindex/_doc/1"},
	}

	for _, op := range operations {
		b.Run("SmartRouter/"+op.name, func(b *testing.B) {
			router := NewSmartRouter()
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
	}
}
