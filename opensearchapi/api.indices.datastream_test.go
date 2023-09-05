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
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kinbiko/jsonassert"
	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
	"github.com/stretchr/testify/require"
)

type DataStreamRequest interface {
	Do(context.Context, opensearchapi.Transport) (*opensearchapi.Response, error)
}

func TestIndicesDataStreams_Do(t *testing.T) {
	// We need two datastreams to ensure endpoints that fetch both are tested appropriately
	dataStream1 := fmt.Sprintf("demo-1-%s", time.Now().Format("2006-01-02-15-04-05"))
	dataStream2 := fmt.Sprintf("demo-2-%s", time.Now().Format("2006-01-02-15-04-05"))

	tests := []struct {
		name     string
		r        DataStreamRequest
		want     *opensearchapi.Response
		wantBody string
		wantErr  bool
	}{
		{
			name: "TestIndicesCreateDataStreamRequest_Do",
			r: opensearchapi.IndicesCreateDataStreamRequest{
				Name:       dataStream1,
				Pretty:     true,
				Human:      true,
				ErrorTrace: true,
				Header: map[string][]string{
					"Content-Type": {"application/json"},
				},
			},
			want: &opensearchapi.Response{
				StatusCode: 200,
				Header: http.Header{
					"Content-Type": []string{"application/json; charset=UTF-8"},
				},
			},
			wantBody: `{"acknowledged":true}`,
			wantErr:  false,
		},
		{
			name: "TestIndicesCreateDataStream2Request_Do",
			r: opensearchapi.IndicesCreateDataStreamRequest{
				Name:       dataStream2,
				Pretty:     true,
				Human:      true,
				ErrorTrace: true,
				Header: map[string][]string{
					"Content-Type": {"application/json"},
				},
			},
			want: &opensearchapi.Response{
				StatusCode: 200,
				Header: http.Header{
					"Content-Type": []string{"application/json; charset=UTF-8"},
				},
			},
			wantBody: `{"acknowledged":true}`,
			wantErr:  false,
		},
		{
			name: "TestIndicesGetDataStreamRequest_Do",
			r: opensearchapi.IndicesGetDataStreamRequest{
				Name:       dataStream1,
				Pretty:     true,
				Human:      true,
				ErrorTrace: true,
				Header: map[string][]string{
					"Content-Type": {"application/json"},
				},
			},
			want: &opensearchapi.Response{
				StatusCode: 200,
				Header: http.Header{
					"Content-Type": []string{"application/json; charset=UTF-8"},
				},
			},
			wantErr: false,
		},
		{
			name: "TestIndicesGetAllDataStreamsRequest_Do",
			r: opensearchapi.IndicesGetDataStreamRequest{
				Pretty:     true,
				Human:      true,
				ErrorTrace: true,
				Header: map[string][]string{
					"Content-Type": {"application/json"},
				},
			},
			want: &opensearchapi.Response{
				StatusCode: 200,
				Header: http.Header{
					"Content-Type": []string{"application/json; charset=UTF-8"},
				},
			},
			wantErr: false,
		},
		{
			name: "TestIndicesGetStatsDataStreamRequest_Do",
			r: opensearchapi.IndicesGetDataStreamStatsRequest{
				Name:       dataStream1,
				Pretty:     true,
				Human:      true,
				ErrorTrace: true,
				Header: map[string][]string{
					"Content-Type": {"application/json"},
				},
			},
			want: &opensearchapi.Response{
				StatusCode: 200,
				Header: http.Header{
					"Content-Type": []string{"application/json; charset=UTF-8"},
				},
			},
			wantBody: fmt.Sprintf(`{"_shards":{"total":2,"successful":1,"failed":0},"data_stream_count":1,"backing_indices":1,"total_store_size":"208b","total_store_size_bytes":208,"data_streams":[{"data_stream":"%s","backing_indices":1,"store_size":"208b","store_size_bytes":208,"maximum_timestamp":0}]}`, dataStream1),
			wantErr:  false,
		},
		{
			name: "TestIndicesGetAllStatsDataStreamRequest_Do",
			r: opensearchapi.IndicesGetDataStreamStatsRequest{
				Pretty:     true,
				Human:      true,
				ErrorTrace: true,
				Header: map[string][]string{
					"Content-Type": {"application/json"},
				},
			},
			want: &opensearchapi.Response{
				StatusCode: 200,
				Header: http.Header{
					"Content-Type": []string{"application/json; charset=UTF-8"},
				},
			},
			wantBody: fmt.Sprintf(`{"_shards":{"total":4,"successful":2,"failed":0},"data_stream_count":2,"backing_indices":2,"total_store_size":"416b","total_store_size_bytes":416,"data_streams":["<<UNORDERED>>",{"data_stream":"%s","backing_indices":1,"store_size":"208b","store_size_bytes":208,"maximum_timestamp":0},{"data_stream":"%s","backing_indices":1,"store_size":"208b","store_size_bytes":208,"maximum_timestamp":0}]}`, dataStream2, dataStream1),
			wantErr:  false,
		},
		{
			name: "TestIndicesDeleteDataStreamRequest_Do",
			r: opensearchapi.IndicesDeleteDataStreamRequest{
				Name:       dataStream1,
				Pretty:     true,
				Human:      true,
				ErrorTrace: true,
				Header: map[string][]string{
					"Content-Type": {"application/json"},
				},
			},
			want: &opensearchapi.Response{
				StatusCode: 200,
				Header: http.Header{
					"Content-Type": []string{"application/json; charset=UTF-8"},
				},
			},
			wantBody: `{"acknowledged":true}`,
			wantErr:  false,
		},
		{
			name: "TestIndicesDeleteDataStream2Request_Do",
			r: opensearchapi.IndicesDeleteDataStreamRequest{
				Name:       dataStream2,
				Pretty:     true,
				Human:      true,
				ErrorTrace: true,
				Header: map[string][]string{
					"Content-Type": {"application/json"},
				},
			},
			want: &opensearchapi.Response{
				StatusCode: 200,
				Header: http.Header{
					"Content-Type": []string{"application/json; charset=UTF-8"},
				},
			},
			wantBody: `{"acknowledged":true}`,
			wantErr:  false,
		},
	}

	client, err := opensearch.NewDefaultClient()
	require.NoError(t, err)

	iPut := opensearchapi.IndicesPutIndexTemplateRequest{
		Name:       fmt.Sprintf("demo-data-template"),
		Pretty:     true,
		Human:      true,
		ErrorTrace: true,
		Body:       strings.NewReader(fmt.Sprintf(`{"index_patterns": ["demo-*"], "data_stream": {}, "priority": 100} }`)),
	}

	iPutResponse, err := iPut.Do(context.Background(), client)
	require.NoError(t, err)
	require.Equalf(t, false, iPutResponse.IsError(),
		"Error when creating index template: %s", iPutResponse.String())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.r.Do(context.Background(), client)
			if (err != nil) != tt.wantErr {
				t.Errorf("Do() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			require.Equalf(t, got.IsError(), tt.wantErr, "Do() got = %v, want %v", got.IsError(), tt.wantErr)
			require.Equalf(t, got.StatusCode, tt.want.StatusCode, "Do() got = %v, want %v", got.StatusCode, tt.want.StatusCode)

			if tt.wantBody != "" {
				for name, value := range tt.want.Header {
					require.Contains(t, got.Header, name)
					require.Equal(t, value, got.Header[name])
				}

				defer got.Body.Close()
				body, err := ioutil.ReadAll(got.Body)
				require.NoError(t, err)

				ja := jsonassert.New(t)
				ja.Assertf(string(body), tt.wantBody)
			}
		})
	}
}
