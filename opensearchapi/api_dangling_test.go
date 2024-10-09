// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (core || opensearchapi)

package opensearchapi_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4"
	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
)

func TestDanglingClient(t *testing.T) {
	// Testing dangling indices would require manipulating data inside the opensearch server container
	// instead we create a http servers that returns the expected status code and body
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testURL := func(u *url.URL) (int, string) {
			if u.Path != "/_dangling/indexUUID" {
				return http.StatusNotImplemented, fmt.Sprintf(`{"status": 501, "error": "Expected '_dangling/indexUUID' but got %s"}`, r.URL.Path)
			}
			if u.Query().Get("accept_data_loss") == "" {
				return http.StatusBadRequest, `{"error":{"root_cause":[{"type":"illegal_argument_exception","reason":"accept_data_loss must be set to true"}],"type":"illegal_argument_exception","reason":"accept_data_loss must be set to true"},"status":400}`
			}
			return 0, ""
		}

		if r.Body != nil {
			defer r.Body.Close()
		}
		switch r.Method {
		case http.MethodGet:
			if r.URL.Path != "/_dangling" {
				w.WriteHeader(http.StatusNotImplemented)
				io.Copy(w, strings.NewReader(fmt.Sprintf(`{"status": 501, "error": "Expected '_dangling' but got %s"}`, r.URL.Path)))
				return
			}
			w.Write([]byte(`{"_nodes":{"total":1,"successful":1,"failed":0},"cluster_name":"docker-cluster","dangling_indices":[{"index_name":"test","index_uuid":"WaO0Mu-bSX6E7SdsuYU-yw","creation_date_millis":1694099652069,"node_ids":["xS9VXy4DTXmtO49gPaC3bw"]}]}`))
		case http.MethodPost:
			if code, resp := testURL(r.URL); code != 0 {
				w.WriteHeader(code)
				io.Copy(w, strings.NewReader(resp))
				return
			}
			w.Write([]byte(`{"acknowledged": true}`))
		case http.MethodDelete:
			if code, resp := testURL(r.URL); code != 0 {
				w.WriteHeader(code)
				io.Copy(w, strings.NewReader(resp))
				return
			}
			w.Write([]byte(`{"acknowledged": true}`))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			io.Copy(w, strings.NewReader(fmt.Sprintf(`{"status": 405, "error": "Unexpected Method: %s"}`, r.Method)))
		}
	}))

	client, err := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Addresses: []string{ts.URL}}})
	require.Nil(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.Nil(t, err)

	type danglingTests struct {
		Name    string
		Results func() (any, *opensearch.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []danglingTests
	}{
		{
			Name: "Import",
			Tests: []danglingTests{
				{
					Name: "with request",
					Results: func() (any, *opensearch.Response, error) {
						return client.Dangling.Import(
							nil,
							opensearchapi.DanglingImportReq{
								IndexUUID: "indexUUID",
								Params:    opensearchapi.DanglingImportParams{AcceptDataLoss: opensearchapi.ToPointer(true)},
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (any, *opensearch.Response, error) {
						return failingClient.Dangling.Import(nil, opensearchapi.DanglingImportReq{IndexUUID: "indexUUID"})
					},
				},
			},
		},
		{
			Name: "Get",
			Tests: []danglingTests{
				{
					Name: "with request",
					Results: func() (any, *opensearch.Response, error) {
						return client.Dangling.Get(nil, &opensearchapi.DanglingGetReq{})
					},
				},
				{
					Name: "inspect",
					Results: func() (any, *opensearch.Response, error) {
						return failingClient.Dangling.Get(nil, nil)
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []danglingTests{
				{
					Name: "with request",
					Results: func() (any, *opensearch.Response, error) {
						return client.Dangling.Delete(
							nil,
							opensearchapi.DanglingDeleteReq{
								IndexUUID: "indexUUID",
								Params:    opensearchapi.DanglingDeleteParams{AcceptDataLoss: opensearchapi.ToPointer(true)},
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (any, *opensearch.Response, error) {
						return failingClient.Dangling.Delete(nil, opensearchapi.DanglingDeleteReq{IndexUUID: "indexUUID"})
					},
				},
			},
		},
	}
	for _, value := range testCases {
		t.Run(value.Name, func(t *testing.T) {
			for _, testCase := range value.Tests {
				t.Run(testCase.Name, func(t *testing.T) {
					resp, httpResp, err := testCase.Results()
					if testCase.Name == "inspect" {
						assert.NotNil(t, err)
						assert.Nil(t, resp)
						assert.NotNil(t, httpResp)
						osapitest.VerifyResponse(t, httpResp)
					} else {
						require.Nil(t, err)
						require.NotNil(t, resp)
						assert.NotNil(t, httpResp)
					}
				})
			}
		})
	}

	t.Run("ValidateResponse", func(t *testing.T) {
		t.Run("Get", func(t *testing.T) {
			resp, httpResp, err := client.Dangling.Get(nil, nil)
			require.Nil(t, err)
			assert.NotNil(t, resp)
			assert.NotNil(t, httpResp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, httpResp)
		})
		t.Run("Delete", func(t *testing.T) {
			resp, httpResp, err := client.Dangling.Delete(
				nil,
				opensearchapi.DanglingDeleteReq{
					IndexUUID: "indexUUID",
					Params:    opensearchapi.DanglingDeleteParams{AcceptDataLoss: opensearchapi.ToPointer(true)},
				},
			)
			require.Nil(t, err)
			assert.NotNil(t, resp)
			assert.NotNil(t, httpResp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, httpResp)
		})
		t.Run("Import", func(t *testing.T) {
			resp, httpResp, err := client.Dangling.Import(
				nil,
				opensearchapi.DanglingImportReq{
					IndexUUID: "indexUUID",
					Params:    opensearchapi.DanglingImportParams{AcceptDataLoss: opensearchapi.ToPointer(true)},
				},
			)
			require.Nil(t, err)
			assert.NotNil(t, resp)
			assert.NotNil(t, httpResp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, httpResp)
		})
	})
}
