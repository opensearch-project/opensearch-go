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

// +build !integration

package opensearch

import (
	"errors"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/opensearch-project/opensearch-go/v2/opensearchtransport"
)

var called bool

type mockTransp struct {
	RoundTripFunc func(*http.Request) (*http.Response, error)
}

var defaultRoundTripFunc = func(req *http.Request) (*http.Response, error) {
	response := &http.Response{Header: http.Header{}}

	if req.URL.Path == "/" {
		response.Body = ioutil.NopCloser(strings.NewReader(`{
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
	t.Parallel()

	t.Run("With empty", func(t *testing.T) {
		c, err := NewDefaultClient()

		if err != nil {
			t.Errorf("Unexpected error: %s", err)
		}

		u := c.Transport.(*opensearchtransport.Client).URLs()[0].String()

		if u != defaultURL {
			t.Errorf("Unexpected URL, want=%s, got=%s", defaultURL, u)
		}
	})

	t.Run("With URL from Addresses", func(t *testing.T) {
		c, err := NewClient(Config{Addresses: []string{"http://localhost:8080//"}, Transport: &mockTransp{}})
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}

		u := c.Transport.(*opensearchtransport.Client).URLs()[0].String()

		if u != "http://localhost:8080" {
			t.Errorf("Unexpected URL, want=http://localhost:8080, got=%s", u)
		}
	})

	t.Run("With URL from ELASTICSEARCH_URL", func(t *testing.T) {
		os.Setenv(envElasticsearchURL, "http://elasticsearch.com")
		defer func() { os.Setenv(envElasticsearchURL, "") }()

		c, err := NewClient(Config{Transport: &mockTransp{}})
		if err != nil {
			t.Errorf("Unexpected error: %s", err)
		}

		u := c.Transport.(*opensearchtransport.Client).URLs()[0].String()

		if u != "http://elasticsearch.com" {
			t.Errorf("Unexpected URL, want=http://elasticsearch.com, got=%s", u)
		}
	})

	t.Run("With URL from OPENSEARCH_URL", func(t *testing.T) {
		os.Setenv(envOpenSearchURL, "http://opensearch.com")
		defer func() { os.Setenv(envOpenSearchURL, "") }()

		c, err := NewClient(Config{Transport: &mockTransp{}})
		if err != nil {
			t.Errorf("Unexpected error: %s", err)
		}

		u := c.Transport.(*opensearchtransport.Client).URLs()[0].String()

		if u != "http://opensearch.com" {
			t.Errorf("Unexpected URL, want=http://opensearch.com, got=%s", u)
		}
	})

	t.Run("With URL from OPENSEARCH_URL and ELASTICSEARCH_URL", func(t *testing.T) {
		os.Setenv(envOpenSearchURL, "http://opensearch.com")
		defer func() { os.Setenv(envOpenSearchURL, "") }()

		os.Setenv(envElasticsearchURL, "http://elasticsearch.com")
		defer func() { os.Setenv(envElasticsearchURL, "") }()

		_, err := NewClient(Config{Transport: &mockTransp{}})
		assert.Error(t, err, "Expected error")

		match, _ := regexp.MatchString("both .* are set", err.Error())
		if !match {
			t.Errorf("Expected error when addresses from OPENSEARCH_URL and ELASTICSEARCH_URL are used together, got: %v", err)
		}
	})

	t.Run("With URL from environment and cfg.Addresses", func(t *testing.T) {
		os.Setenv(envElasticsearchURL, "http://example.com")
		defer func() { os.Setenv(envElasticsearchURL, "") }()

		c, err := NewClient(Config{Addresses: []string{"http://localhost:8080//"}, Transport: &mockTransp{}})
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}

		u := c.Transport.(*opensearchtransport.Client).URLs()[0].String()

		if u != "http://localhost:8080" {
			t.Errorf("Unexpected URL, want=http://localhost:8080, got=%s", u)
		}
	})

	t.Run("With invalid URL", func(t *testing.T) {
		u := ":foo"
		_, err := NewClient(Config{Addresses: []string{u}})

		if err == nil {
			t.Errorf("Expected error for URL %q, got %v", u, err)
		}
	})

	t.Run("With invalid URL from environment", func(t *testing.T) {
		os.Setenv(envElasticsearchURL, ":foobar")
		defer func() { os.Setenv(envElasticsearchURL, "") }()

		c, err := NewDefaultClient()
		if err == nil {
			t.Errorf("Expected error, got: %+v", c)
		}
	})

	t.Run("With skip check", func(t *testing.T) {
		_, err := NewClient(
			Config{
				Transport: &mockTransp{
					RoundTripFunc: func(request *http.Request) (*http.Response, error) {
						return &http.Response{
							Header: http.Header{},
							Body:   ioutil.NopCloser(strings.NewReader("")),
						}, nil
					},
				},
			})
		if err != nil {
			t.Errorf("Unexpected error, got: %+v", err)
		}
	})
}

func TestClientInterface(t *testing.T) {
	t.Run("Transport", func(t *testing.T) {
		c, err := NewClient(Config{Transport: &mockTransp{}})

		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}

		if called != false { // megacheck ignore
			t.Errorf("Unexpected call to transport by client")
		}

		c.Perform(&http.Request{URL: &url.URL{}, Header: make(http.Header)}) // errcheck ignore

		if called != true { // megacheck ignore
			t.Errorf("Expected client to call transport")
		}
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
				if err == nil {
					t.Errorf("Expected error, got: %v", err)
				}
				match, _ := regexp.MatchString(tc.err.Error(), err.Error())
				if !match {
					t.Errorf("Expected err [%s] to match: %s", err.Error(), tc.err.Error())
				}
			}

			for i := range tc.urls {
				if res[i].Scheme != tc.urls[i].Scheme {
					t.Errorf("%s: Unexpected scheme, want=%s, got=%s", tc.name, tc.urls[i].Scheme, res[i].Scheme)
				}
			}
			for i := range tc.urls {
				if res[i].Host != tc.urls[i].Host {
					t.Errorf("%s: Unexpected host, want=%s, got=%s", tc.name, tc.urls[i].Host, res[i].Host)
				}
			}
			for i := range tc.urls {
				if res[i].Path != tc.urls[i].Path {
					t.Errorf("%s: Unexpected path, want=%s, got=%s", tc.name, tc.urls[i].Path, res[i].Path)
				}
			}
		})
	}
}

func TestVersion(t *testing.T) {
	if Version == "" {
		t.Error("Version is empty")
	}
}

func TestClientMetrics(t *testing.T) {
	c, _ := NewClient(Config{EnableMetrics: true, Transport: &mockTransp{}})

	m, err := c.Metrics()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if m.Requests > 1 {
		t.Errorf("Unexpected output: %s", m)
	}
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
			got, got1, got2, err := ParseVersion(tt.version)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseVersion() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.major {
				t.Errorf("ParseVersion() got = %v, want %v", got, tt.major)
			}
			if got1 != tt.minor {
				t.Errorf("ParseVersion() got1 = %v, want %v", got1, tt.minor)
			}
			if got2 != tt.patch {
				t.Errorf("ParseVersion() got2 = %v, want %v", got2, tt.patch)
			}
		})
	}
}

func TestGenuineCheckInfo(t *testing.T) {
	tests := []struct {
		name    string
		info    info
		wantErr bool
		err     error
	}{
		{
			name: "Supported OpenSearch 1.0.0",
			info: info{
				Version: esVersion{
					Number:       "1.0.0",
					Distribution: openSearch,
				},
			},
			wantErr: false,
			err:     nil,
		},
		{
			name: "Supported Elasticsearch 7.10.0",
			info: info{
				Version: esVersion{
					Number:      "7.10.0",
					BuildFlavor: "anything",
				},
			},
			wantErr: false,
			err:     nil,
		},
		{
			name: "Unsupported Elasticsearch Version 6.15.1",
			info: info{
				Version: esVersion{
					Number:      "6.15.1",
					BuildFlavor: "default",
				},
				Tagline: "You Know, for Search",
			},
			wantErr: true,
			err:     errors.New(unsupportedProduct),
		},
		{
			name: "Elasticsearch oss",
			info: info{
				Version: esVersion{
					Number:      "7.10.0",
					BuildFlavor: "oss",
				},
				Tagline: "You Know, for Search",
			},
			wantErr: false,
			err:     nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := checkCompatibleInfo(tt.info); (err != nil) != tt.wantErr && err != tt.err {
				t.Errorf("checkCompatibleInfo() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
