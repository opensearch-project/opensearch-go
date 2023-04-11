// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

//go:build integration
// +build integration

package opensearchapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
	"github.com/stretchr/testify/require"
	"io/ioutil"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestDocumentRequest_Do(t *testing.T) {
	index := fmt.Sprintf("index-%s", time.Now().Format("2006-01-02-15-04-05"))

	tests := []struct {
		name     string
		r        DataStreamRequest
		want     *opensearchapi.Response
		wantBody string
		wantErr  bool
	}{
		// Create document
		{
			name: "TestCreateRequest_Do",
			r: opensearchapi.CreateRequest{
				Index:      index,
				DocumentID: "1",
				Body:       strings.NewReader(`{ "title": "Moneyball", "director": "Bennett Miller", "year": "2011" }`),
			},
			want: &opensearchapi.Response{
				StatusCode: 201,
				Header: http.Header{
					"Content-Type": []string{"application/json; charset=UTF-8"},
					"Location":     []string{fmt.Sprintf("/%s/_doc/1", index)},
				},
			},
			wantBody: fmt.Sprintf(`{"_index":"%s","_id":"1","_version":1,"result":"created","_shards":{"total":2,"successful":1,"failed":0},"_seq_no":0,"_primary_term":1}`, index),
			wantErr:  false,
		},
		{
			name: "TestCreateRequest_Do",
			r: opensearchapi.CreateRequest{
				Index:      index,
				DocumentID: "2",
				Body:       strings.NewReader(`{ "title": "Tenet", "director": "Christopher Nolan", "year": "2019" }`),
			},
			want: &opensearchapi.Response{
				StatusCode: 201,
				Header: http.Header{
					"Content-Type": []string{"application/json; charset=UTF-8"},
					"Location":     []string{fmt.Sprintf("/%s/_doc/2", index)},
				},
			},
			wantBody: fmt.Sprintf(`{"_index":"%s","_id":"2","_version":1,"result":"created","_shards":{"total":2,"successful":1,"failed":0},"_seq_no":1,"_primary_term":1}`, index),
			wantErr:  false,
		},

		// Get document
		{
			name: "TestGetRequest_Do",
			r: opensearchapi.GetRequest{
				Index:      index,
				DocumentID: "2",
				Source:     true,
			},
			want: &opensearchapi.Response{
				StatusCode: 200,
				Header: http.Header{
					"Content-Type": []string{"application/json; charset=UTF-8"},
				},
			},
			wantBody: fmt.Sprintf(`{"_id":"2", "_index":"%s", "_primary_term":1, "_seq_no":1, "_source": {"director":"Christopher Nolan", "title":"Tenet", "year":"2019"}, "_version":1, "found":true}`, index),
			wantErr:  false,
		},
		// Get multiple documents
		{
			name: "TestMultiGetRequest_Do. Source parameter is a bool and slice of strings",
			r: opensearchapi.MgetRequest{
				Index: index,
				Body:  strings.NewReader(`{ "docs": [ { "_id": "1", "_source": true }, { "_id": "2", "_source": [ "title" ] } ] }`),
				// seems to does not work
				Source: false,
			},
			want: &opensearchapi.Response{
				StatusCode: 200,
				Header: http.Header{
					"Content-Type": []string{"application/json; charset=UTF-8"},
				},
			},
			wantBody: fmt.Sprintf(`{ "docs": [ { "_id": "1", "_index": "%s", "_primary_term": 1, "_seq_no": 0, "_source": { "director": "Bennett Miller", "title": "Moneyball", "year": "2011" }, "_version": 1, "found": true }, { "_id": "2", "_index": "%s", "_primary_term": 1, "_seq_no": 1, "_source": {"title":"Tenet"}, "_version": 1, "found": true } ] }`, index, index),
			wantErr:  false,
		},
		// Get source document
		{
			name: "TestGetSourceRequest_Do. Source parameter is a bool and slice of strings",
			r: opensearchapi.GetSourceRequest{
				Index:      index,
				DocumentID: "2",
			},
			want: &opensearchapi.Response{
				StatusCode: 200,
				Header: http.Header{
					"Content-Type": []string{"application/json; charset=UTF-8"},
				},
			},
			wantBody: `{"director":"Christopher Nolan", "title":"Tenet", "year":"2019"}`,
			wantErr:  false,
		},

		// Exists document
		{
			name: "TestExistsRequest_Do",
			r: opensearchapi.ExistsRequest{
				Index:      index,
				DocumentID: "2",
				Source:     true,
			},
			want: &opensearchapi.Response{
				StatusCode: 200,
				Header: http.Header{
					"Content-Type":   []string{"application/json; charset=UTF-8"},
					"Content-Length": []string{"189"},
				},
			},
			wantBody: ``,
			wantErr:  false,
		},

		// Search document
		{
			name: "TestSearchRequest_Do. Source parameter is a slice of strings",
			r: opensearchapi.SearchRequest{
				Index:  []string{index},
				Body:   strings.NewReader(`{ "query": { "match": { "title": "Tenet" } } }`),
				Source: true,
			},
			want: &opensearchapi.Response{
				StatusCode: 200,
				Header: http.Header{
					"Content-Type": []string{"application/json; charset=UTF-8"},
				},
			},
			wantBody: fmt.Sprintf(`{"_shards": {"failed":0, "skipped":0, "successful":4, "total":4}, "hits":{"hits":[], "max_score": null, "total": {"relation":"eq", "value":0}}, "timed_out":false, "took":0}`),
			wantErr:  false,
		},

		// Update document
		{
			name: "TestUpdateRequest_Do",
			r: opensearchapi.UpdateRequest{
				Index:      index,
				DocumentID: "1",
				Body:       strings.NewReader(`{ "doc": { "title": "Moneyball", "director": "Bennett", "year": "2012" } }`),
				Source:     nil,
			},
			want: &opensearchapi.Response{
				StatusCode: 200,
				Header: http.Header{
					"Content-Type": []string{"application/json; charset=UTF-8"},
				},
			},
			wantBody: fmt.Sprintf(`{"_index":"%s","_id":"1","_version":2,"result":"updated","_shards":{"total":2,"successful":1,"failed":0},"_seq_no":2,"_primary_term":1}`, index),
			wantErr:  false,
		},
		{
			name: "TestUpdateRequest_Do. Source parameter is bool",
			r: opensearchapi.UpdateRequest{
				Index:      index,
				DocumentID: "1",
				Body:       strings.NewReader(`{ "doc": { "title": "Moneyball", "director": "Bennett", "year": "2012" } }`),
				Source:     true,
			},
			want: &opensearchapi.Response{
				StatusCode: 200,
				Header: http.Header{
					"Content-Type": []string{"application/json; charset=UTF-8"},
				},
			},
			wantBody: fmt.Sprintf(`{"_index":"%s","_id":"1","_version":2,"result":"noop","_shards":{"total":0,"successful":0,"failed":0},"_seq_no":2,"_primary_term":1,"get":{"_seq_no":2,"_primary_term":1,"found":true,"_source":{"title":"Moneyball","director":"Bennett","year":"2012"}}}`, index),
			wantErr:  false,
		},
		{
			name: "TestUpdateRequest_Do. Source parameter is a slice of strings",
			r: opensearchapi.UpdateRequest{
				Index:      index,
				DocumentID: "1",
				Body:       strings.NewReader(`{ "doc": { "title": "Moneyball", "director": "Bennett", "year": "2012" } }`),
				Source:     []string{"true"},
			},
			want: &opensearchapi.Response{
				StatusCode: 200,
				Header: http.Header{
					"Content-Type": []string{"application/json; charset=UTF-8"},
				},
			},
			wantBody: fmt.Sprintf(`{"_index":"%s","_id":"1","_version":2,"result":"noop","_shards":{"total":0,"successful":0,"failed":0},"_seq_no":2,"_primary_term":1,"get":{"_seq_no":2,"_primary_term":1,"found":true,"_source":{"title":"Moneyball","director":"Bennett","year":"2012"}}}`, index),
			wantErr:  false,
		},
		{
			name: "TestUpdateRequest_Do. Source Excludes",
			r: opensearchapi.UpdateRequest{
				Index:          index,
				DocumentID:     "1",
				Body:           strings.NewReader(`{ "doc": { "title": "Moneyball", "director": "Bennett", "year": "2012" } }`),
				Source:         []string{"true"},
				SourceExcludes: []string{"director"},
			},
			want: &opensearchapi.Response{
				StatusCode: 200,
				Header: http.Header{
					"Content-Type": []string{"application/json; charset=UTF-8"},
				},
			},
			wantBody: fmt.Sprintf(`{"_index":"%s","_id":"1","_version":2,"result":"noop","_shards":{"total":0,"successful":0,"failed":0},"_seq_no":2,"_primary_term":1,"get":{"_seq_no":2,"_primary_term":1,"found":true,"_source":{"title":"Moneyball","year":"2012"}}}`, index),
			wantErr:  false,
		},

		// Bulk document
		{
			name: "TestBulkRequest_Do.",
			r: opensearchapi.BulkRequest{
				Index: index,
				Body: strings.NewReader(`{ "index": { "_index": "movies", "_id": "tt1979320" } }
{ "title": "Rush", "year": 2013 }
{ "create": { "_index": "movies", "_id": "tt1392214" } }
{ "title": "Prisoners", "year": 2013 }
{ "update": { "_index": "movies", "_id": "tt0816711" } }
{ "doc" : { "title": "World War Z" } }
`),
				Source: []string{"true"},
			},
			want: &opensearchapi.Response{
				StatusCode: 200,
				Header: http.Header{
					"Content-Type": []string{"application/json; charset=UTF-8"},
				},
			},
			wantErr: false,
		},
	}

	client, err := opensearch.NewDefaultClient()
	require.NoError(t, err)

	iCreate := opensearchapi.IndicesCreateRequest{
		Index:      index,
		Pretty:     true,
		Human:      true,
		ErrorTrace: true,
		Body:       strings.NewReader(fmt.Sprintf(`{"settings": {"index": {"number_of_shards": 4}}}`)),
	}

	_, err = iCreate.Do(context.Background(), client)
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.r.Do(context.Background(), client)
			if (err != nil) != tt.wantErr {
				t.Errorf("Do() error = %+v, wantErr %v", err, tt.wantErr)
				return
			}

			require.Equalf(t, got.StatusCode, tt.want.StatusCode, "Do() got = %v, want %v", got.StatusCode, tt.want.StatusCode)

			if tt.wantBody != "" {
				require.Equalf(t, got.Header, tt.want.Header, "Do() got = %v, want %v", got.Header, tt.want.Header)

				defer got.Body.Close()
				body, err := ioutil.ReadAll(got.Body)
				require.NoError(t, err)

				buffer := new(bytes.Buffer)
				err = json.Compact(buffer, body)
				require.NoError(t, err)

				// ignore took field, since it is dynamic
				took := regexp.MustCompile(`"took":\d+`)
				actual := took.ReplaceAllString(buffer.String(), `"took":0`)
				// ignore _type field, since it is legacy
				actual = strings.ReplaceAll(actual, `"_type":"_doc",`, "")

				require.JSONEqf(t, tt.wantBody, actual, "Do() got = %v, want %v", got.String(), tt.wantBody)
			}
		})
	}
}
