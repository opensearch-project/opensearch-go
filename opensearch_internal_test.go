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

//go:build !integration

package opensearch

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/opensearch-project/opensearch-go/v3/opensearchtransport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var called bool

type mockTransp struct {
	RoundTripFunc func(*http.Request) (*http.Response, error)
}

var defaultRoundTripFunc = func(req *http.Request) (*http.Response, error) {
	response := &http.Response{Header: http.Header{}}

	if req.URL.Path == "/" {
		response.Body = io.NopCloser(strings.NewReader(`{
		  "version" : {
			"number" : "1.0.0",
			"distribution" : "opensearch"
		  }
		}`))
		response.Header.Add("Content-Type", "application/json")
	} else {
		called = true
	}

	return response, nil
}

func (t *mockTransp) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.RoundTripFunc == nil {
		return defaultRoundTripFunc(req)
	}
	return t.RoundTripFunc(req)
}

func TestClientConfiguration(t *testing.T) {
	t.Run("With empty", func(t *testing.T) {
		c, err := NewDefaultClient()
		require.NoError(t, err)
		u := c.Transport.(*opensearchtransport.Client).URLs()[0].String()
		assert.Equal(t, u, defaultURL)
	})

	t.Run("With URL from Addresses", func(t *testing.T) {
		c, err := NewClient(Config{Addresses: []string{"http://localhost:8080//"}, Transport: &mockTransp{}})
		require.NoError(t, err)
		u := c.Transport.(*opensearchtransport.Client).URLs()[0].String()
		assert.Equal(t, u, "http://localhost:8080")
	})

	t.Run("With URL from OPENSEARCH_URL", func(t *testing.T) {
		os.Setenv(envOpenSearchURL, "http://opensearch.com")
		defer func() { os.Setenv(envOpenSearchURL, "") }()

		c, err := NewClient(Config{Transport: &mockTransp{}})
		require.NoError(t, err)
		u := c.Transport.(*opensearchtransport.Client).URLs()[0].String()
		assert.Equal(t, u, "http://opensearch.com")
	})

	t.Run("With URL from environment and cfg.Addresses", func(t *testing.T) {
		os.Setenv(envOpenSearchURL, "http://example.com")
		defer func() { os.Setenv(envOpenSearchURL, "") }()

		c, err := NewClient(Config{Addresses: []string{"http://localhost:8080//"}, Transport: &mockTransp{}})
		require.NoError(t, err)
		u := c.Transport.(*opensearchtransport.Client).URLs()[0].String()
		assert.Equal(t, u, "http://localhost:8080")
	})

	t.Run("With invalid URL", func(t *testing.T) {
		u := ":foo"
		_, err := NewClient(Config{Addresses: []string{u}})

		assert.Error(t, err)
	})

	t.Run("With invalid URL from environment", func(t *testing.T) {
		os.Setenv(envOpenSearchURL, ":foobar")
		defer func() { os.Setenv(envOpenSearchURL, "") }()

		_, err := NewDefaultClient()
		assert.Error(t, err)
	})

	t.Run("With skip check", func(t *testing.T) {
		_, err := NewClient(
			Config{
				Transport: &mockTransp{
					RoundTripFunc: func(request *http.Request) (*http.Response, error) {
						return &http.Response{
							Header: http.Header{},
							Body:   io.NopCloser(strings.NewReader("")),
						}, nil
					},
				},
			})
		assert.NoError(t, err)
	})
}

func TestClientInterface(t *testing.T) {
	t.Run("Transport", func(t *testing.T) {
		c, err := NewClient(Config{Transport: &mockTransp{}})
		require.NoError(t, err)

		assert.False(t, called, "Unexpected call to transport by client")

		res, err := c.Perform(&http.Request{URL: &url.URL{}, Header: make(http.Header)}) // errcheck ignore
		if err == nil && res != nil && res.Body != nil {
			res.Body.Close()
		}

		assert.True(t, called, "Expected client to call transport")
	})
}

func TestAddrsToURLs(t *testing.T) {
	tt := []struct {
		name  string
		addrs []string
		urls  []*url.URL
		err   error
	}{
		{
			name: "valid",
			addrs: []string{
				"http://example.com",
				"https://example.com",
				"http://192.168.255.255",
				"http://example.com:8080",
			},
			urls: []*url.URL{
				{Scheme: "http", Host: "example.com"},
				{Scheme: "https", Host: "example.com"},
				{Scheme: "http", Host: "192.168.255.255"},
				{Scheme: "http", Host: "example.com:8080"},
			},
			err: nil,
		},
		{
			name:  "trim trailing slash",
			addrs: []string{"http://example.com/", "http://example.com//"},
			urls: []*url.URL{
				{Scheme: "http", Host: "example.com", Path: ""},
				{Scheme: "http", Host: "example.com", Path: ""},
			},
		},
		{
			name:  "keep suffix",
			addrs: []string{"http://example.com/foo"},
			urls:  []*url.URL{{Scheme: "http", Host: "example.com", Path: "/foo"}},
		},
		{
			name:  "invalid url",
			addrs: []string{"://invalid.com"},
			urls:  nil,
			err:   errors.New("missing protocol scheme"),
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			res, err := addrsToURLs(tc.addrs)

			if tc.err != nil {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.err.Error())
			}

			for i := range tc.urls {
				assert.Equal(t, tc.urls[i].Scheme, res[i].Scheme, tc.name)
			}
			for i := range tc.urls {
				assert.Equal(t, tc.urls[i].Host, res[i].Host, tc.name)
			}
			for i := range tc.urls {
				assert.Equal(t, tc.urls[i].Path, res[i].Path, tc.name)
			}
		})
	}
}

func TestVersion(t *testing.T) {
	require.NotEmpty(t, Version)
}

func TestClientMetrics(t *testing.T) {
	c, _ := NewClient(Config{EnableMetrics: true, Transport: &mockTransp{}})

	m, err := c.Metrics()
	require.Nil(t, err)

	assert.LessOrEqual(t, m.Requests, 1, m)
}

func TestParseElasticsearchVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		major   int64
		minor   int64
		patch   int64
		wantErr bool
	}{
		{
			name:    "Nominal version parsing",
			version: "7.14.0",
			major:   7,
			minor:   14,
			patch:   0,
			wantErr: false,
		},
		{
			name:    "Snapshot version parsing",
			version: "1.0.0-SNAPSHOT",
			major:   1,
			minor:   0,
			patch:   0,
			wantErr: false,
		},
		{
			name:    "Previous major parsing",
			version: "6.15.1",
			major:   6,
			minor:   15,
			patch:   1,
			wantErr: false,
		},
		{
			name:    "Error parsing version",
			version: "6.15",
			major:   0,
			minor:   0,
			patch:   0,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			major, minor, patch, err := ParseVersion(tt.version)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, major, tt.major)
			assert.Equal(t, minor, tt.minor)
			assert.Equal(t, patch, tt.patch)
		})
	}
}
