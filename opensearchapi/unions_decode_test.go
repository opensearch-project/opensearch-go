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

// TestUnionDecodeRoundTrip exercises the generated discriminated-union decode
// path across the distinct structural variants (scalar branches, string/array,
// map/array-of-raw). For each it decodes wire JSON, checks the discriminant and
// the matching accessor, then re-marshals and confirms the bytes round-trip.
// These accessors, UnmarshalJSON, and MarshalJSON are otherwise unexercised, so
// this covers the bulk of unions_gen.go.
func TestUnionDecodeRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("ErrorCauseHeaderValue string branch", func(t *testing.T) {
		t.Parallel()
		var u opensearchapi.ErrorCauseHeaderValue
		require.NoError(t, json.Unmarshal([]byte(`"boom"`), &u))
		require.Equal(t, opensearchapi.ErrorCauseHeaderValueStringType, u.Type())
		require.Equal(t, "boom", u.String())
		require.JSONEq(t, `"boom"`, string(u.RawJSON()))
		out, err := json.Marshal(&u)
		require.NoError(t, err)
		require.JSONEq(t, `"boom"`, string(out))
	})

	t.Run("ErrorCauseHeaderValue array branch", func(t *testing.T) {
		t.Parallel()
		var u opensearchapi.ErrorCauseHeaderValue
		require.NoError(t, json.Unmarshal([]byte(`["a","b"]`), &u))
		require.Equal(t, opensearchapi.ErrorCauseHeaderValueArrayType, u.Type())
		require.Equal(t, []string{"a", "b"}, u.Array())
		out, err := json.Marshal(&u)
		require.NoError(t, err)
		require.JSONEq(t, `["a","b"]`, string(out))
	})

	t.Run("scalar union bool branch", func(t *testing.T) {
		t.Parallel()
		var u opensearchapi.CommonAggregationsCompositeAggregateKeyValue
		require.NoError(t, json.Unmarshal([]byte(`true`), &u))
		require.Equal(t, opensearchapi.CommonAggregationsCompositeAggregateKeyValueBoolType, u.Type())
		require.True(t, u.Bool())
		out, err := json.Marshal(&u)
		require.NoError(t, err)
		require.JSONEq(t, `true`, string(out))
	})

	t.Run("scalar union float64 branch", func(t *testing.T) {
		t.Parallel()
		var u opensearchapi.CommonAggregationsCompositeAggregateKeyValue
		require.NoError(t, json.Unmarshal([]byte(`42.5`), &u))
		require.Equal(t, opensearchapi.CommonAggregationsCompositeAggregateKeyValueFloat64Type, u.Type())
		require.InEpsilon(t, 42.5, u.Float64(), 1e-9)
		out, err := json.Marshal(&u)
		require.NoError(t, err)
		require.JSONEq(t, `42.5`, string(out))
	})

	t.Run("scalar union string branch", func(t *testing.T) {
		t.Parallel()
		var u opensearchapi.CommonAggregationsCompositeAggregateKeyValue
		require.NoError(t, json.Unmarshal([]byte(`"k"`), &u))
		require.Equal(t, opensearchapi.CommonAggregationsCompositeAggregateKeyValueStringType, u.Type())
		require.Equal(t, "k", u.String())
	})

	t.Run("bucket union map branch", func(t *testing.T) {
		t.Parallel()
		var u opensearchapi.CommonAggregationsMultiBucketAggregateBaseBuckets
		require.NoError(t, json.Unmarshal([]byte(`{"a":{"doc_count":1}}`), &u))
		require.Equal(t, opensearchapi.CommonAggregationsMultiBucketAggregateBaseBucketsMapType, u.Type())
		require.Contains(t, u.Map(), "a")
	})

	t.Run("bucket union array branch", func(t *testing.T) {
		t.Parallel()
		var u opensearchapi.CommonAggregationsMultiBucketAggregateBaseBuckets
		require.NoError(t, json.Unmarshal([]byte(`[{"doc_count":1}]`), &u))
		require.Equal(t, opensearchapi.CommonAggregationsMultiBucketAggregateBaseBucketsArrayType, u.Type())
		require.Len(t, u.Array(), 1)
	})
}

// TestUnionDecodeNullAndErrors covers the null short-circuit (Unknown branch,
// no error) and the invalid-token error path in generated UnmarshalJSON.
func TestUnionDecodeNullAndErrors(t *testing.T) {
	t.Parallel()

	t.Run("null decodes to unknown branch", func(t *testing.T) {
		t.Parallel()
		var u opensearchapi.ErrorCauseHeaderValue
		require.NoError(t, json.Unmarshal([]byte(`null`), &u))
		require.Equal(t, opensearchapi.ErrorCauseHeaderValueUnknownType, u.Type())
		out, err := json.Marshal(&u)
		require.NoError(t, err)
		require.JSONEq(t, `null`, string(out))
	})

	t.Run("unexpected token errors", func(t *testing.T) {
		t.Parallel()
		var u opensearchapi.ErrorCauseHeaderValue
		// A JSON object matches neither the string nor the array branch.
		require.Error(t, json.Unmarshal([]byte(`{"unexpected":true}`), &u))
	})

	t.Run("malformed array payload errors", func(t *testing.T) {
		t.Parallel()
		var u opensearchapi.ErrorCauseHeaderValue
		require.Error(t, json.Unmarshal([]byte(`[`), &u))
	})
}

// TestUnionSetRawMarshals covers the SetRaw escape hatch: staged wire bytes are
// emitted verbatim by MarshalJSON when no typed branch is set, and SetRaw clears
// any prior typed branch.
func TestUnionSetRawMarshals(t *testing.T) {
	t.Parallel()

	var u opensearchapi.ErrorCauseHeaderValue
	u.SetRaw(json.RawMessage(`"staged"`))
	require.Equal(t, opensearchapi.ErrorCauseHeaderValueUnknownType, u.Type())
	out, err := json.Marshal(&u)
	require.NoError(t, err)
	require.JSONEq(t, `"staged"`, string(out))

	// Setting raw after a typed branch clears the typed value.
	typed := opensearchapi.NewErrorCauseHeaderValueFromString("typed")
	typed.SetRaw(json.RawMessage(`["raw"]`))
	require.Equal(t, opensearchapi.ErrorCauseHeaderValueUnknownType, typed.Type())
	out, err = json.Marshal(&typed)
	require.NoError(t, err)
	require.JSONEq(t, `["raw"]`, string(out))
}
