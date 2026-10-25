// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi_test

import (
	"fmt"
	"testing"

	opensearch "github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// searchBodyWithQueries returns a typed search body whose query is a bool of n
// term and match clauses, each a union nested inside the bool's must union, so
// that encoding it walks unions three deep.
func searchBodyWithQueries(n int) *opensearchapi.SearchBody {
	must := make([]opensearchapi.CommonQueryDSLQueryContainer, n)
	for i := range must {
		if i%2 == 0 {
			must[i] = opensearchapi.CommonQueryDSLQueryContainer{Term: map[string]opensearchapi.CommonQueryDSLTermQuery{
				fmt.Sprintf("field-%d", i): opensearchapi.NewCommonQueryDSLTermQueryFromFieldValue(
					opensearchapi.NewFieldValueFromString(fmt.Sprintf("value-%d", i))),
			}}
		} else {
			must[i] = opensearchapi.CommonQueryDSLQueryContainer{Match: map[string]opensearchapi.CommonQueryDSLMatchQuery{
				"message": opensearchapi.NewCommonQueryDSLMatchQueryFromFieldValue(
					opensearchapi.NewFieldValueFromString("the quick brown fox jumps over the lazy dog")),
			}}
		}
	}
	bm := opensearchapi.NewCommonQueryDSLBoolQueryMustFromArray(must)
	size := 10

	return &opensearchapi.SearchBody{
		Query: &opensearchapi.CommonQueryDSLQueryContainer{Bool: &opensearchapi.CommonQueryDSLBoolQuery{Must: &bm}},
		Size:  &size,
	}
}

// BenchmarkSearchEncode measures one search call whose typed body holds n query
// clauses, against a canned response of no hits, so that encoding the request
// body dominates.
func BenchmarkSearchEncode(b *testing.B) {
	for _, n := range []int{1, 20, 200} {
		b.Run(fmt.Sprintf("clauses=%d", n), func(b *testing.B) {
			client, err := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{
				Addresses: []string{"http://canned.invalid:9200"},
				Transport: &cannedTransport{body: searchResponseBody(0)},
			}})
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = client.Close() })

			req := &opensearchapi.SearchReq{Indices: []string{"logs"}, Body: searchBodyWithQueries(n)}
			if _, err := client.Search(b.Context(), req); err != nil {
				b.Fatal(err)
			}

			b.ReportAllocs()
			for b.Loop() {
				if _, err := client.Search(b.Context(), req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
