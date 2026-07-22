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
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/build"
	"github.com/opensearch-project/opensearch-go/v5/internal/ttlcache"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
)

var called int

func boolPtr(v bool) *bool { return &v }

var defaultRoundTripFunc = func(req *http.Request) (*http.Response, error) {
	response := &http.Response{Header: http.Header{}}

	switch req.URL.Path {
	case "/":
		response.Body = io.NopCloser(strings.NewReader(`{
		  "version" : {
			"number" : "1.0.0",
			"distribution" : "opensearch"
		  }
		}`))
		response.Header.Add("Content-Type", "application/json")
	case "/test":
		called++
	}

	return response, nil
}

type testReq struct {
	Method  string
	Path    string
	Body    io.Reader
	Params  map[string]string
	Error   bool
	Headers http.Header
}

func (r testReq) GetRequest(method string) (*http.Request, error) {
	if r.Error {
		return nil, fmt.Errorf("test error")
	}
	return build.Request(
		method,
		r.Path,
		r.Body,
		r.Params,
		r.Headers,
	)
}

func TestClientConfiguration(t *testing.T) {
	t.Run("With empty", func(t *testing.T) {
		c, err := NewDefaultClient()
		require.NoError(t, err)
		u := c.Transport.(*opensearchtransport.Transport).URLs()[0].String()
		require.Equal(t, defaultURL, u)
	})

	t.Run("With URL from Addresses", func(t *testing.T) {
		c, err := NewClient(Config{Addresses: []string{"http://localhost:8080//"}, Transport: mockhttp.NewRoundTripFunc(t, defaultRoundTripFunc)})
		require.NoError(t, err)
		u := c.Transport.(*opensearchtransport.Transport).URLs()[0].String()
		require.Equal(t, "http://localhost:8080", u)
	})

	t.Run("With URL from OPENSEARCH_URL", func(t *testing.T) {
		os.Setenv(envOpenSearchURL, "http://opensearch.com")
		defer func() { os.Setenv(envOpenSearchURL, "") }()

		c, err := NewClient(Config{Transport: mockhttp.NewRoundTripFunc(t, defaultRoundTripFunc)})
		require.NoError(t, err)
		u := c.Transport.(*opensearchtransport.Transport).URLs()[0].String()
		require.Equal(t, "http://opensearch.com", u)
	})

	t.Run("With URL from environment and cfg.Addresses", func(t *testing.T) {
		os.Setenv(envOpenSearchURL, "http://example.com")
		defer func() { os.Setenv(envOpenSearchURL, "") }()

		c, err := NewClient(Config{Addresses: []string{"http://localhost:8080//"}, Transport: mockhttp.NewRoundTripFunc(t, defaultRoundTripFunc)})
		require.NoError(t, err)
		u := c.Transport.(*opensearchtransport.Transport).URLs()[0].String()
		require.Equal(t, "http://localhost:8080", u)
	})

	t.Run("With invalid URL", func(t *testing.T) {
		u := ":foo"
		_, err := NewClient(Config{Addresses: []string{u}})

		require.Error(t, err)
	})

	t.Run("With invalid URL from environment", func(t *testing.T) {
		os.Setenv(envOpenSearchURL, ":foobar")
		defer func() { os.Setenv(envOpenSearchURL, "") }()

		_, err := NewDefaultClient()
		require.Error(t, err)
	})

	t.Run("With skip check", func(t *testing.T) {
		_, err := NewClient(
			Config{
				Transport: mockhttp.NewRoundTripFunc(t, func(request *http.Request) (*http.Response, error) {
					return &http.Response{
						Header: http.Header{},
						Body:   io.NopCloser(strings.NewReader("")),
					}, nil
				}),
			})
		require.NoError(t, err)
	})

	t.Run("With User:Password", func(t *testing.T) {
		c, err := NewClient(
			Config{
				Addresses: []string{"http://admin:admin@localhost:8080//"},
				Transport: mockhttp.NewRoundTripFunc(t, defaultRoundTripFunc),
			},
		)
		require.NoError(t, err)
		u := c.Transport.(*opensearchtransport.Transport).URLs()[0].String()
		require.Equal(t, "http://admin:admin@localhost:8080", u)
	})

	t.Run("With DiscoverNodes on start", func(t *testing.T) {
		c, err := NewClient(
			Config{
				Addresses:            []string{"http://localhost:8080//"},
				Transport:            mockhttp.NewRoundTripFunc(t, defaultRoundTripFunc),
				DiscoverNodesOnStart: boolPtr(true),
			},
		)
		require.NoError(t, err)
		u := c.Transport.(*opensearchtransport.Transport).URLs()[0].String()
		require.Equal(t, "http://localhost:8080", u)
	})

	t.Run("With failing creation", func(t *testing.T) {
		_, err := NewClient(
			Config{
				Addresses: []string{"http://admin:admin@localhost:8080//"},
				Transport: mockhttp.NewRoundTripFunc(t, defaultRoundTripFunc),
				CACert:    []byte{1},
			},
		)
		require.ErrorIs(t, err, ErrCreateTransport)
	})
}

func TestClientInterfe(t *testing.T) {
	t.Run("Stream()", func(t *testing.T) {
		c, err := NewClient(Config{Transport: mockhttp.NewRoundTripFunc(t, defaultRoundTripFunc)})
		require.NoError(t, err)

		call := called

		res, err := c.Stream(&http.Request{URL: &url.URL{Path: "/test"}, Header: make(http.Header)})
		if err == nil && res != nil && res.Body != nil {
			res.Body.Close()
		}

		require.Equal(t, called-1, call, "Expected client to call transport")
	})

	t.Run("Do()", func(t *testing.T) {
		c, err := NewClient(Config{Transport: mockhttp.NewRoundTripFunc(t, defaultRoundTripFunc)})
		require.NoError(t, err)

		req := testReq{}
		resp, err := Execute[NoBody](t.Context(), c, http.MethodGet, req, nil)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("Generic Execute()", func(t *testing.T) {
		c, err := NewClient(Config{Transport: mockhttp.NewRoundTripFunc(t, defaultRoundTripFunc)})
		require.NoError(t, err)

		type versionInfo struct {
			Number       string `json:"number"`
			Distribution string `json:"distribution"`
		}
		type rootResp struct {
			Version versionInfo `json:"version"`
		}

		var got rootResp
		resp, err := Execute(t.Context(), c, http.MethodGet, testReq{Path: "/"}, &got)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, "1.0.0", got.Version.Number)
		require.Equal(t, "opensearch", got.Version.Distribution)
	})

	t.Run("Generic Execute() nil NoBody pointer", func(t *testing.T) {
		c, err := NewClient(Config{Transport: mockhttp.NewRoundTripFunc(t, defaultRoundTripFunc)})
		require.NoError(t, err)

		resp, err := Execute[NoBody](t.Context(), c, http.MethodGet, testReq{Path: "/"}, nil)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("Do() GetRequest error", func(t *testing.T) {
		c, err := NewClient(Config{Transport: mockhttp.NewRoundTripFunc(t, defaultRoundTripFunc)})
		require.NoError(t, err)

		req := testReq{Error: true}
		resp, err := Execute[NoBody](t.Context(), c, http.MethodGet, req, nil)
		require.Error(t, err)
		require.Nil(t, resp)
	})

	t.Run("Do() Unmarshal error", func(t *testing.T) {
		c, err := NewClient(Config{Transport: mockhttp.NewRoundTripFunc(t, defaultRoundTripFunc)})
		require.NoError(t, err)

		type failStr struct {
			Version int `json:"version"`
		}
		req := testReq{Path: "/"}
		resp, err := Execute(t.Context(), c, http.MethodGet, req, &failStr{})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrJSONUnmarshalBody)
		require.NotNil(t, resp)
	})

	t.Run("Do() io read error", func(t *testing.T) {
		c, err := NewClient(
			Config{
				Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					return &http.Response{
							StatusCode: http.StatusOK,
							Body:       io.NopCloser(iotest.ErrReader(errors.New("io reader test"))),
						},
						nil
				}),
			},
		)
		require.NoError(t, err)

		type failStr struct {
			Version int `json:"version"`
		}
		req := testReq{}
		resp, err := Execute(t.Context(), c, http.MethodGet, req, &failStr{})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrReadBody)
		require.NotNil(t, resp)
	})
}

// TestResponseString_RawBody verifies that String renders from the buffered
// rawBody set by Do and never consumes Body, so a value copy (as fmt makes)
// renders the body and Body remains readable afterwards.
func TestResponseString_RawBody(t *testing.T) {
	c, err := NewClient(Config{Transport: mockhttp.NewRoundTripFunc(t, defaultRoundTripFunc)})
	require.NoError(t, err)

	type versionInfo struct {
		Number string `json:"number"`
	}
	type rootResp struct {
		Version versionInfo `json:"version"`
	}

	var got rootResp
	resp, err := Execute(t.Context(), c, http.MethodGet, testReq{Path: "/"}, &got)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.rawBody, "Do should buffer the decoded body into rawBody")

	// A value copy renders the buffered body without draining Body.
	valueCopy := *resp
	rendered := valueCopy.String()
	require.Contains(t, rendered, `"number" : "1.0.0"`)

	// Body is still fully readable after rendering.
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), `"number" : "1.0.0"`)
}

// TestResponseString_ErrorBodyNotDrained reproduces the canonical error-handling
// flow: logging an error response with String() must not drain Body, so a
// subsequent ParseError still sees the real API error. Do buffers error-response
// bodies into rawBody, so String takes the rawBody fast-path and never touches
// Body. Without that buffering, the value-receiver String would consume the
// single-use error stream and ParseError would read an empty body.
func TestResponseString_ErrorBodyNotDrained(t *testing.T) {
	const errBody = `{"error":{"type":"index_not_found_exception","reason":"no such index"},"status":404}`

	rt := mockhttp.NewRoundTripFunc(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(errBody)),
		}, nil
	})
	c, err := NewClient(Config{Transport: rt})
	require.NoError(t, err)

	type rootResp struct {
		Version struct{ Number string } `json:"version"`
	}
	var got rootResp
	resp, err := Execute(t.Context(), c, http.MethodGet, testReq{Path: "/"}, &got)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.True(t, resp.IsError())
	require.NotNil(t, resp.rawBody, "Do should buffer error-response bodies into rawBody")

	// Logging the error via String (value copy, as fmt makes) must not drain Body.
	valueCopy := *resp
	_ = valueCopy.String()

	// ParseError still reads the intact body and surfaces the real API error,
	// not ErrJSONUnmarshalBody from an empty payload.
	perr := ParseError(resp)
	require.Error(t, perr)
	require.NotErrorIs(t, perr, ErrJSONUnmarshalBody)
	require.Contains(t, perr.Error(), "index_not_found_exception")
}

// fakeTransport is an opensearchtransport.Interface that returns a fixed
// (response, error) pair, used to exercise Client.Do's handling of the
// (resp != nil, err != nil) contract that Stream may return.
type fakeTransport struct {
	resp *http.Response
	err  error
}

func (f fakeTransport) Stream(*http.Request) (*http.Response, error) {
	return f.resp, f.err
}

func (f fakeTransport) Request(req *http.Request) (*http.Response, error) {
	return f.Stream(req)
}

// TestDoStreamErrorClassification verifies that Client.Do only labels a
// returned error as ErrReadBody when it is a genuine body-read failure
// (signaled by opensearchtransport.ErrResponseBodyRead). An unrelated transport
// error returned alongside a response -- e.g. context cancellation during retry
// backoff -- must surface with its identity intact and without the misleading
// "failed to read body" prefix.
func TestDoStreamErrorClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		streamErr   error
		wantReadErr bool   // expect errors.Is(err, ErrReadBody)
		wantIs      error  // an error the result must still wrap (nil to skip)
		wantNotMsg  string // substring that must NOT appear in the message (empty to skip)
	}{
		{
			name:        "genuine body-read failure is labeled ErrReadBody",
			streamErr:   fmt.Errorf("%w: %w", opensearchtransport.ErrResponseBodyRead, io.ErrUnexpectedEOF),
			wantReadErr: true,
			wantIs:      io.ErrUnexpectedEOF,
		},
		{
			name:        "context cancellation during backoff is not ErrReadBody",
			streamErr:   context.Canceled,
			wantReadErr: false,
			wantIs:      context.Canceled,
			wantNotMsg:  "failed to read body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Disable on-start discovery: this test swaps c.Transport after
			// construction, which would race with the discovery goroutine that
			// the default (router-on) config otherwise spawns.
			noDiscovery := false
			c, err := NewClient(Config{
				Transport:            mockhttp.NewRoundTripFunc(t, defaultRoundTripFunc),
				DiscoverNodesOnStart: &noDiscovery,
			})
			require.NoError(t, err)

			// Replace the transport with one that returns a fixed
			// (response, error) pair so Do exercises the (resp != nil,
			// err != nil) classification path.
			c.Transport = fakeTransport{
				resp: &http.Response{
					StatusCode: http.StatusServiceUnavailable,
					Header:     http.Header{},
					Body:       io.NopCloser(strings.NewReader("")),
				},
				err: tt.streamErr,
			}

			resp, err := Execute[NoBody](t.Context(), c, http.MethodGet, testReq{Path: "/test"}, nil)
			require.Error(t, err)
			require.NotNil(t, resp, "response must be returned alongside the error")

			if tt.wantReadErr {
				require.ErrorIs(t, err, ErrReadBody)
			} else {
				require.NotErrorIs(t, err, ErrReadBody)
			}
			if tt.wantIs != nil {
				require.ErrorIs(t, err, tt.wantIs)
			}
			if tt.wantNotMsg != "" {
				require.NotContains(t, err.Error(), tt.wantNotMsg)
			}
		})
	}
}

// headerCapturingTransport records the request header it is handed so a test
// can assert Client.Do never dispatches a request with a nil Header map.
type headerCapturingTransport struct {
	gotHeader http.Header
	called    bool
}

func (h *headerCapturingTransport) Stream(req *http.Request) (*http.Response, error) {
	h.called = true
	h.gotHeader = req.Header
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("{}")),
	}, nil
}

func (h *headerCapturingTransport) Request(req *http.Request) (*http.Response, error) {
	return h.Stream(req)
}

// TestDoInitializesNilRequestHeader guards the contract that Client.Do routes
// through Client.Stream, which allocates req.Header when a Request builds one
// with a nil Header. A custom transport that touches req.Header (e.g. calls
// req.SetBasicAuth) would otherwise panic with "assignment to entry in nil
// map". Regression test for the Perform -> Stream transition.
func TestDoInitializesNilRequestHeader(t *testing.T) {
	t.Parallel()

	noDiscovery := false
	c, err := NewClient(Config{
		Transport:            mockhttp.NewRoundTripFunc(t, defaultRoundTripFunc),
		DiscoverNodesOnStart: &noDiscovery,
	})
	require.NoError(t, err)

	tr := &headerCapturingTransport{}
	c.Transport = tr

	// testReq with no Headers builds an *http.Request whose Header is nil.
	resp, err := Execute[NoBody](t.Context(), c, http.MethodGet, testReq{Path: "/test"}, nil)
	require.NoError(t, err)
	require.NotNil(t, resp)

	require.True(t, tr.called, "transport must be invoked")
	require.NotNil(t, tr.gotHeader, "Do must hand the transport a non-nil request Header")
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
				require.Contains(t, err.Error(), tc.err.Error())
			}

			for i := range tc.urls {
				require.Equal(t, tc.urls[i].Scheme, res[i].Scheme, tc.name)
			}
			for i := range tc.urls {
				require.Equal(t, tc.urls[i].Host, res[i].Host, tc.name)
			}
			for i := range tc.urls {
				require.Equal(t, tc.urls[i].Path, res[i].Path, tc.name)
			}
		})
	}
}

func TestVersion(t *testing.T) {
	require.NotEmpty(t, Version)
}

func TestClientMetrics(t *testing.T) {
	c, _ := NewClient(Config{Transport: mockhttp.NewRoundTripFunc(t, defaultRoundTripFunc)})

	m, err := c.Metrics()
	require.NoError(t, err)

	require.LessOrEqual(t, m.Requests, 1, m)
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
			require.Equal(t, major, tt.major)
			require.Equal(t, minor, tt.minor)
			require.Equal(t, patch, tt.patch)
		})
	}
}

func TestToPointer(t *testing.T) {
	testPointer := ToPointer(true)
	require.NotNil(t, testPointer)
	require.True(t, *testPointer)
}

func TestClientGetConfig(t *testing.T) {
	t.Run("returns config", func(t *testing.T) {
		expectedAddresses := []string{"http://localhost:9200"}
		expectedUsername := "admin"
		expectedMaxRetries := 5

		osClient, err := NewClient(Config{
			Addresses:  expectedAddresses,
			Username:   expectedUsername,
			MaxRetries: expectedMaxRetries,
			Transport:  mockhttp.NewRoundTripFunc(t, defaultRoundTripFunc),
		})
		require.NoError(t, err)

		config := osClient.GetConfig()

		require.Equal(t, expectedAddresses, config.Addresses)
		require.Equal(t, expectedUsername, config.Username)
		require.Equal(t, expectedMaxRetries, config.MaxRetries)
	})

	t.Run("preserves all config fields", func(t *testing.T) {
		expectedConfig := Config{
			Addresses:            []string{"http://localhost:9200", "http://localhost:9201"},
			Username:             "testuser",
			Password:             "testpass",
			DisableRetry:         true,
			MaxRetries:           10,
			CompressRequestBody:  true,
			EnableRetryOnTimeout: true,
			Transport:            mockhttp.NewRoundTripFunc(t, defaultRoundTripFunc),
		}

		osClient, err := NewClient(expectedConfig)
		require.NoError(t, err)

		config := osClient.GetConfig()

		require.Equal(t, expectedConfig.Addresses, config.Addresses)
		require.Equal(t, expectedConfig.Username, config.Username)
		require.Equal(t, expectedConfig.Password, config.Password)
		require.Equal(t, expectedConfig.DisableRetry, config.DisableRetry)
		require.Equal(t, expectedConfig.MaxRetries, config.MaxRetries)
		require.Equal(t, expectedConfig.CompressRequestBody, config.CompressRequestBody)
		require.Equal(t, expectedConfig.EnableRetryOnTimeout, config.EnableRetryOnTimeout)
	})
}

func TestClientClose(t *testing.T) {
	t.Run("release hook takes precedence", func(t *testing.T) {
		count := 0
		c := &Client{release: func() error { count++; return nil }}
		require.NoError(t, c.Close())
		require.Equal(t, 1, count)
	})

	t.Run("falls back to transport io.Closer", func(t *testing.T) {
		tc := &stubTransportCloser{}
		c := &Client{Transport: tc}
		require.NoError(t, c.Close())
		require.Equal(t, 1, tc.closed)
	})

	t.Run("no-op when transport lacks Close", func(t *testing.T) {
		c := &Client{Transport: stubStreamOnly{}}
		require.NoError(t, c.Close())
	})
}

// stubTransportCloser implements opensearchtransport.Interface + io.Closer.
type stubTransportCloser struct{ closed int }

//nolint:nilnil // stub: Stream is never called, only Close is exercised
func (s *stubTransportCloser) Stream(*http.Request) (*http.Response, error) { return nil, nil }

//nolint:nilnil // stub: Request is never called, only Close is exercised
func (s *stubTransportCloser) Request(*http.Request) (*http.Response, error) { return nil, nil }

//nolint:unparam // Close must return error to satisfy io.Closer; stub never fails
func (s *stubTransportCloser) Close() error { s.closed++; return nil }

// stubStreamOnly implements only opensearchtransport.Interface.
type stubStreamOnly struct{}

//nolint:nilnil // stub: Stream is never called, exists only to satisfy Interface
func (stubStreamOnly) Stream(*http.Request) (*http.Response, error) { return nil, nil }

//nolint:nilnil // stub: Request is never called, exists only to satisfy Interface
func (stubStreamOnly) Request(*http.Request) (*http.Response, error) { return nil, nil }

func TestConfigKey(t *testing.T) {
	t.Run("hashable configs", func(t *testing.T) {
		tests := []struct {
			name   string
			a, b   Config
			wantEq bool
		}{
			{"identical empty", Config{}, Config{}, true},
			{"same addresses", Config{Addresses: []string{"http://x:9200"}}, Config{Addresses: []string{"http://x:9200"}}, true},
			{"diff addresses", Config{Addresses: []string{"http://x:9200"}}, Config{Addresses: []string{"http://y:9200"}}, false},
			{"diff username", Config{Username: "a"}, Config{Username: "b"}, false},
			{"diff password", Config{Password: "a"}, Config{Password: "b"}, false},
			{"diff insecure", Config{InsecureSkipVerify: true}, Config{}, false},
			{"diff cacert", Config{CACert: []byte("a")}, Config{CACert: []byte("b")}, false},
			{"diff maxretries", Config{MaxRetries: 3}, Config{MaxRetries: 5}, false},
			{"diff request timeout", Config{RequestTimeout: time.Second}, Config{}, false},
			{"same header", Config{Header: http.Header{"X": {"1"}}}, Config{Header: http.Header{"X": {"1"}}}, true},
			{"diff header", Config{Header: http.Header{"X": {"1"}}}, Config{Header: http.Header{"X": {"2"}}}, false},
			{
				"header value-count boundary",
				Config{Header: http.Header{"a": {"b"}, "c": {"d"}}},
				Config{Header: http.Header{"a": {"b", "c", "d"}}},
				false,
			},
			{"diff retry-on-status", Config{RetryOnStatus: []int{502}}, Config{RetryOnStatus: []int{503}}, false},
			{"same retry-on-status", Config{RetryOnStatus: []int{502, 503}}, Config{RetryOnStatus: []int{502, 503}}, true},
			{
				"discover-on-start true vs false",
				Config{DiscoverNodesOnStart: boolPtr(true)},
				Config{DiscoverNodesOnStart: boolPtr(false)},
				false,
			},
			{
				"discover-on-start explicit false vs nil auto",
				Config{DiscoverNodesOnStart: boolPtr(false)},
				Config{},
				false,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				ha, oka := configKey(tt.a)
				hb, okb := configKey(tt.b)
				require.True(t, oka)
				require.True(t, okb)
				if tt.wantEq {
					require.Equal(t, ha, hb)
				} else {
					require.NotEqual(t, ha, hb)
				}
			})
		}
	})

	t.Run("un-hashable configs bypass", func(t *testing.T) {
		tests := []struct {
			name string
			cfg  Config
		}{
			{"transport", Config{Transport: http.DefaultTransport}},
			{"context", Config{Context: context.Background()}},
			{"retry backoff", Config{RetryBackoff: func(int) time.Duration { return 0 }}},
			{"health modifier", Config{HealthCheckRequestModifier: func(*http.Request) {}}},
			{"operation classifier", Config{OperationClassifier: opensearchtransport.NewOperationClassifier()}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, ok := configKey(tt.cfg)
				require.False(t, ok, "un-hashable field must force bypass")
			})
		}
	})
}

// TestCachedDefaultKeyNotCacheable verifies cachedDefault.Key surfaces
// ttlcache.ErrNotCacheable for an un-hashable config, so GetOrCreate falls
// back to a direct build instead of caching a client that cannot be keyed.
func TestCachedDefaultKeyNotCacheable(t *testing.T) {
	d := cachedDefault{cfg: Config{Transport: http.DefaultTransport}}
	_, err := d.Key()
	require.ErrorIs(t, err, ttlcache.ErrNotCacheable)
}

// TestConfigKey_FieldGuard fails loudly when Config grows a field without a
// corresponding update to configKey, preventing a silent cache-key collision.
func TestConfigKey_FieldGuard(t *testing.T) {
	const knownFieldCount = 48
	got := reflect.TypeFor[Config]().NumField()
	require.Equal(t, knownFieldCount, got,
		"Config field count changed: audit configKey for the new field, then update knownFieldCount")
}

func TestNewDefaultClientCaches(t *testing.T) {
	c1, err := NewDefaultClient()
	require.NoError(t, err)
	t.Cleanup(func() { _ = c1.Close() })

	c2, err := NewDefaultClient()
	require.NoError(t, err)
	t.Cleanup(func() { _ = c2.Close() })

	require.NotSame(t, c1, c2, "each call returns a distinct *Client wrapper")
	require.Same(t, c1.Transport, c2.Transport, "identical default config must share one transport")
	require.NotNil(t, c1.release, "cached client must carry a release hook")
	require.NotNil(t, c1.GetConfig(), "cached client must preserve config threaded through the shared handle")
}

// TestNewClientNeverCaches locks in the acceptance criterion that user-built
// NewClient(cfg) clients never enter the shared cache -- only the implicit
// default path (NewDefaultClient) is cached. Two explicit calls with identical
// config must build independent transports and carry no cache release hook.
func TestNewClientNeverCaches(t *testing.T) {
	cfg := Config{Addresses: []string{"http://never-cache-core:9200"}}
	c1, err := NewClient(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c1.Close() })
	c2, err := NewClient(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c2.Close() })

	require.NotSame(t, c1.Transport, c2.Transport,
		"explicit NewClient must not share a cached transport")
	require.Nil(t, c1.release, "explicit NewClient must not carry a cache release hook")
}
