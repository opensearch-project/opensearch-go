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

// TestUnionUnmarshalResetsStaleState guards the F4 regression: a union's
// UnmarshalJSON must clear any prior branch state (u.value, and u.typ where
// present) before decoding. A value reused as a decode target -- built via a
// New...From...() constructor or already decoded to one branch -- otherwise
// returns the stale branch through Type()/As<T>()/MarshalJSON instead of the
// freshly decoded bytes. The null early-return path has the same requirement.
func TestUnionUnmarshalResetsStaleState(t *testing.T) {
	t.Parallel()

	staleAvg := float64(10)

	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{
			// Lazy union built with a constructor (u.value pre-populated):
			// reuse must decode the fresh raw, not return the stale value.
			name: "lazy union reuse decodes fresh raw",
			check: func(t *testing.T) {
				t.Helper()
				u := opensearchapi.NewSearchResultAggregationsValueFromAvg(
					opensearchapi.CommonAggregationsAvgAggregate{
						CommonAggregationsSingleMetricAggregateBase: opensearchapi.CommonAggregationsSingleMetricAggregateBase{Value: &staleAvg},
					},
				)
				require.NoError(t, json.Unmarshal([]byte(`{"value":2.5}`), &u))
				got, err := u.AsAvg()
				require.NoError(t, err)
				require.NotNil(t, got.Value)
				require.InDelta(t, 2.5, *got.Value, 1e-9)
			},
		},
		{
			// Merged union pre-populated on the error branch: decoding a
			// success payload must switch the active branch.
			name: "merged union reuse switches branch",
			check: func(t *testing.T) {
				t.Helper()
				u := opensearchapi.NewMGetRespBodyDocsItemFromMGetMultiGetError(
					opensearchapi.MGetMultiGetError{ID: "1", Index: "i"},
				)
				require.NoError(t, json.Unmarshal(
					[]byte(`{"_index":"i","_id":"1","found":true}`), &u))
				require.Equal(t, opensearchapi.MGetRespBodyDocsItemGetResultType, u.Type())
				require.True(t, u.GetResult().Found)
			},
		},
		{
			// The null early-return path must also clear prior branch state.
			name: "merged union null payload clears prior branch",
			check: func(t *testing.T) {
				t.Helper()
				var u opensearchapi.MGetRespBodyDocsItem
				require.NoError(t, json.Unmarshal(
					[]byte(`{"_index":"i","_id":"1","found":true}`), &u))
				require.Equal(t, opensearchapi.MGetRespBodyDocsItemGetResultType, u.Type())

				require.NoError(t, json.Unmarshal([]byte(`null`), &u))
				require.Equal(t, opensearchapi.MGetRespBodyDocsItemUnknownType, u.Type())

				b, err := u.MarshalJSON()
				require.NoError(t, err)
				require.JSONEq(t, `null`, string(b))
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
