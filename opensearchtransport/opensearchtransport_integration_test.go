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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi/testutil"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
	tptestutil "github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
	"github.com/opensearch-project/opensearch-go/v5/opensearchutil"
)

var _ = fmt.Print

func TestTransportRetries(t *testing.T) {
	var counter int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter++

		body, _ := io.ReadAll(r.Body)
		if testutil.IsDebugEnabled(t) {
			t.Logf("req.Body: %q", string(body))
		}

		http.Error(w, "FAKE 502", http.StatusBadGateway)
	}))
	serverURL, _ := url.Parse(server.URL)

	transport, _ := opensearchtransport.New(opensearchtransport.Config{URLs: []*url.URL{serverURL}})
	t.Cleanup(func() { _ = transport.Close() })

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

			res, err := transport.Stream(req)
			if err != nil {
				t.Fatalf("Unexpected error: %s", err)
			}
			defer res.Body.Close()

			body, _ := io.ReadAll(res.Body)
			res.Body.Close()

			if testutil.IsDebugEnabled(t) {
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
	_, err := testutil.InitClient(t)
	if err != nil {
		t.Fatalf("Failed to create client for cluster readiness check: %s", err)
	}

	// OpenSearch < 2.2.0 with the security plugin has a non-thread-safe User
	// serialization race (java.io.OptionalDataException) during inter-node
	// transport. Fixed in 2.2.0 by opensearch-project/security#1970.
	if testutil.IsSecure(t) {
		tptestutil.SkipIfVersion(t, "<", "2.2.0", "security plugin OptionalDataException")
	}

	hdr := http.Header{}
	hdr.Set("Accept", "application/yaml")

	// Use standardized URL construction and client config
	u := mockhttp.GetOpenSearchURL(t)
	config := testutil.ClientConfig(t)

	tp, _ := opensearchtransport.New(opensearchtransport.Config{
		URLs:      []*url.URL{u},
		Header:    hdr,
		Username:  config.Client.Username,
		Password:  config.Client.Password,
		Transport: config.Client.Transport,
	})
	t.Cleanup(func() { _ = tp.Close() })

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	res, err := tp.Stream(req)
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
	_, err := testutil.InitClient(t)
	if err != nil {
		t.Fatalf("Failed to create client for cluster readiness check: %s", err)
	}

	// OpenSearch < 2.2.0 with the security plugin has a non-thread-safe User
	// serialization race (java.io.OptionalDataException) during inter-node
	// transport. Fixed in 2.2.0 by opensearch-project/security#1970.
	if testutil.IsSecure(t) {
		tptestutil.SkipIfVersion(t, "<", "2.2.0", "security plugin OptionalDataException")
	}

	// Use standardized URL construction and client config
	u := mockhttp.GetOpenSearchURL(t)
	config := testutil.ClientConfig(t)

	tp, _ := opensearchtransport.New(opensearchtransport.Config{
		URLs:      []*url.URL{u},
		Username:  config.Client.Username,
		Password:  config.Client.Password,
		Transport: config.Client.Transport,
	})
	t.Cleanup(func() { _ = tp.Close() })

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	res, err := tp.Stream(req)
	if err != nil {
		t.Fatalf("Unexpected error: %s", err)
	}

	// Stream returns the raw, unbuffered body: the caller owns it and must
	// read before closing. Read first, then verify Close is idempotent.
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("Failed to read the response body: %s", err)
	}
	if len(body) == 0 {
		t.Fatalf("Unexpected response body:\n%s", body)
	}

	if closeResp := res.Body.Close(); closeResp != nil {
		t.Fatalf("Unexpected return on res.Body.Close(): %s", closeResp)
	}
	if closeResp := res.Body.Close(); closeResp != nil {
		t.Fatalf("Unexpected return on second res.Body.Close(): %s", closeResp)
	}
}

func TestTransportCompression(t *testing.T) {
	// Ensure cluster is ready by using modern test pattern
	_, err := testutil.InitClient(t)
	if err != nil {
		t.Fatalf("Failed to create client for cluster readiness check: %s", err)
	}

	// OpenSearch < 2.2.0 with the security plugin has a non-thread-safe User
	// serialization race (java.io.OptionalDataException) during inter-node
	// transport. Fixed in 2.2.0 by opensearch-project/security#1970.
	if testutil.IsSecure(t) {
		tptestutil.SkipIfVersion(t, "<", "2.2.0", "security plugin OptionalDataException")
	}

	var req *http.Request
	var res *http.Response

	// Use standardized URL construction and client config
	u := mockhttp.GetOpenSearchURL(t)
	config := testutil.ClientConfig(t)

	transport, _ := opensearchtransport.New(opensearchtransport.Config{
		URLs:                []*url.URL{u},
		CompressRequestBody: true,
		Username:            config.Client.Username,
		Password:            config.Client.Password,
		Transport:           config.Client.Transport,
	})
	t.Cleanup(func() { _ = transport.Close() })

	// Use unique index name for this test
	indexName := testutil.MustUniqueString(t, "/transport-compression-test")

	req, _ = http.NewRequest(http.MethodPut, indexName, nil)
	res, err = transport.Stream(req)
	if err != nil {
		t.Fatalf("Unexpected error, cannot create index: %v", err)
	}
	if res != nil && res.Body != nil {
		res.Body.Close()
	}

	req, _ = http.NewRequest(http.MethodGet, indexName, nil)
	res, err = transport.Stream(req)
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
	res, err = transport.Stream(req)
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
	res, err = transport.Stream(req)
	if err != nil {
		t.Fatalf("Unexpected error, cannot DELETE %s: %v", indexName, err)
	}
	if res != nil && res.Body != nil {
		res.Body.Close()
	}
}

func TestTransportAPIKeyAuth(t *testing.T) {
	// API key creation requires the security plugin, which is only present in
	// secure (HTTPS) integration environments.
	if !testutil.IsSecure(t) {
		t.Skip("TestTransportAPIKeyAuth requires SECURE_INTEGRATION=true (security plugin)")
	}

	tptestutil.SkipIfVersion(t, "<", "3.7.0", "API key auth was introduced in OpenSearch 3.7")

	config := testutil.ClientConfig(t)
	u := mockhttp.GetOpenSearchURL(t)

	// Step 1: create an API key using admin credentials. The apitokens endpoint
	// is only registered when api_tokens are enabled in the security config
	// (config.dynamic.api_tokens.enabled), so a non-200 means the feature is not
	// available on this cluster: skip rather than fail.
	adminTP, err := opensearchtransport.New(opensearchtransport.Config{
		URLs:      []*url.URL{u},
		Username:  config.Client.Username,
		Password:  config.Client.Password,
		Transport: config.Client.Transport,
	})
	if err != nil {
		t.Fatalf("failed to create admin transport: %s", err)
	}

	createBody := strings.NewReader(`{"name":"go-client-test-key","cluster_permissions":["cluster_monitor"]}`)
	createReq, _ := http.NewRequest(http.MethodPost, "/_plugins/_security/api/apitokens", createBody)
	createReq.Header.Set("Content-Type", "application/json")
	createRes, err := adminTP.Stream(createReq)
	if err != nil {
		t.Fatalf("failed to call /_plugins/_security/api/apitokens: %s", err)
	}
	defer createRes.Body.Close()

	if createRes.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(createRes.Body)
		t.Skipf("API key tokens not enabled on this cluster (status %d): %s", createRes.StatusCode, body)
	}

	// The plain-text token (prefixed "os_") is returned once in the "token" field.
	var keyResp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(createRes.Body).Decode(&keyResp); err != nil {
		t.Fatalf("failed to decode API key response: %s", err)
	}
	if keyResp.Token == "" {
		t.Fatal("API key response missing 'token' field")
	}

	// Step 2: build a transport that authenticates solely with the API key.
	keyTP, err := opensearchtransport.New(opensearchtransport.Config{
		URLs:      []*url.URL{u},
		APIKey:    keyResp.Token,
		Transport: config.Client.Transport,
	})
	if err != nil {
		t.Fatalf("failed to create API key transport: %s", err)
	}

	// Step 3: verify the API key (with cluster_monitor) grants access.
	infoReq, _ := http.NewRequest(http.MethodGet, "/_cluster/health", nil)
	infoRes, err := keyTP.Stream(infoReq)
	if err != nil {
		t.Fatalf("GET /_cluster/health with API key failed: %s", err)
	}
	defer infoRes.Body.Close()
	_, _ = io.ReadAll(infoRes.Body) // drain body; only the status matters here

	if infoRes.StatusCode != http.StatusOK {
		t.Errorf("expected 200 from GET /_cluster/health with API key, got %d", infoRes.StatusCode)
	}
}
