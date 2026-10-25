// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchapi

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/jsonx"
)

// marshalJSONOnly hides a value's MarshalJSONTo, so the codec encodes it through
// MarshalJSON, as it did before MarshalJSONTo was generated.
type marshalJSONOnly struct{ v json.Marshaler }

func (m marshalJSONOnly) MarshalJSON() ([]byte, error) { return m.v.MarshalJSON() }

// TestMarshalJSONToMatchesMarshalJSON checks that the codec writes the bytes
// through a generated MarshalJSONTo that it wrote through MarshalJSON, and
// fails where it failed.
func TestMarshalJSONToMatchesMarshalJSON(t *testing.T) {
	t.Parallel()

	var raw MinimumShouldMatch
	raw.SetRaw(json.RawMessage(` { "spaced" : [ 1 , 2 ] } `))
	var decoded MGetRespItem
	require.NoError(t, jsonx.Unmarshal([]byte(`{"_index":"i","_id":"1","found":true,"_source":{"a":"<&>"}}`), &decoded))

	tests := []struct {
		name string
		v    json.Marshaler
	}{
		{name: "a union holding a branch", v: NewMinimumShouldMatchFromString("75%")},
		{name: "a union holding raw bytes", v: raw},
		{name: "an empty union", v: MinimumShouldMatch{}},
		{name: "a decoded union", v: decoded},
		{name: "an enum", v: RestStatusNotFound},
		{name: "an enum value with no name", v: RestStatus(9999)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, ok := tt.v.(interface {
				MarshalJSONTo(*jsonx.TextEncoder) error
			})
			require.True(t, ok, "%T has MarshalJSONTo", tt.v)

			for _, wrap := range []func(any) any{
				func(v any) any { return v },
				func(v any) any { return map[string]any{"in": []any{v, v}} },
			} {
				want, wantErr := jsonx.Marshal(wrap(marshalJSONOnly{tt.v}))
				got, err := jsonx.Marshal(wrap(tt.v))
				require.Equal(t, wantErr == nil, err == nil, "MarshalJSON: %v, MarshalJSONTo: %v", wantErr, err)
				require.Equal(t, string(want), string(got))
			}
		})
	}
}
