// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchapi

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/jsonx"
)

// unionDecoder is a generated union with both decode methods.
type unionDecoder[T any] interface {
	*T
	json.Unmarshaler
	UnmarshalJSONFrom(*jsonx.TextDecoder) error
}

// oneByteReader returns its bytes one at a time, so a decoder reading it refills
// and reuses its buffer at every byte.
type oneByteReader struct{ b []byte }

func (r *oneByteReader) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	p[0], r.b = r.b[0], r.b[1:]

	return 1, nil
}

// requireSameUnion checks that UnmarshalJSONFrom, which the client's codec
// calls, decodes each payload to the union UnmarshalJSON decodes it to: the
// same branch, value and raw bytes, or an error for the same payloads. Each
// payload is decoded alone, as an array element after whitespace and a comma,
// and through a decoder that refills its buffer at every byte, where the raw
// bytes are not kept and only the branch and value must agree.
func requireSameUnion[T any, P unionDecoder[T]](t *testing.T, payloads []string) {
	t.Helper()

	for _, payload := range payloads {
		var want T
		wantErr := P(&want).UnmarshalJSON([]byte(payload))

		var alone T
		err := jsonx.Unmarshal([]byte(payload), P(&alone))
		require.Equal(t, wantErr == nil, err == nil, "%s: UnmarshalJSON: %v, UnmarshalJSONFrom: %v", payload, wantErr, err)
		if wantErr != nil {
			continue
		}
		require.True(t, reflect.DeepEqual(want, alone), "%s:\nUnmarshalJSON:     %#v\nUnmarshalJSONFrom: %#v", payload, want, alone)

		var inArray []T
		require.NoError(t, jsonx.Unmarshal([]byte("[ null ,\n\t"+payload+" ]"), &inArray), payload)
		require.Len(t, inArray, 2)
		require.True(t, reflect.DeepEqual(want, inArray[1]), "%s in an array:\nUnmarshalJSON:     %#v\nUnmarshalJSONFrom: %#v",
			payload, want, inArray[1])

		var streamed T
		dec := jsonx.NewTextDecoder(&oneByteReader{b: []byte(payload)})
		require.NoError(t, jsonx.UnmarshalDecode(dec, P(&streamed)), payload)
		wantType := reflect.ValueOf(P(&want)).MethodByName("Type").Call(nil)[0].Interface()
		gotType := reflect.ValueOf(P(&streamed)).MethodByName("Type").Call(nil)[0].Interface()
		require.Equal(t, wantType, gotType, "%s streamed: the branch", payload)
		if fmt.Sprint(wantType) != "unknown" {
			// MarshalJSON writes the decoded value when a branch is set.
			wantJSON, err := json.Marshal(&want)
			require.NoError(t, err)
			gotJSON, err := json.Marshal(&streamed)
			require.NoError(t, err)
			require.JSONEq(t, string(wantJSON), string(gotJSON), "%s streamed: the value", payload)
		}
	}
}

func TestUnionUnmarshalJSONFromMatchesUnmarshalJSON(t *testing.T) {
	t.Parallel()

	t.Run("merge: MGetRespItem", func(t *testing.T) {
		t.Parallel()
		requireSameUnion[MGetRespItem](t, []string{
			`{"_index":"i","_id":"1","_version":2,"found":true,"_source":{"a":[1,{"b":"<&>"}]}}`,
			`{"_index":"i","_id":"2","found":false}`,
			`{"_index":"i","_id":"3","error":{"type":"index_not_found_exception","reason":"no such index","index":"i"}}`,
			`{ "_index" : "i", "_id" : "4", "error" : { "type" : "x", "reason" : "spaced out" } , "extra" : [ 1 ] }`,
			`{"_index":"i","_id":"5","found":true,"unknown_field":{"kept":"in raw"}}`,
			`null`,
			`{}`,
			`{"_index":1}`,
			`{"_index":"i",`,
		})
	})

	t.Run("token class: MinimumShouldMatch", func(t *testing.T) {
		t.Parallel()
		requireSameUnion[MinimumShouldMatch](t, []string{`2`, `-1`, `"75%"`, `"3<90%"`, `null`, `true`, `{}`, `[1]`, `"x`})
	})

	t.Run("token class: FieldValue", func(t *testing.T) {
		t.Parallel()
		requireSameUnion[FieldValue](t, []string{`1.5`, `"s"`, `true`, `false`, `null`, `{"a":1}`, `[1,2]`, `tru`})
	})
}

// TestUnionStreamedErrorBranchDecodes decodes an mget error document through a
// decoder that refills its buffer at every byte, so the bytes the merge strategy
// would decode the error branch from are overwritten: the branch must still come
// out whole, from the members the decoder read.
func TestUnionStreamedErrorBranchDecodes(t *testing.T) {
	t.Parallel()

	payload := `{"_index":"i","_id":"3","error":{"type":"index_not_found_exception","reason":"no such index"}}`
	var u MGetRespItem
	dec := jsonx.NewTextDecoder(&oneByteReader{b: []byte(payload)})
	require.NoError(t, jsonx.UnmarshalDecode(dec, &u))

	failure, err := u.MultiGetError()
	require.NoError(t, err)
	require.Equal(t, "i", failure.Index)
	require.Equal(t, "3", failure.ID)
	require.Equal(t, "index_not_found_exception", failure.Error.Type)
	require.NotNil(t, failure.Error.Reason)
	require.Equal(t, "no such index", *failure.Error.Reason)
}

// TestUnionUnmarshalJSONFromIsCalled checks that the client's codec takes the
// one-pass method: a union decoded through it keeps the bytes of the input,
// not a copy.
func TestUnionUnmarshalJSONFromIsCalled(t *testing.T) {
	t.Parallel()

	data := []byte(`{"docs":[{"_index":"i","_id":"1","found":true}]}`)
	var resp MGetResp
	require.NoError(t, jsonx.Unmarshal(data, &resp))
	raw := resp.Docs[0].RawJSON()
	require.JSONEq(t, `{"_index":"i","_id":"1","found":true}`, string(raw))
	require.Same(t, &data[strings.Index(string(data), `{"_index"`)], &raw[0], "the raw bytes are a slice of the input")

	// The codec reports a union's error as encoding/json would.
	err := jsonx.Unmarshal([]byte(`{"docs":[{"_index":1}]}`), &resp)
	var typeErr *json.UnmarshalTypeError
	require.ErrorAs(t, err, &typeErr)
}
