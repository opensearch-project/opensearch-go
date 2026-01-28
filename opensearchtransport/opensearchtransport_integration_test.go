// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

//go:build integration && (core || opensearchtransport)

package opensearchtransport_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
	ostestutil "github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil/mockhttp"
)

var _ = fmt.Print

func TestTransportRetries(t *testing.T) {
	var counter int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter++

		body, _ := io.ReadAll(r.Body)
		if ostestutil.IsDebugEnabled(t) {
			t.Logf("req.Body: %q", string(body))
		}

		http.Error(w, "FAKE 502", http.StatusBadGateway)
	}))
	serverURL, _ := url.Parse(server.URL)

	transport, _ := opensearchtransport.New(opensearchtransport.Config{URLs: []*url.URL{serverURL}})

	bodies := []io.Reader{
		strings.NewReader(`FAKE`),
		opensearchutil.NewJSONReader(`FAKE`),
	}

	for _, body := range bodies {
		t.Run(fmt.Sprintf("Reset the %T request body", body), func(t *testing.T) {
			counter = 0

			req, err := http.NewRequest(http.MethodGet, "/", body)
			if err != nil {
				t.Fatalf("Unexpected error: %s", err)
			}

			res, err := transport.Perform(req)
			if err != nil {
				t.Fatalf("Unexpected error: %s", err)
			}
			defer res.Body.Close()

			body, _ := io.ReadAll(res.Body)
			res.Body.Close()

			if ostestutil.IsDebugEnabled(t) {
				t.Logf("> GET %q", req.URL)
				t.Logf("< %q (tries: %d)", bytes.TrimSpace(body), counter)
			}

			if counter != 7 {
				t.Errorf("Unexpected number of attempts, want=7, got=%d", counter)
			}
		})
	}
}

func TestTransportHeaders(t *testing.T) {
	// Ensure cluster is ready by using modern test pattern
	_, err := testutil.NewClient(t)
	if err != nil {
		t.Fatalf("Failed to create client for cluster readiness check: %s", err)
	}

	hdr := http.Header{}
	hdr.Set("Accept", "application/yaml")

	// Use standardized URL construction and client config
	u := mockhttp.GetOpenSearchURL(t)
	config, err := testutil.ClientConfig(t)
	if err != nil {
		t.Fatalf("Failed to get client config: %s", err)
	}

	tp, _ := opensearchtransport.New(opensearchtransport.Config{
		URLs:      []*url.URL{u},
		Header:    hdr,
		Username:  config.Client.Username,
		Password:  config.Client.Password,
		Transport: config.Client.Transport,
	})

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	res, err := tp.Perform(req)
	if err != nil {
		t.Fatalf("Unexpected error: %s", err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("Unexpected error: %s", err)
	}

	if !bytes.HasPrefix(body, []byte("---")) {
		t.Errorf("Unexpected response body:\n%s", body)
	}
}

func TestTransportBodyClose(t *testing.T) {
	// Ensure cluster is ready by using modern test pattern
	_, err := testutil.NewClient(t)
	if err != nil {
		t.Fatalf("Failed to create client for cluster readiness check: %s", err)
	}

	// Use standardized URL construction and client config
	u := mockhttp.GetOpenSearchURL(t)
	config, err := testutil.ClientConfig(t)
	if err != nil {
		t.Fatalf("Failed to get client config: %s", err)
	}

	tp, _ := opensearchtransport.New(opensearchtransport.Config{
		URLs:      []*url.URL{u},
		Username:  config.Client.Username,
		Password:  config.Client.Password,
		Transport: config.Client.Transport,
	})

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	res, err := tp.Perform(req)
	if err != nil {
		t.Fatalf("Unexpected error: %s", err)
	}
	if closeResp := res.Body.Close(); closeResp != nil {
		t.Fatalf("Unexpected return on res.Body.Close(): %s", closeResp)
	}
	if closeResp := res.Body.Close(); closeResp != nil {
		t.Fatalf("Unexpected return on res.Body.Close(): %s", closeResp)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("Failed to read the response body: %s", err)
	}
	if len(body) == 0 {
		t.Fatalf("Unexpected response body:\n%s", body)
	}
}

func TestTransportCompression(t *testing.T) {
	// Ensure cluster is ready by using modern test pattern
	_, err := testutil.NewClient(t)
	if err != nil {
		t.Fatalf("Failed to create client for cluster readiness check: %s", err)
	}

	var req *http.Request
	var res *http.Response

	// Use standardized URL construction and client config
	u := mockhttp.GetOpenSearchURL(t)
	config, err := testutil.ClientConfig(t)
	if err != nil {
		t.Fatalf("Failed to get client config: %s", err)
	}

	transport, _ := opensearchtransport.New(opensearchtransport.Config{
		URLs:                []*url.URL{u},
		CompressRequestBody: true,
		Username:            config.Client.Username,
		Password:            config.Client.Password,
		Transport:           config.Client.Transport,
	})

	// Use unique index name for this test
	indexName := testutil.MustUniqueString(t, "/transport-compression-test")

	req, _ = http.NewRequest(http.MethodPut, indexName, nil)
	res, err = transport.Perform(req)
	if err != nil {
		t.Fatalf("Unexpected error, cannot create index: %v", err)
	}
	if res != nil && res.Body != nil {
		res.Body.Close()
	}

	req, _ = http.NewRequest(http.MethodGet, indexName, nil)
	res, err = transport.Perform(req)
	if err != nil {
		t.Fatalf("Unexpected error, cannot find index: %v", err)
	}
	if res != nil && res.Body != nil {
		res.Body.Close()
	}

	req, _ = http.NewRequest(
		http.MethodPost,
		strings.Join([]string{indexName, "/_doc"}, ""),
		strings.NewReader(`{"solidPayload": 1}`),
	)
	req.Header.Set("Content-Type", "application/json")
	res, err = transport.Perform(req)
	if err != nil {
		t.Fatalf("Unexpected error, cannot POST payload: %v", err)
	}
	if res != nil && res.Body != nil {
		res.Body.Close()
	}

	if res.StatusCode != http.StatusCreated {
		t.Fatalf("Unexpected StatusCode, expected 201, got: %v", res.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodDelete, indexName, nil)
	res, err = transport.Perform(req)
	if err != nil {
		t.Fatalf("Unexpected error, cannot DELETE %s: %v", indexName, err)
	}
	if res != nil && res.Body != nil {
		res.Body.Close()
	}
}
