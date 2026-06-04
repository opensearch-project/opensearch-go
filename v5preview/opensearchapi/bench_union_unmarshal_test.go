// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi"
)

// A single successful mget doc. This is the common case and also the worst
// case for the try-each union decoder: the "_id","_index","error" probe must
// scan the whole object only to discover "error" is absent, then the real
// GetResult decode runs as a second full parse.
const benchMGetDocSuccess = `{"_index":"my-index","_id":"42","_version":7,"_seq_no":13,"_primary_term":2,"found":true,` +
	`"_source":{"title":"the quick brown fox","count":1234,"tags":["a","b","c"],"nested":{"x":1,"y":2}}}`

// A single error doc (hits the discriminator branch in pass 1).
const benchMGetDocError = `{"_index":"my-index","_id":"99","error":` +
	`{"type":"index_not_found_exception","reason":"no such index","index":"my-index"}}`

// plainGetResult mirrors the decoded shape of a successful mget doc without the
// union machinery. Decoding into a slice of these is the "no try-each" baseline.
type plainGetResult struct {
	Index       string          `json:"_index"`
	ID          string          `json:"_id"`
	Version     *int64          `json:"_version,omitempty"`
	SeqNo       *int64          `json:"_seq_no,omitempty"`
	PrimaryTerm *int64          `json:"_primary_term,omitempty"`
	Found       bool            `json:"found"`
	Source      json.RawMessage `json:"_source"`
}

type plainMGetResp struct {
	Docs []plainGetResult `json:"docs"`
}

func buildMGetBody(nDocs int, errEvery int) []byte {
	var b strings.Builder
	b.WriteString(`{"docs":[`)
	for i := range nDocs {
		if i > 0 {
			b.WriteByte(',')
		}
		if errEvery > 0 && i%errEvery == 0 {
			b.WriteString(benchMGetDocError)
		} else {
			b.WriteString(benchMGetDocSuccess)
		}
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

// --- Proposed alternatives modeled as plain structs ---

// mergedMGetDoc models a single-pass decode: one struct carrying the union of
// both branch field sets plus the error-branch discriminator. Decode once,
// then pick the branch by inspecting Error. No probe, no second parse.
type mergedMGetDoc struct {
	Index       string          `json:"_index"`
	ID          string          `json:"_id"`
	Version     *int64          `json:"_version,omitempty"`
	SeqNo       *int64          `json:"_seq_no,omitempty"`
	PrimaryTerm *int64          `json:"_primary_term,omitempty"`
	Found       bool            `json:"found"`
	Source      json.RawMessage `json:"_source"`
	Error       json.RawMessage `json:"error"` // discriminator: error branch
}

type mergedMGetResp struct {
	Docs []mergedMGetDoc `json:"docs"`
}

// mgetProbe models a discriminator-only probe: a struct with just the keys we
// test for, so encoding/json skips every other key without allocating a
// RawMessage per field (the map probe in build.HasJSONKeys does the opposite).
type mgetProbe struct {
	ID    json.RawMessage `json:"_id"`
	Index json.RawMessage `json:"_index"`
	Error json.RawMessage `json:"error"`
}

func hasErrorKeysStructProbe(data []byte) bool {
	var p mgetProbe
	if err := json.Unmarshal(data, &p); err != nil {
		return false
	}
	return p.ID != nil && p.Index != nil && p.Error != nil
}

// Single-pass merged decode of the full response.
func BenchmarkProposedMerged_MGetResp(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		data := buildMGetBody(n, 0)
		b.Run(fmt.Sprintf("docs=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			for b.Loop() {
				var resp mergedMGetResp
				if err := json.Unmarshal(data, &resp); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// "Decode then check" with a cheap struct probe instead of the map probe.
// Mirrors the existing pass-1 structure but swaps HasJSONKeys for a struct.
func BenchmarkProposedStructProbe_MGetDocsItem_Success(b *testing.B) {
	data := []byte(benchMGetDocSuccess)
	b.ReportAllocs()
	for b.Loop() {
		var v plainGetResult
		if hasErrorKeysStructProbe(data) {
			_ = v // would decode error branch
		} else if err := json.Unmarshal(data, &v); err != nil {
			b.Fatal(err)
		}
	}
}

// Single-item union decode, success branch (the costly probe-then-decode path).
func BenchmarkUnion_MGetDocsItem_Success(b *testing.B) {
	data := []byte(benchMGetDocSuccess)
	b.ReportAllocs()
	for b.Loop() {
		var item opensearchapi.MGetRespBodyDocsItem
		if err := json.Unmarshal(data, &item); err != nil {
			b.Fatal(err)
		}
	}
}

// Single-item union decode, error branch (pass-1 discriminator hit).
func BenchmarkUnion_MGetDocsItem_Error(b *testing.B) {
	data := []byte(benchMGetDocError)
	b.ReportAllocs()
	for b.Loop() {
		var item opensearchapi.MGetRespBodyDocsItem
		if err := json.Unmarshal(data, &item); err != nil {
			b.Fatal(err)
		}
	}
}

// Single-item plain decode (no union) for direct comparison against Success.
func BenchmarkPlain_MGetDocsItem_Success(b *testing.B) {
	data := []byte(benchMGetDocSuccess)
	b.ReportAllocs()
	for b.Loop() {
		var item plainGetResult
		if err := json.Unmarshal(data, &item); err != nil {
			b.Fatal(err)
		}
	}
}

// Full response decode at realistic doc counts: union vs plain struct.
func BenchmarkUnion_MGetResp(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		data := buildMGetBody(n, 0)
		b.Run(fmt.Sprintf("docs=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			for b.Loop() {
				var resp opensearchapi.MGetResp
				if err := json.Unmarshal(data, &resp); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkPlain_MGetResp(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		data := buildMGetBody(n, 0)
		b.Run(fmt.Sprintf("docs=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			for b.Loop() {
				var resp plainMGetResp
				if err := json.Unmarshal(data, &resp); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Mixed success/error docs (1 in 10 is an error) at 1000 docs, the
// production-realistic shape for a partial-failure mget.
func BenchmarkUnion_MGetResp_Mixed(b *testing.B) {
	data := buildMGetBody(1000, 10)
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		var resp opensearchapi.MGetResp
		if err := json.Unmarshal(data, &resp); err != nil {
			b.Fatal(err)
		}
	}
}
