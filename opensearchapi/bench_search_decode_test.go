// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	opensearch "github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// cannedTransport answers every request with the same JSON body.
type cannedTransport struct {
	body []byte
}

func (rt *cannedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
	}

	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{"Content-Type": {"application/json"}},
		Body:          io.NopCloser(bytes.NewReader(rt.body)),
		ContentLength: int64(len(rt.body)),
		Request:       req,
	}, nil
}

// searchResponseBody returns a search response with hits hits, each with a
// small _source, and a terms aggregation of 50 buckets.
func searchResponseBody(hits int) []byte {
	var b strings.Builder
	b.WriteString(`{"took":12,"timed_out":false,"_shards":{"total":5,"successful":5,"skipped":0,"failed":0},`)
	fmt.Fprintf(&b, `"hits":{"total":{"value":%d,"relation":"eq"},"max_score":1.5,"hits":[`, hits)
	for i := range hits {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"_index":"logs-2026.10.23","_id":"doc-%d","_score":1.5,`+
			`"_source":{"@timestamp":"2026-10-23T10:00:%02dZ","host":"host-%d","level":"info",`+
			`"message":"request served in %d ms","tags":["a","b","c"],"bytes":%d}}`, i, i%60, i%32, i%997, i*13)
	}
	b.WriteString(`]},"aggregations":{"by_host":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[`)
	for i := range 50 {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"key":"host-%d","doc_count":%d}`, i, 100-i)
	}
	b.WriteString(`]}}}`)

	return []byte(b.String())
}

// BenchmarkSearchDecode measures one search call against a canned response:
// the client's request path, the transport and decoding the typed response.
// The decode dominates as hits grow.
func BenchmarkSearchDecode(b *testing.B) {
	for _, hits := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("hits=%d", hits), func(b *testing.B) {
			body := searchResponseBody(hits)
			client, err := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{
				Addresses: []string{"http://canned.invalid:9200"},
				Transport: &cannedTransport{body: body},
			}})
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = client.Close() })

			req := &opensearchapi.SearchReq{Indices: []string{"logs"}}
			resp, err := client.Search(b.Context(), req)
			if err != nil {
				b.Fatal(err)
			}
			if got := len(resp.Hits.Hits); got != hits {
				b.Fatalf("decoded %d hits, want %d", got, hits)
			}

			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := client.Search(b.Context(), req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
