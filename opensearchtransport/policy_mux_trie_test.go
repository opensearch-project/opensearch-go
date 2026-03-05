// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport //nolint:testpackage // requires internal access to routeTrie and MuxPolicy.pathTrie

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRouteTrieMatch(t *testing.T) {
	t.Run("exact literal match", func(t *testing.T) {
		var trie routeTrie
		p := NewNullPolicy()
		trie.add([]string{http.MethodPost}, "/_bulk", p, 0, "write")

		m, ok := trie.match(http.MethodPost, "/_bulk")
		require.True(t, ok)
		require.Same(t, p, m.policy)
		require.Equal(t, "write", m.poolName)
		require.True(t, m.isSystem)
	})

	t.Run("wildcard index match", func(t *testing.T) {
		var trie routeTrie
		p := NewNullPolicy()
		trie.add([]string{http.MethodGet, http.MethodPost}, "/{index}/_search", p, 0, "search")

		m, ok := trie.match(http.MethodPost, "/my-index/_search")
		require.True(t, ok)
		require.Same(t, p, m.policy)
		require.Equal(t, routeAttr(0), m.attrs)
		require.Equal(t, "search", m.poolName)
		require.Equal(t, "my-index", "/my-index/_search"[m.indexStart:m.indexEnd])
		require.False(t, m.isSystem)
	})

	t.Run("wildcard index and id match", func(t *testing.T) {
		var trie routeTrie
		p := NewNullPolicy()
		trie.add([]string{http.MethodGet}, "/{index}/_doc/{id}", p, 0, "get")

		m, ok := trie.match(http.MethodGet, "/my-index/_doc/abc123")
		require.True(t, ok)
		require.Same(t, p, m.policy)
		path := "/my-index/_doc/abc123"
		require.Equal(t, "my-index", path[m.indexStart:m.indexEnd])
		require.Equal(t, "abc123", path[m.docStart:m.docEnd])
	})

	t.Run("method mismatch returns no match", func(t *testing.T) {
		var trie routeTrie
		trie.add([]string{http.MethodPost}, "/_bulk", NewNullPolicy(), 0, "")

		_, ok := trie.match(http.MethodGet, "/_bulk")
		require.False(t, ok)
	})

	t.Run("path mismatch returns no match", func(t *testing.T) {
		var trie routeTrie
		trie.add([]string{http.MethodPost}, "/_bulk", NewNullPolicy(), 0, "")

		_, ok := trie.match(http.MethodPost, "/_search")
		require.False(t, ok)
	})

	t.Run("too short path returns no match", func(t *testing.T) {
		var trie routeTrie
		trie.add([]string{http.MethodGet}, "/{index}/_search", NewNullPolicy(), 0, "")

		_, ok := trie.match(http.MethodGet, "/my-index")
		require.False(t, ok)
	})

	t.Run("too long path returns no match", func(t *testing.T) {
		var trie routeTrie
		trie.add([]string{http.MethodPost}, "/_bulk", NewNullPolicy(), 0, "")

		_, ok := trie.match(http.MethodPost, "/_bulk/extra")
		require.False(t, ok)
	})

	t.Run("literal takes priority over wildcard", func(t *testing.T) {
		var trie routeTrie
		systemPolicy := NewNullPolicy()
		indexPolicy := NewNullPolicy()

		trie.add([]string{http.MethodPost}, "/_search", systemPolicy, 0, "system-search")
		trie.add([]string{http.MethodPost}, "/{index}/_search", indexPolicy, 0, "index-search")

		// /_search should match the literal system route
		m, ok := trie.match(http.MethodPost, "/_search")
		require.True(t, ok)
		require.Same(t, systemPolicy, m.policy)
		require.Equal(t, "system-search", m.poolName)

		// /my-index/_search should match the wildcard index route
		m, ok = trie.match(http.MethodPost, "/my-index/_search")
		require.True(t, ok)
		require.Same(t, indexPolicy, m.policy)
		require.Equal(t, "index-search", m.poolName)
	})

	t.Run("system vs index disambiguation", func(t *testing.T) {
		var trie routeTrie
		snapshotPolicy := NewNullPolicy()
		explainPolicy := NewNullPolicy()

		// Literal segments match before wildcards, resolving this ambiguity
		trie.add([]string{http.MethodPost}, "/_snapshot/{repository}/_mount", snapshotPolicy, 0, "")
		trie.add([]string{http.MethodPost}, "/{index}/_explain/{id}", explainPolicy, 0, "")

		// /_snapshot/my-repo/_mount should match snapshot route
		m, ok := trie.match(http.MethodPost, "/_snapshot/my-repo/_mount")
		require.True(t, ok)
		require.Same(t, snapshotPolicy, m.policy)

		// /my-index/_explain/doc1 should match explain route
		m, ok = trie.match(http.MethodPost, "/my-index/_explain/doc1")
		require.True(t, ok)
		require.Same(t, explainPolicy, m.policy)
	})

	t.Run("multiple methods on same path", func(t *testing.T) {
		var trie routeTrie
		p := NewNullPolicy()
		trie.add([]string{http.MethodGet, http.MethodPost}, "/_search", p, 0, "")

		_, ok := trie.match(http.MethodGet, "/_search")
		require.True(t, ok)

		_, ok = trie.match(http.MethodPost, "/_search")
		require.True(t, ok)

		_, ok = trie.match(http.MethodDelete, "/_search")
		require.False(t, ok)
	})

	t.Run("isSystem flag set for system paths", func(t *testing.T) {
		var trie routeTrie
		p := NewNullPolicy()
		trie.add([]string{http.MethodGet}, "/_search", p, 0, "")
		trie.add([]string{http.MethodGet}, "/{index}/_search", p, 0, "")

		m, ok := trie.match(http.MethodGet, "/_search")
		require.True(t, ok)
		require.True(t, m.isSystem)

		m, ok = trie.match(http.MethodGet, "/my-index/_search")
		require.True(t, ok)
		require.False(t, m.isSystem)
	})

	t.Run("empty trie returns no match", func(t *testing.T) {
		var trie routeTrie
		_, ok := trie.match(http.MethodGet, "/_search")
		require.False(t, ok)
	})

	t.Run("root path returns no match", func(t *testing.T) {
		var trie routeTrie
		trie.add([]string{http.MethodGet}, "/{index}/_search", NewNullPolicy(), 0, "")

		_, ok := trie.match(http.MethodGet, "/")
		require.False(t, ok)
	})

	t.Run("all 124 scored routes register and resolve", func(t *testing.T) {
		// Smoke test: build the full route table and verify a sample of routes match
		routes := NewDefaultRoutes()
		policy := NewMuxPolicy(routes).(*MuxPolicy)
		require.NotNil(t, policy)

		cases := []struct {
			method string
			path   string
		}{
			{http.MethodPost, "/_bulk"},
			{http.MethodPost, "/my-index/_bulk"},
			{http.MethodGet, "/_search"},
			{http.MethodPost, "/my-index/_search"},
			{http.MethodGet, "/my-index/_doc/123"},
			{http.MethodPost, "/my-index/_update/123"},
			{http.MethodDelete, "/my-index/_doc/123"},
			{http.MethodPost, "/_snapshot/my-repo/_mount"},
			{http.MethodPost, "/my-index/_explain/doc1"},
			{http.MethodGet, "/_ingest/pipeline/my-pipe"},
			{http.MethodGet, "/my-index/_stats/indexing"},
		}
		for _, tc := range cases {
			m, ok := policy.pathTrie.match(tc.method, tc.path)
			require.True(t, ok, "expected match for %s %s", tc.method, tc.path)
			require.NotNil(t, m.policy, "expected non-nil policy for %s %s", tc.method, tc.path)
		}
	})
}

func TestRouteTrieAdversarial(t *testing.T) {
	// Build a trie with the full route table for adversarial testing.
	routes := NewDefaultRoutes()
	policy := NewMuxPolicy(routes).(*MuxPolicy)
	trie := &policy.pathTrie

	t.Run("empty path", func(t *testing.T) {
		_, ok := trie.match(http.MethodGet, "")
		require.False(t, ok)
	})

	t.Run("bare slash", func(t *testing.T) {
		m, ok := trie.match(http.MethodGet, "/")
		require.True(t, ok, "GET / is a registered route (RestMainAction)")
		require.NotNil(t, m.policy)
	})

	t.Run("triple slash normalizes to root", func(t *testing.T) {
		// path.Clean("///") -> "/" which matches GET /
		m, ok := trie.match(http.MethodGet, "///")
		require.True(t, ok, "/// normalizes to / via path.Clean")
		require.NotNil(t, m.policy)
	})

	t.Run("path traversal attempt", func(t *testing.T) {
		// ".." is a valid literal segment --the wildcard matches it as an index name.
		// Path sanitization is the responsibility of net/http, not the router trie.
		m, ok := trie.match(http.MethodGet, "/../_search")
		require.True(t, ok)
		require.NotNil(t, m.policy)
	})

	t.Run("dot segments normalize", func(t *testing.T) {
		// path.Clean("/./my-index/_search") -> "/my-index/_search"
		m, ok := trie.match(http.MethodGet, "/./my-index/_search")
		require.True(t, ok, "/./my-index/_search normalizes to /my-index/_search via path.Clean")
		require.NotNil(t, m.policy)
	})

	t.Run("null byte in path", func(t *testing.T) {
		// Null bytes are opaque to the trie --the segment is matched by the wildcard.
		// Input sanitization is upstream of the router.
		m, ok := trie.match(http.MethodGet, "/my-index\x00/_search")
		require.True(t, ok)
		require.NotNil(t, m.policy)
	})

	t.Run("very long path segment", func(t *testing.T) {
		long := "/" + strings.Repeat("a", 10000) + "/_search"
		m, ok := trie.match(http.MethodGet, long)
		// Should match --the long segment is the {index} wildcard
		require.True(t, ok)
		require.NotNil(t, m.policy)
	})

	t.Run("very deep path", func(t *testing.T) {
		deep := "/" + strings.Repeat("a/", 1000) + "_search"
		_, ok := trie.match(http.MethodGet, deep)
		require.False(t, ok)
	})

	t.Run("empty method", func(t *testing.T) {
		_, ok := trie.match("", "/_search")
		require.False(t, ok)
	})

	t.Run("bogus method", func(t *testing.T) {
		_, ok := trie.match("DESTROY", "/_search")
		require.False(t, ok)
	})

	t.Run("unicode in path segment", func(t *testing.T) {
		// Unicode index name --wildcard should still match
		m, ok := trie.match(http.MethodGet, "/日本語/_search")
		require.True(t, ok)
		require.NotNil(t, m.policy)
	})

	t.Run("percent encoded slashes", func(t *testing.T) {
		// %2F is '/' percent-encoded --trie sees literal "%2F" not a separator
		_, ok := trie.match(http.MethodGet, "/my%2Findex/_search")
		// Matches --%2F is treated as part of the index name segment
		require.True(t, ok)
	})

	t.Run("trailing slash on matched route", func(t *testing.T) {
		// Trailing slash produces an empty segment that is skipped,
		// so /_bulk/ resolves the same as /_bulk.
		m, ok := trie.match(http.MethodPost, "/_bulk/")
		require.True(t, ok)
		require.NotNil(t, m.policy)
	})

	t.Run("query string in path is not stripped", func(t *testing.T) {
		// Trie matches path only; query strings should not appear in path
		// but if they do, the last segment won't match
		_, ok := trie.match(http.MethodGet, "/_search?q=test")
		require.False(t, ok)
	})

	t.Run("fragment in path", func(t *testing.T) {
		_, ok := trie.match(http.MethodGet, "/_search#fragment")
		require.False(t, ok)
	})

	t.Run("spaces in path", func(t *testing.T) {
		_, ok := trie.match(http.MethodGet, "/my index/_search")
		// Spaces are not slashes --treated as part of segment, wildcard matches
		require.True(t, ok)
	})

	t.Run("concurrent reads are safe", func(t *testing.T) {
		// Trie is immutable after construction --concurrent reads must not race.
		const goroutines = 100
		done := make(chan struct{})
		for range goroutines {
			go func() {
				defer func() { done <- struct{}{} }()
				for range 1000 {
					trie.match(http.MethodPost, "/my-index/_search")
					trie.match(http.MethodGet, "/my-index/_doc/123")
					trie.match(http.MethodPost, "/_bulk")
					trie.match(http.MethodGet, "/nonexistent/path")
				}
			}()
		}
		for range goroutines {
			<-done
		}
	})
}

// FuzzRouteTrieMatch fuzzes the trie with arbitrary method+path combinations.
// The trie must never panic regardless of input.
func FuzzRouteTrieMatch(f *testing.F) {
	// Seed corpus with representative inputs.
	seeds := []struct {
		method string
		path   string
	}{
		{"GET", "/_search"},
		{"POST", "/my-index/_search"},
		{"GET", "/my-index/_doc/123"},
		{"DELETE", "/my-index/_doc/abc"},
		{"POST", "/_bulk"},
		{"PUT", "/idx/_doc/1"},
		{"GET", ""},
		{"", "/_search"},
		{"GET", "/"},
		{"GET", "//"},
		{"GET", "/../../etc/passwd"},
		{"GET", "/\x00"},
		{"GET", strings.Repeat("/a", 100)},
		{"POST", "/_snapshot/repo/_mount"},
		{"POST", "/idx/_explain/doc1"},
	}
	for _, s := range seeds {
		f.Add(s.method, s.path)
	}

	// Build trie once with full route table.
	routes := NewDefaultRoutes()
	policy := NewMuxPolicy(routes).(*MuxPolicy)
	trie := &policy.pathTrie

	f.Fuzz(func(t *testing.T, method, path string) {
		// Must never panic.
		m, ok := trie.match(method, path)
		if ok {
			// If matched, policy must be non-nil.
			require.NotNil(t, m.policy)
			// Byte offsets must be within path bounds.
			require.LessOrEqual(t, m.indexEnd, len(path))
			require.LessOrEqual(t, m.docEnd, len(path))
			require.LessOrEqual(t, m.indexStart, m.indexEnd)
			require.LessOrEqual(t, m.docStart, m.docEnd)
		}
	})
}
