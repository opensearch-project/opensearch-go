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

// bulkRespCorpus are bulk responses BulkResp.UnmarshalJSONFrom must decode as the
// reflective decoder does.
var bulkRespCorpus = map[string]string{
	"every action and field": `{"took":30,"ingest_took":2,"errors":true,"items":[` +
		`{"index":{"_index":"i","_id":"1","_version":1,"result":"created","forced_refresh":true,` +
		`"_shards":{"total":2,"successful":1,"failed":0},"_seq_no":0,"_primary_term":1,"status":201}},` +
		`{"create":{"_index":"i","_id":"2","_type":"_doc","status":409,"error":{"type":"version_conflict_engine_exception",` +
		`"reason":"r","index":"i","shard":"0"}}},` +
		`{"update":{"_index":"i","_id":"3","result":"noop","status":200,"get":{"found":true,"_source":{"a":1}}}},` +
		`{"delete":{"_index":"i","_id":"4","result":"not_found","status":404,` +
		`"_shards":{"total":2,"successful":1,"failed":1,"failures":[{"shard":0,"index":"i","status":"x",` +
		`"reason":{"type":"t","reason":"r"}}]}}}]}`,
	"no items":           `{"took":1,"errors":false,"items":[]}`,
	"null items":         `{"took":1,"errors":false,"items":null}`,
	"no items member":    `{"took":1,"errors":false}`,
	"null response":      `null`,
	"keys in other case": `{"Took":5,"ERRORS":true,"Items":[{"INDEX":{"_ID":"a","Status":201,"Result":"created","_Version":3}}]}`,
	"unknown members":    `{"took":1,"extra":{"x":[1,2]},"items":[{"index":{"_id":"a","status":201,"novel":[{}]},"other":5}]}`,
	"null fields": `{"took":1,"items":[{"index":{"_id":null,"_index":null,"_version":null,"result":null,"_shards":null,` +
		`"error":null,"get":null,"forced_refresh":null,"_seq_no":null,"_primary_term":null,"_type":null,"status":null}}]}`,
	"a null action":              `{"items":[{"index":null},{"create":{"_id":"b","status":201}}]}`,
	"a null item":                `{"items":[null,{"index":{"_id":"c","status":201}}]}`,
	"an item with two actions":   `{"items":[{"index":{"_id":"d","status":201},"create":{"_id":"e","status":201}}]}`,
	"a repeated field":           `{"items":[{"index":{"_id":"f","_id":"g","status":200,"status":201}}],"took":1,"took":2}`,
	"escaped names":              `{"t\u006fok":7,"\u0069tems":[{"ind\u0065x":{"_\u0069d":"h","st\u0061tus":201}}]}`,
	"escaped and unicode values": `{"items":[{"index":{"_id":"é\"q\"","result":"créated","status":201}}]}`,
	"invalid UTF-8 in a value":   "{\"items\":[{\"index\":{\"_id\":\"a\xffb\",\"status\":201}}]}",
	"whitespace":                 " {\n \"took\" : 1 ,\n \"items\" : [ { \"index\" : { \"_id\" : \"w\" , \"status\" : 201 } } ]\n}\n",
}

func decodeBulkGeneric(t *testing.T, body string) (BulkResp, error) {
	t.Helper()

	var g bulkRespGenericDecode
	err := json.Unmarshal([]byte(body), &g)

	return BulkResp(g), err
}

// TestBulkRespDecodesAsTheReflectiveDecoder decodes each response of the corpus
// with BulkResp.UnmarshalJSONFrom, from bytes and from a reader that hands over a
// byte at a time, and checks that it gets what the reflective decoder gets.
func TestBulkRespDecodesAsTheReflectiveDecoder(t *testing.T) {
	t.Parallel()

	for name, body := range bulkRespCorpus {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want, wantErr := decodeBulkGeneric(t, body)
			require.NoError(t, wantErr)

			var got BulkResp
			require.NoError(t, json.Unmarshal([]byte(body), &got))
			require.Equal(t, want, got)

			var streamed BulkResp
			dec := json.NewTextDecoder(iotest.OneByteReader(strings.NewReader(body)))
			require.NoError(t, json.UnmarshalDecode(dec, &streamed))
			require.Equal(t, want, streamed, "decoded from a reader")
		})
	}
}

// TestBulkRespDecodeFailures checks that a response the reflective decoder
// refuses is refused here too.
func TestBulkRespDecodeFailures(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"items not an array":      `{"items":{}}`,
		"an item not an object":   `{"items":[5]}`,
		"an action not an object": `{"items":[{"index":5}]}`,
		"a status not a number":   `{"items":[{"index":{"status":"201"}}]}`,
		"a version not a number":  `{"items":[{"index":{"_version":"x"}}]}`,
		"an id not a string":      `{"items":[{"index":{"_id":5}}]}`,
		"took not a number":       `{"took":"1"}`,
		"truncated":               `{"items":[{"index":{"_id":"a"`,
		"not an object":           `[1]`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, wantErr := decodeBulkGeneric(t, body)
			require.Error(t, wantErr, "the reflective decoder refuses it")

			var got BulkResp
			require.Error(t, json.Unmarshal([]byte(body), &got))
		})
	}
}

// TestBulkRespDecodeAllocations bounds what decoding a response of 100 created
// items allocates: the items slice, a few chunks of storage, and each item's
// _id. The reflective decoder makes eight allocations an item here.
//
//nolint:paralleltest // testing.AllocsPerRun panics in a parallel test.
func TestBulkRespDecodeAllocations(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"took":3,"errors":false,"items":[`)
	for i := range 100 {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"index":{"_index":"i","_id":"doc-%d","_version":1,"result":"created",`+
			`"_shards":{"total":2,"successful":2,"failed":0},"_seq_no":%d,"_primary_term":1,"status":201}}`, i, i)
	}
	b.WriteString(`]}`)
	body := []byte(b.String())

	var resp BulkResp
	allocs := testing.AllocsPerRun(20, func() {
		resp = BulkResp{}
		if err := json.Unmarshal(body, &resp); err != nil {
			panic(err)
		}
	})
	require.Len(t, resp.Items, 100)
	t.Logf("allocations per item: %.2f", allocs/100)
	if raceEnabled {
		t.Skip("the race detector allocates on its own")
	}
	require.LessOrEqual(t, allocs, 125.0, "about one allocation an item, for its _id")
}

// TestBulkRespDecoderKnowsEveryField fails when the generated BulkResp, BulkItem or
// BulkRespItem gains or renames a field, which BulkResp.UnmarshalJSONFrom would
// otherwise skip as an unknown member.
func TestBulkRespDecoderKnowsEveryField(t *testing.T) {
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
	const update = "update BulkResp.UnmarshalJSONFrom to decode the new field, then this list"
	require.Equal(t, []string{"errors", "ingest_took,omitempty", "items", "took"}, tags(BulkResp{}), update)
	require.Equal(t, []string{"create,omitempty", "delete,omitempty", "index,omitempty", "update,omitempty"},
		tags(BulkItem{}), update)
	require.Equal(t, []string{
		"_id,omitempty", "_index", "_primary_term,omitempty", "_seq_no,omitempty", "_shards,omitempty",
		"_type,omitempty", "_version,omitempty", "error,omitempty", "forced_refresh,omitempty", "get,omitempty",
		"result,omitempty", "status",
	}, tags(BulkRespItem{}), update)
}
