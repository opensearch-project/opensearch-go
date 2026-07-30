// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// TestUnionBranchAccessorErrors pins the contract that makes a branch accessor
// safe to call: asking for a branch the union does not hold returns an error
// naming the branch that IS held, rather than a zero value.
//
// The mget case is what motivated it. A document that genuinely was not found
// decodes as the GetResult branch with Found=false, so a zero GetResultBase
// returned for an item actually holding MultiGetError is indistinguishable
// from real data: without the error a caller silently reads "not found" for what
// is really an index-level failure.
func TestUnionBranchAccessorErrors(t *testing.T) {
	t.Parallel()

	const mgetErrDoc = `{"_index":"i","_id":"1","error":{"type":"index_not_found_exception","reason":"no such index"}}`

	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{
			// The branch that IS set returns cleanly, no error.
			name: "held branch returns no error",
			check: func(t *testing.T) {
				t.Helper()
				var u opensearchapi.MGetRespItem
				require.NoError(t, json.Unmarshal([]byte(mgetErrDoc), &u))
				require.Equal(t, opensearchapi.MGetRespItemMultiGetErrorType, u.Type())

				got, err := u.MultiGetError()
				require.NoError(t, err)
				require.Equal(t, "1", got.ID)
			},
		},
		{
			// The zero value returned alongside the error is a valid-looking
			// found=false document, which is exactly why the error is the signal.
			name: "unheld branch reports the branch actually held",
			check: func(t *testing.T) {
				t.Helper()
				var u opensearchapi.MGetRespItem
				require.NoError(t, json.Unmarshal([]byte(mgetErrDoc), &u))

				zero, err := u.GetResult()
				require.Error(t, err)
				require.False(t, zero.Found, "value is the zero struct; the error is the signal")

				var branchErr *opensearchapi.UnionBranchError
				require.ErrorAs(t, err, &branchErr)
				require.Equal(t, "MGetRespItem", branchErr.Union)
				require.Equal(t, "GetResult", branchErr.Want)
				require.Equal(t, "MultiGetError", branchErr.Got)
				require.Contains(t, err.Error(), "holds branch MultiGetError, not GetResult")
			},
		},
		{
			// A union that was never decoded must not hand back a value that
			// looks decoded.
			name: "undecoded union reports the unknown branch",
			check: func(t *testing.T) {
				t.Helper()
				var u opensearchapi.MGetRespItem
				require.Equal(t, opensearchapi.MGetRespItemUnknownType, u.Type())

				_, err := u.GetResult()
				require.Error(t, err)

				var branchErr *opensearchapi.UnionBranchError
				require.ErrorAs(t, err, &branchErr)
				require.Equal(t, "unknown", branchErr.Got)
				require.Contains(t, err.Error(), "holds branch unknown")
			},
		},
		{
			// One shared error type, so Union is what distinguishes failures
			// from different unions in a single call chain.
			name: "Union names the union that failed",
			check: func(t *testing.T) {
				t.Helper()
				var sort opensearchapi.FieldValue
				require.NoError(t, json.Unmarshal([]byte(`"asc"`), &sort))

				_, err := sort.Float64()
				require.Error(t, err)

				var branchErr *opensearchapi.UnionBranchError
				require.ErrorAs(t, err, &branchErr)
				require.Equal(t, "FieldValue", branchErr.Union)
				require.Equal(t, "Float64", branchErr.Want)
				require.Equal(t, "String", branchErr.Got)
			},
		},
		{
			// Primitive branches are affected too: a zero int64 or empty string
			// is a plausible decoded value, so the error is the only signal.
			name: "primitive branch zero is indistinguishable without the error",
			check: func(t *testing.T) {
				t.Helper()
				var u opensearchapi.SearchHitsMetadataTotal
				require.NoError(t, json.Unmarshal([]byte(`{"value":1,"relation":"eq"}`), &u))

				n, err := u.Int64()
				require.Error(t, err, "union holds the struct branch, not int64")
				require.Zero(t, n, "a zero count is a plausible real value")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.check(t)
		})
	}
}
