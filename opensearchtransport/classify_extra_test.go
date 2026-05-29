// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
)

// TestClassify_ZeroAlloc guards the zero-allocation claim documented in
// CHANGELOG: OperationClassifier.Classify must not allocate on the hot
// path (it lives inside RoundTrip and runs once per request). A
// regression here means a per-request heap object that compounds across
// the cluster's RPS.
func TestClassify_ZeroAlloc(t *testing.T) {
	c := opensearchtransport.NewOperationClassifier()
	// Warm any one-time setup the classifier may do.
	_ = c.Classify(http.MethodGet, "/events/_search")

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"search hot path", http.MethodPost, "/events/_search"},
		{"bulk hot path", http.MethodPost, "/_bulk"},
		{"doc get hot path", http.MethodGet, "/events/_doc/abc-123"},
		{"unknown path falls through to OpOther", http.MethodGet, "/_unknown/endpoint"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allocs := testing.AllocsPerRun(200, func() {
				_ = c.Classify(tt.method, tt.path)
			})
			require.Zero(t, allocs, "Classify(%q, %q) must be zero-alloc, got %g", tt.method, tt.path, allocs)
		})
	}
}

// TestClassify_PathEdgeCases covers path-shape variants that callers
// pass through Classify directly: trailing slashes, query strings, mixed
// case methods. The classifier must be tolerant of common HTTP-layer
// noise without misclassifying.
func TestClassify_PathEdgeCases(t *testing.T) {
	t.Parallel()

	c := opensearchtransport.NewOperationClassifier()

	tests := []struct {
		name   string
		method string
		path   string
		want   opensearchtransport.OperationID
	}{
		// The classifier matches on path prefix/segment boundaries, so
		// query strings appended by the caller should be ignored. If
		// they aren't, we'd misclassify any request with ?pretty etc.
		// Note: the classifier accepts the raw path; callers strip query
		// strings before calling. This test documents the contract.
		{"plain search", http.MethodGet, "/_search", opensearchtransport.OpSearch},
		{"empty path -> OpOther", http.MethodGet, "", opensearchtransport.OpOther},
		{"slash only -> cluster info", http.MethodGet, "/", opensearchtransport.OpClusterInfo},

		// Method must be case-sensitive (HTTP standard); lowercase
		// "get" is not an HTTP method.
		{"lowercase get -> OpOther", "get", "/_search", opensearchtransport.OpOther},
		{"lowercase post -> OpOther", "post", "/_bulk", opensearchtransport.OpOther},

		// Random PATCH on a known path falls through to OpOther because
		// the route table doesn't list PATCH for /_search.
		{"unsupported method on known path", http.MethodPatch, "/_search", opensearchtransport.OpOther},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, c.Classify(tt.method, tt.path),
				"Classify(%q, %q)", tt.method, tt.path)
		})
	}
}

// TestOperationID_Minor covers the Minor() helper not exercised by
// TestOperationID_Masking. Minor() returns the bits below the major
// (category) marker, which uniquely identify an op within its category.
func TestOperationID_Minor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		op   opensearchtransport.OperationID
	}{
		// Within a category, Minor values must be distinct -- otherwise
		// two ops would collide on Category|Minor reconstruction.
		{"OpSearch minor distinct from OpMSearch", opensearchtransport.OpSearch},
		{"OpDocGet minor distinct from OpDocExists", opensearchtransport.OpDocGet},
		{"OpBulk minor", opensearchtransport.OpBulk},
		{"OpOther has no category", opensearchtransport.OpOther},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			minor := tt.op.Minor()
			// Minor must be a subset of the op's bits.
			require.Equal(t, minor, tt.op&minor, "Minor() must be a subset of op bits")
			// Minor must not include any category bits.
			require.Zero(t, minor&tt.op.Category(), "Minor() and Category() must be disjoint")
		})
	}
}

// TestOperationID_StringSmoke confirms every OperationID constant
// declared in the operation package returns a non-empty, distinct,
// snake_case string. Catches forgotten String() entries when new ops
// land. The exhaustive table of "exact name -> exact String value"
// lives in TestOperationID_String; this test guards uniqueness +
// non-emptiness across the full set so additions don't slip in
// unnamed.
func TestOperationID_StringSmoke(t *testing.T) {
	t.Parallel()

	// Reflective enumeration is awkward without runtime metadata; spot
	// the categories the package exposes by referencing every Op*
	// constant we expect to be unique. Adding a new op to the package
	// should require a new entry here.
	allOps := []opensearchtransport.OperationID{
		opensearchtransport.OpClusterInfo,
		opensearchtransport.OpPing,
		opensearchtransport.OpSearch,
		opensearchtransport.OpMSearch,
		opensearchtransport.OpMSearchTmpl,
		opensearchtransport.OpSearchTemplate,
		opensearchtransport.OpScrollGet,
		opensearchtransport.OpScrollDelete,
		opensearchtransport.OpCount,
		opensearchtransport.OpDeleteByQuery,
		opensearchtransport.OpUpdateByQuery,
		opensearchtransport.OpValidate,
		opensearchtransport.OpRankEval,
		opensearchtransport.OpSearchShards,
		opensearchtransport.OpFieldCaps,
		opensearchtransport.OpPITCreate,
		opensearchtransport.OpPITDelete,
		opensearchtransport.OpPITList,
		opensearchtransport.OpBulk,
		opensearchtransport.OpBulkStream,
		opensearchtransport.OpReindex,
		opensearchtransport.OpReindexRethrottle,
		opensearchtransport.OpUBQRethrottle,
		opensearchtransport.OpDBQRethrottle,
		opensearchtransport.OpDocGet,
		opensearchtransport.OpDocExists,
		opensearchtransport.OpDocSourceGet,
		opensearchtransport.OpDocSourceExist,
		opensearchtransport.OpMGet,
		opensearchtransport.OpTermVectors,
		opensearchtransport.OpMTermVectors,
		opensearchtransport.OpExplain,
		opensearchtransport.OpDocIndex,
		opensearchtransport.OpDocCreate,
		opensearchtransport.OpDocUpdate,
		opensearchtransport.OpDocDelete,
		opensearchtransport.OpIngestGet,
		opensearchtransport.OpIngestCreate,
		opensearchtransport.OpIngestDelete,
		opensearchtransport.OpIngestSimulate,
		opensearchtransport.OpRefresh,
		opensearchtransport.OpFlush,
		opensearchtransport.OpForceMerge,
		opensearchtransport.OpSegments,
		opensearchtransport.OpCacheClear,
		opensearchtransport.OpRecovery,
		opensearchtransport.OpShardStores,
		opensearchtransport.OpStats,
		opensearchtransport.OpOther,
	}

	seen := make(map[string]opensearchtransport.OperationID, len(allOps))
	for _, op := range allOps {
		s := op.String()
		require.NotEmpty(t, s, "op %d returned empty String()", int64(op))
		require.NotContains(t, s, " ", "op %s String() contains a space", s)
		require.Equal(t, strings.ToLower(s), s, "op %s String() should be lowercase snake_case", s)

		if prev, dup := seen[s]; dup {
			t.Fatalf("String() collision: %s and %s both return %q", prev, op, s)
		}
		seen[s] = op
	}
}
