// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchapi

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"

	json "github.com/opensearch-project/opensearch-go/v5/internal/jsonx"
)

// mgetRespCorpus are mget responses MGetResp.UnmarshalJSONFrom must decode as the
// generated decoder does.
var mgetRespCorpus = map[string]string{
	"every kind of document": `{"docs":[` +
		`{"_index":"i","_id":"1","_version":2,"_seq_no":5,"_primary_term":1,"_routing":"r","found":true,` +
		`"_source":{"a":[1,{"b":"c"}]},"fields":{"f":[1]}},` +
		`{"_index":"i","_id":"2","found":false},` +
		`{"_index":"i","_id":"3","error":{"type":"index_not_found_exception","reason":"no such index","index":"i"}},` +
		`null,` +
		`{"_index":"i","_id":"4","found":true,"_source":{},"novel":{"x":1}}]}`,
	"no docs":                 `{"docs":[]}`,
	"null docs":               `{"docs":null}`,
	"no docs member":          `{}`,
	"null response":           `null`,
	"keys in other case":      `{"DOCS":[{"_ID":"a","Found":true,"_Source":{"k":1}}]}`,
	"unknown members":         `{"took":1,"docs":[{"_id":"a","found":true}],"other":[{}]}`,
	"escaped names":           `{"d\u006fcs":[{"_\u0069d":"a","found":true}]}`,
	"an error without a type": `{"docs":[{"_id":"a","error":{"reason":"r"}}]}`,
	"a repeated member":       `{"docs":[{"_id":"a","found":true}],"docs":[{"_id":"b","found":false}]}`,
	"whitespace":              " {\n \"docs\" : [ { \"_id\" : \"a\" , \"found\" : true } ]\n}\n",
}

// TestMGetRespDecodesAsTheGeneratedDecoder decodes each response of the corpus
// with MGetResp.UnmarshalJSONFrom, from bytes and from a reader that hands over a
// byte at a time, and checks that it gets what the generated decoder gets.
func TestMGetRespDecodesAsTheGeneratedDecoder(t *testing.T) {
	t.Parallel()

	for name, body := range mgetRespCorpus {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var want mgetRespGenericDecode
			require.NoError(t, json.Unmarshal([]byte(body), &want))
			var got MGetResp
			require.NoError(t, json.Unmarshal([]byte(body), &got))
			require.Equal(t, MGetResp(want), got)

			var wantStreamed mgetRespGenericDecode
			require.NoError(t, json.UnmarshalDecode(json.NewTextDecoder(iotest.OneByteReader(strings.NewReader(body))), &wantStreamed))
			var gotStreamed MGetResp
			require.NoError(t, json.UnmarshalDecode(json.NewTextDecoder(iotest.OneByteReader(strings.NewReader(body))), &gotStreamed))
			require.Equal(t, MGetResp(wantStreamed), gotStreamed, "decoded from a reader")
		})
	}
}

// TestMGetRespDecodeFailures checks that a response the generated decoder refuses
// is refused here too.
func TestMGetRespDecodeFailures(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"docs not an array":           `{"docs":{}}`,
		"a document not an object":    `{"docs":[5]}`,
		"a found not a boolean":       `{"docs":[{"_id":"a","found":"yes"}]}`,
		"an error of the wrong shape": `{"docs":[{"_id":"a","error":{"type":5}}]}`,
		"truncated":                   `{"docs":[{"_id":"a"`,
		"not an object":               `[1]`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var want mgetRespGenericDecode
			require.Error(t, json.Unmarshal([]byte(body), &want), "the generated decoder refuses it")
			var got MGetResp
			require.Error(t, json.Unmarshal([]byte(body), &got))
		})
	}
}

// TestMGetRespDecodeUsesTheSeed checks that the documents go into the capacity
// Docs already has, which the mget dispatch sizes for the documents asked for.
func TestMGetRespDecodeUsesTheSeed(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	b.WriteString(`{"docs":[`)
	for i := range 20 {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"_index":"i","_id":"d%d","found":true,"_source":{}}`, i)
	}
	b.WriteString(`]}`)

	resp := MGetResp{Docs: make([]MGetRespItem, 0, 20)}
	backing := &resp.Docs[:1][0]
	require.NoError(t, json.Unmarshal([]byte(b.String()), &resp))
	require.Len(t, resp.Docs, 20)
	require.Same(t, backing, &resp.Docs[0], "decoded into the seed's array")
	for i := range resp.Docs {
		got, err := resp.Docs[i].GetResult()
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("d%d", i), got.ID)
	}
}

// TestMGetRespDecoderKnowsEveryField fails when the generated MGetResp or
// GetResult gains or renames a field, which MGetResp.UnmarshalJSONFrom would
// otherwise skip as an unknown member.
func TestMGetRespDecoderKnowsEveryField(t *testing.T) {
	t.Parallel()

	tags := func(v any) []string {
		var out []string
		for f := range reflect.TypeOf(v).Fields() {
			if f.IsExported() {
				out = append(out, f.Tag.Get("json"))
			}
		}

		return out
	}
	const update = "update MGetResp.UnmarshalJSONFrom to decode the new field, then this list"
	require.Equal(t, []string{"docs"}, tags(MGetResp{}), update)
	require.Equal(t, []string{
		"_id", "_index", "_primary_term,omitempty", "_routing,omitempty", "_seq_no,omitempty",
		"_source,omitempty", "_type,omitempty", "_version,omitempty", "fields,omitempty", "found",
	}, tags(GetResult{}), update)
}

// TestMGetRespDecoderKnowsTheUnion fails when the generated MGetRespItem gains a
// branch, which MGetResp.UnmarshalJSONFrom, written for two, would not decode.
func TestMGetRespDecoderKnowsTheUnion(t *testing.T) {
	t.Parallel()

	require.Equal(t, "GetResult", MGetRespItemGetResultType.String())
	require.Equal(t, "MultiGetError", MGetRespItemMultiGetErrorType.String())
	require.Equal(t, "unknown", (MGetRespItemMultiGetErrorType + 1).String(),
		"update MGetResp.UnmarshalJSONFrom for the new branch, then this test")
}

// TestMGetRespDecodeAllocations bounds what decoding 100 found documents into a
// seeded response allocates: each document's _id and the copy of its _source,
// and one chunk of storage. The generated decoder makes six allocations a
// document here.
//
//nolint:paralleltest // testing.AllocsPerRun panics in a parallel test.
func TestMGetRespDecodeAllocations(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"docs":[`)
	for i := range 100 {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"_index":"i","_id":"doc-%d","_version":1,"_seq_no":%d,"_primary_term":1,"found":true,`+
			`"_source":{"field":"value-%d"}}`, i, i, i)
	}
	b.WriteString(`]}`)
	body := []byte(b.String())

	var resp MGetResp
	allocs := testing.AllocsPerRun(20, func() {
		resp = MGetResp{Docs: make([]MGetRespItem, 0, 100)}
		if err := json.Unmarshal(body, &resp); err != nil {
			panic(err)
		}
	})
	require.Len(t, resp.Docs, 100)
	t.Logf("allocations per document: %.2f", allocs/100)
	if raceEnabled {
		t.Skip("the race detector allocates on its own")
	}
	require.LessOrEqual(t, allocs, mgetRespDecodeMaxAllocs, "allocations decoding 100 documents")
}

// mgetRespDecodeMaxAllocs is the most allocations TestMGetRespDecodeAllocations
// allows: what was measured, plus 2%.
const mgetRespDecodeMaxAllocs = 206.0
