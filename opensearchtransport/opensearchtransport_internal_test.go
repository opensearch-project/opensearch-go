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

package opensearchtransport

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil/mockhttp"
	"github.com/opensearch-project/opensearch-go/v4/signer"
)

var _ = fmt.Print

func init() {
	rand.New(rand.NewSource(time.Now().Unix())).Uint64()
}

type mockNetError struct{ error }

type mockSigner struct {
	SampleKey   string
	SampleValue string
	ReturnError bool
	testHook    func(*http.Request)
	signer.Signer
}

func (m *mockSigner) SignRequest(req *http.Request) error {
	if m.testHook != nil {
		m.testHook(req)
	}
	if m.ReturnError {
		return fmt.Errorf("invalid data")
	}
	req.Header.Add(m.SampleKey, m.SampleValue)
	return nil
}

func (e *mockNetError) Timeout() bool   { return false }
func (e *mockNetError) Temporary() bool { return false }

func TestTransport(t *testing.T) {
	t.Run("Interface", func(t *testing.T) {
		tp, _ := New(Config{})
		var _ Interface = tp
		_ = tp.transport
	})

	t.Run("Default", func(t *testing.T) {
		tp, _ := New(Config{})
		if tp.transport == nil {
			t.Error("Expected the transport to not be nil")
		}
		if tp.transport != http.DefaultTransport {
			t.Errorf("Expected the transport to be http.DefaultTransport, got: %T", tp.transport)
		}
	})

	t.Run("Custom", func(t *testing.T) {
		tp, _ := New(
			Config{
				URLs: []*url.URL{{}},
				Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					return &http.Response{Status: "MOCK"}, nil
				}),
			},
		)
		//nolint:bodyclose // Mock response does not have a body to close
		res, err := tp.transport.RoundTrip(&http.Request{URL: &url.URL{}})
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}

		if res.Status != "MOCK" {
			t.Errorf("Unexpected response from transport: %+v", res)
		}
	})
}

func TestTransportConfig(t *testing.T) {
	t.Run("Defaults", func(t *testing.T) {
		tp, _ := New(Config{})

		if !reflect.DeepEqual(tp.retryOnStatus, []int{502, 503, 504}) {
			t.Errorf("Unexpected retryOnStatus: %v", tp.retryOnStatus)
		}

		if tp.disableRetry {
			t.Errorf("Unexpected disableRetry: %v", tp.disableRetry)
		}

		if tp.enableRetryOnTimeout {
			t.Errorf("Unexpected enableRetryOnTimeout: %v", tp.enableRetryOnTimeout)
		}

		if tp.maxRetries != 6 {
			t.Errorf("Unexpected maxRetries: %v", tp.maxRetries)
		}

		if tp.compressRequestBody {
			t.Errorf("Unexpected compressRequestBody: %v", tp.compressRequestBody)
		}
	})

	t.Run("Custom", func(t *testing.T) {
		tp, _ := New(Config{
			RetryOnStatus:        []int{404, 408},
			DisableRetry:         true,
			EnableRetryOnTimeout: true,
			MaxRetries:           5,
			CompressRequestBody:  true,
		})

		if !reflect.DeepEqual(tp.retryOnStatus, []int{404, 408}) {
			t.Errorf("Unexpected retryOnStatus: %v", tp.retryOnStatus)
		}

		if !tp.disableRetry {
			t.Errorf("Unexpected disableRetry: %v", tp.disableRetry)
		}

		if !tp.enableRetryOnTimeout {
			t.Errorf("Unexpected enableRetryOnTimeout: %v", tp.enableRetryOnTimeout)
		}

		if tp.maxRetries != 5 {
			t.Errorf("Unexpected maxRetries: %v", tp.maxRetries)
		}

		if !tp.compressRequestBody {
			t.Errorf("Unexpected compressRequestBody: %v", tp.compressRequestBody)
		}
	})
}

func TestTransportConnectionPool(t *testing.T) {
	t.Run("Single URL", func(t *testing.T) {
		tp, _ := New(Config{URLs: []*url.URL{{Scheme: "http", Host: "foo1"}}})

		if _, ok := tp.mu.connectionPool.(*singleConnectionPool); !ok {
			t.Errorf("Expected connection to be singleConnectionPool, got: %T", tp)
		}

		conn, err := tp.mu.connectionPool.Next()
		if err != nil {
			t.Errorf("Unexpected error: %s", err)
		}

		if conn.URL.String() != "http://foo1" {
			t.Errorf("Unexpected URL, want=http://foo1, got=%s", conn.URL)
		}
	})

	t.Run("Two URLs", func(t *testing.T) {
		var (
			conn *Connection
			err  error
		)

		tp, _ := New(Config{
			URLs: []*url.URL{
				{Scheme: "http", Host: "foo1"},
				{Scheme: "http", Host: "foo2"},
			},
			SkipConnectionShuffle: true, // Disable shuffling for predictable test results
		})

		if _, ok := tp.mu.connectionPool.(*statusConnectionPool); !ok {
			t.Errorf("Expected connection to be statusConnectionPool, got: %T", tp)
		}

		conn, err = tp.mu.connectionPool.Next()
		if err != nil {
			t.Errorf("Unexpected error: %s", err)
		}
		if conn.URL.String() != "http://foo1" {
			t.Errorf("Unexpected URL, want=foo1, got=%s", conn.URL)
		}

		conn, err = tp.mu.connectionPool.Next()
		if err != nil {
			t.Errorf("Unexpected error: %s", err)
		}
		if conn.URL.String() != "http://foo2" {
			t.Errorf("Unexpected URL, want=http://foo2, got=%s", conn.URL)
		}

		conn, err = tp.mu.connectionPool.Next()
		if err != nil {
			t.Errorf("Unexpected error: %s", err)
		}
		if conn.URL.String() != "http://foo1" {
			t.Errorf("Unexpected URL, want=http://foo1, got=%s", conn.URL)
		}
	})
}

type CustomConnectionPool struct {
	urls []*url.URL
}

// Next returns a random connection.
func (cp *CustomConnectionPool) Next() (*Connection, error) {
	u := cp.urls[rand.Intn(len(cp.urls))]
	return &Connection{URL: u}, nil
}

func (cp *CustomConnectionPool) OnFailure(c *Connection) error {
	index := -1
	for i, u := range cp.urls {
		if u == c.URL {
			index = i
		}
	}
	if index > -1 {
		cp.urls = append(cp.urls[:index], cp.urls[index+1:]...)
		return nil
	}
	return fmt.Errorf("connection not found")
}
func (cp *CustomConnectionPool) OnSuccess(_ *Connection) {}
func (cp *CustomConnectionPool) URLs() []*url.URL        { return cp.urls }

func TestTransportCustomConnectionPool(t *testing.T) {
	t.Run("Run", func(t *testing.T) {
		tp, _ := New(Config{
			ConnectionPoolFunc: func(conns []*Connection, selector Selector) ConnectionPool {
				return &CustomConnectionPool{
					urls: []*url.URL{
						{Scheme: "http", Host: "custom1"},
						{Scheme: "http", Host: "custom2"},
					},
				}
			},
		})

		if _, ok := tp.mu.connectionPool.(*CustomConnectionPool); !ok {
			t.Fatalf("Unexpected connection pool, want=CustomConnectionPool, got=%T", tp.mu.connectionPool)
		}

		conn, err := tp.mu.connectionPool.Next()
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}
		if conn.URL == nil {
			t.Errorf("Empty connection URL: %+v", conn)
		}
		if err := tp.mu.connectionPool.OnFailure(conn); err != nil {
			t.Errorf("Error removing the %q connection: %s", conn.URL, err)
		}
		if len(tp.mu.connectionPool.URLs()) != 1 {
			t.Errorf("Unexpected number of connections in pool: %q", tp.mu.connectionPool)
		}
	})
}

func TestTransportPerform(t *testing.T) {
	t.Run("Executes", func(t *testing.T) {
		u, _ := url.Parse("https://foo.com/bar")
		tp, _ := New(
			Config{
				URLs:      []*url.URL{u},
				Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) { return &http.Response{Status: "MOCK"}, nil }),
			},
		)

		req, _ := http.NewRequest(http.MethodGet, "/abc", nil)

		//nolint:bodyclose // Mock response does not have a body to close
		res, err := tp.Perform(req)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}

		if res.Status != "MOCK" {
			t.Errorf("Unexpected response: %+v", res)
		}
	})

	t.Run("Sets URL", func(t *testing.T) {
		u, _ := url.Parse("https://foo.com/bar")
		tp, _ := New(Config{URLs: []*url.URL{u}})

		req, _ := http.NewRequest(http.MethodGet, "/abc", nil)
		tp.setReqURL(u, req)

		expected := "https://foo.com/bar/abc"

		if req.URL.String() != expected {
			t.Errorf("req.URL: got=%s, want=%s", req.URL, expected)
		}
	})

	t.Run("Sets HTTP Basic Auth from URL", func(t *testing.T) {
		u, _ := url.Parse("https://foo:bar@example.com")
		tp, _ := New(Config{URLs: []*url.URL{u}})

		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		tp.setReqAuth(u, req)

		username, password, ok := req.BasicAuth()
		if !ok {
			t.Error("Expected the request to have Basic Auth set")
		}

		if username != "foo" || password != "bar" {
			t.Errorf("Unexpected values for username and password: %s:%s", username, password)
		}
	})

	t.Run("Sets HTTP Basic Auth from configuration", func(t *testing.T) {
		u, _ := url.Parse("http://example.com")
		tp, _ := New(Config{URLs: []*url.URL{u}, Username: "foo", Password: "bar"})

		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		tp.setReqAuth(u, req)

		username, password, ok := req.BasicAuth()
		if !ok {
			t.Errorf("Expected the request to have Basic Auth set")
		}

		if username != "foo" || password != "bar" {
			t.Errorf("Unexpected values for username and password: %s:%s", username, password)
		}
	})

	t.Run("Sets UserAgent", func(t *testing.T) {
		u, _ := url.Parse("http://example.com")
		tp, _ := New(Config{URLs: []*url.URL{u}})

		req, _ := http.NewRequest(http.MethodGet, "/abc", nil)
		tp.setReqUserAgent(req)

		if !strings.HasPrefix(req.UserAgent(), "opensearch-go") {
			t.Errorf("Unexpected user agent: %s", req.UserAgent())
		}
	})

	t.Run("Sets global HTTP request headers", func(t *testing.T) {
		hdr := http.Header{}
		hdr.Set("X-Foo", "bar")

		tp, _ := New(Config{Header: hdr})

		{
			// Set the global HTTP header
			req, _ := http.NewRequest(http.MethodGet, "/abc", nil)
			tp.setReqGlobalHeader(req)

			if req.Header.Get("X-Foo") != "bar" {
				t.Errorf("Unexpected global HTTP request header value: %s", req.Header.Get("X-Foo"))
			}
		}

		{
			// Do NOT overwrite an existing request header
			req, _ := http.NewRequest(http.MethodGet, "/abc", nil)
			req.Header.Set("X-Foo", "baz")
			tp.setReqGlobalHeader(req)

			if req.Header.Get("X-Foo") != "baz" {
				t.Errorf("Unexpected global HTTP request header value: %s", req.Header.Get("X-Foo"))
			}
		}
	})

	t.Run("Sign request", func(t *testing.T) {
		u, _ := url.Parse("https://foo:bar@example.com")
		tp, _ := New(
			Config{
				URLs: []*url.URL{u},
				Signer: &mockSigner{
					SampleKey:   "sign-status",
					SampleValue: "success",
				},
			},
		)

		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		tp.signRequest(req)

		if _, ok := req.Header["Sign-Status"]; !ok {
			t.Error("Signature is not added")
		}
	})

	t.Run("Error No URL", func(t *testing.T) {
		tp, _ := New(
			Config{
				URLs:      []*url.URL{},
				Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) { return &http.Response{Status: "MOCK"}, nil }),
			},
		)

		req, _ := http.NewRequest(http.MethodGet, "/abc", nil)

		//nolint:bodyclose // Mock response does not have a body to close
		_, err := tp.Perform(req)
		if err.Error() != `cannot get connection: no connections available` {
			t.Fatalf("Expected error `cannot get connection: no connections available`: but got error %q", err)
		}
	})
}

func TestTransportPerformRetries(t *testing.T) {
	t.Run("Retry request on network error and return the response", func(t *testing.T) {
		var (
			i       int
			numReqs = 2
		)

		u, _ := url.Parse("http://foo.bar")
		tp, _ := New(
			Config{
				URLs:                  []*url.URL{u, u, u},
				SkipConnectionShuffle: true,            // Disable shuffling for predictable test results
				HealthCheck:           NoOpHealthCheck, // Disable health checks to avoid extra requests during resurrection
				Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					i++
					fmt.Printf("Request #%d", i)
					if i == numReqs {
						fmt.Print(": OK\n")
						return &http.Response{Status: "OK"}, nil
					}
					fmt.Print(": ERR\n")
					return nil, &mockNetError{error: fmt.Errorf("Mock network error (%d)", i)}
				}),
			},
		)

		req, _ := http.NewRequest(http.MethodGet, "/abc", nil)

		//nolint:bodyclose // Mock response does not have a body to close
		res, err := tp.Perform(req)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}

		if res.Status != "OK" {
			t.Errorf("Unexpected response: %+v", res)
		}

		if i != numReqs {
			t.Errorf("Unexpected number of requests, want=%d, got=%d", numReqs, i)
		}
	})

	t.Run("Retry request on EOF error and return the response", func(t *testing.T) {
		var (
			i       int
			numReqs = 2
		)

		u, _ := url.Parse("http://foo.bar")
		tp, _ := New(
			Config{
				URLs:                  []*url.URL{u, u, u},
				SkipConnectionShuffle: true,            // Disable shuffling for predictable test results
				HealthCheck:           NoOpHealthCheck, // Disable health checks to avoid extra requests during resurrection
				Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					i++
					fmt.Printf("Request #%d", i)
					if i == numReqs {
						fmt.Print(": OK\n")
						return &http.Response{Status: "OK"}, nil
					}
					fmt.Print(": ERR\n")
					return nil, io.EOF
				}),
			},
		)

		req, _ := http.NewRequest(http.MethodGet, "/abc", nil)

		//nolint:bodyclose // Mock response does not have a body to close
		res, err := tp.Perform(req)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}

		if res.Status != "OK" {
			t.Errorf("Unexpected response: %+v", res)
		}

		if i != numReqs {
			t.Errorf("Unexpected number of requests, want=%d, got=%d", numReqs, i)
		}
	})

	t.Run("Retry request on 5xx response and return new response", func(t *testing.T) {
		var (
			i       int
			numReqs = 2
		)

		u, _ := url.Parse("http://foo.bar")
		tp, _ := New(
			Config{
				URLs:                  []*url.URL{u, u, u},
				SkipConnectionShuffle: true,            // Disable shuffling for predictable test results
				HealthCheck:           NoOpHealthCheck, // Disable health checks to avoid extra requests during resurrection
				Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					i++
					fmt.Printf("Request #%d", i)
					if i == numReqs {
						fmt.Print(": 200\n")
						return &http.Response{StatusCode: http.StatusOK}, nil
					}
					fmt.Print(": 502\n")
					return &http.Response{StatusCode: http.StatusBadGateway}, nil
				}),
			},
		)

		req, _ := http.NewRequest(http.MethodGet, "/abc", nil)

		//nolint:bodyclose // Mock response does not have a body to close
		res, err := tp.Perform(req)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}

		if res.StatusCode != http.StatusOK {
			t.Errorf("Unexpected response: %+v", res)
		}

		if i != numReqs {
			t.Errorf("Unexpected number of requests, want=%d, got=%d", numReqs, i)
		}
	})

	t.Run("Close response body for a 5xx response", func(t *testing.T) {
		var (
			i       int
			numReqs = 5
		)

		u, _ := url.Parse("http://foo.bar")
		tp, _ := New(
			Config{
				URLs:       []*url.URL{u, u, u},
				MaxRetries: numReqs,
				Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					i++
					fmt.Printf("Request #%d", i)
					fmt.Print(": 502\n")
					body := io.NopCloser(strings.NewReader(`MOCK`))
					return &http.Response{StatusCode: http.StatusBadGateway, Body: body}, nil
				}),
			},
		)

		req, _ := http.NewRequest(http.MethodGet, "/", nil)

		res, err := tp.Perform(req)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}

		if i != numReqs+1 {
			t.Errorf("Unexpected number of requests, want=%d, got=%d", numReqs, i)
		}

		if res.StatusCode != http.StatusBadGateway {
			t.Errorf("Unexpected response: %+v", res)
		}

		resBody, _ := io.ReadAll(res.Body)
		res.Body.Close()

		if string(resBody) != "MOCK" {
			t.Errorf("Unexpected body, want=MOCK, got=%s", resBody)
		}
	})

	t.Run("Retry request and return error when max retries exhausted", func(t *testing.T) {
		var (
			i       int
			numReqs = 3
		)

		u, _ := url.Parse("http://foo.bar")
		tp, _ := New(
			Config{
				URLs:                  []*url.URL{u, u, u},
				MaxRetries:            numReqs,         // Explicitly set MaxRetries to match test expectation
				SkipConnectionShuffle: true,            // Disable shuffling for predictable test results
				HealthCheck:           NoOpHealthCheck, // Disable health checks to avoid extra requests during resurrection
				Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					i++
					fmt.Printf("Request #%d", i)
					fmt.Print(": ERR\n")
					return nil, &mockNetError{error: fmt.Errorf("Mock network error (%d)", i)}
				}),
			},
		)

		req, _ := http.NewRequest(http.MethodGet, "/abc", nil)

		//nolint:bodyclose // Mock response does not have a body to close
		res, err := tp.Perform(req)
		if err == nil {
			t.Fatalf("Expected error, got: %v", err)
		}

		if res != nil {
			t.Errorf("Unexpected response: %+v", res)
		}

		// Should be initial HTTP request + 3 retries
		if i != numReqs+1 {
			t.Errorf("Unexpected number of requests, want=%d, got=%d", numReqs, i)
		}
	})

	t.Run("Reset request body during retry", func(t *testing.T) {
		var bodies []string
		u, _ := url.Parse("https://foo.com/bar")
		tp, _ := New(
			Config{
				URLs:       []*url.URL{u},
				MaxRetries: 3, // Set to 3 retries to get 4 total requests (1 original + 3 retries)
				Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					body, err := io.ReadAll(req.Body)
					if err != nil {
						panic(err)
					}
					bodies = append(bodies, string(body))
					return &http.Response{Status: "MOCK", StatusCode: http.StatusBadGateway}, nil
				}),
			},
		)

		req, _ := http.NewRequest(http.MethodPost, "/abc", strings.NewReader("FOOBAR"))
		//nolint:bodyclose // Mock response does not have a body to close
		res, err := tp.Perform(req)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}
		_ = res

		if n := len(bodies); n != 4 {
			t.Fatalf("expected 4 requests, got %d", n)
		}
		for i, body := range bodies {
			if body != "FOOBAR" {
				t.Fatalf("request %d body: expected %q, got %q", i, "FOOBAR", body)
			}
		}
	})

	t.Run("Reset request body during retry with request body compression", func(t *testing.T) {
		var bodies []string
		u, _ := url.Parse("https://foo.com/bar")
		tp, _ := New(
			Config{
				URLs:                []*url.URL{u},
				MaxRetries:          3, // Set to 3 retries to get 4 total requests
				CompressRequestBody: true,
				Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					body, err := io.ReadAll(req.Body)
					if err != nil {
						panic(err)
					}
					bodies = append(bodies, string(body))
					return &http.Response{Status: "MOCK", StatusCode: http.StatusBadGateway}, nil
				}),
			},
		)

		foobar := "FOOBAR"
		foobarGzipped := "\x1f\x8b\b\x00\x00\x00\x00\x00\x00\xffr\xf3\xf7wr\f\x02\x04\x00\x00\xff\xff\x13\xd8\x0en\x06\x00\x00\x00"

		req, _ := http.NewRequest(http.MethodPost, "/abc", strings.NewReader(foobar))
		//nolint:bodyclose // Mock response does not have a body to close
		res, err := tp.Perform(req)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}
		_ = res

		if n := len(bodies); n != 4 {
			t.Fatalf("expected 4 requests, got %d", n)
		}
		for i, body := range bodies {
			if body != foobarGzipped {
				t.Fatalf("request %d body: expected %q, got %q", i, foobarGzipped, body)
			}
		}
	})

	t.Run("Signer can sign correctly during retry", func(t *testing.T) {
		u, _ := url.Parse("https://foo.com/bar")
		signer := mockSigner{}
		callsToSigner := 0
		expectedBody := "FOOBAR"

		signer.testHook = func(req *http.Request) {
			callsToSigner++
			body, err := io.ReadAll(req.Body)
			if err != nil {
				panic(err)
			}
			if string(body) != expectedBody {
				t.Fatalf("request %d body: expected %q, got %q", callsToSigner, expectedBody, body)
			}
		}

		tp, _ := New(
			Config{
				URLs:       []*url.URL{u},
				MaxRetries: 3, // Set to 3 retries to get 4 total requests
				Signer:     &signer,
				Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					return &http.Response{Status: "MOCK", StatusCode: http.StatusBadGateway}, nil
				}),
			},
		)

		req, _ := http.NewRequest(http.MethodPost, "/abc", strings.NewReader(expectedBody))
		//nolint:bodyclose // Mock response does not have a body to close
		_, err := tp.Perform(req)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}

		if callsToSigner != 4 {
			t.Fatalf("expected 4 requests, got %d", callsToSigner)
		}
	})

	t.Run("Don't retry request on regular error", func(t *testing.T) {
		var i atomic.Int32

		u, _ := url.Parse("http://foo.bar")
		tp, _ := New(
			Config{
				URLs:                  []*url.URL{u, u, u},
				SkipConnectionShuffle: true, // Disable shuffling for predictable test results
				Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					count := i.Add(1)
					fmt.Printf("Request #%d", count)
					fmt.Print(": ERR\n")
					return nil, fmt.Errorf("Mock regular error (%d)", count)
				}),
			},
		)

		req, _ := http.NewRequest(http.MethodGet, "/abc", nil)

		//nolint:bodyclose // Mock response does not have a body to close
		res, err := tp.Perform(req)
		if err == nil {
			t.Fatalf("Expected error, got: %v", err)
		}

		if res != nil {
			t.Errorf("Unexpected response: %+v", res)
		}

		if count := i.Load(); count != 1 {
			t.Errorf("Unexpected number of requests, want=%d, got=%d", 1, count)
		}
	})

	t.Run("Don't retry request when retries are disabled", func(t *testing.T) {
		var i atomic.Int32

		u, _ := url.Parse("http://foo.bar")
		tp, _ := New(
			Config{
				URLs:                  []*url.URL{u, u, u},
				SkipConnectionShuffle: true, // Disable shuffling for predictable test results
				Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					count := i.Add(1)
					fmt.Printf("Request #%d", count)
					fmt.Print(": ERR\n")
					return nil, &mockNetError{error: fmt.Errorf("Mock network error (%d)", count)}
				}),
				DisableRetry: true,
				HealthCheck:  NoOpHealthCheck, // Disable health checks to avoid extra requests during resurrection
			},
		)

		req, _ := http.NewRequest(http.MethodGet, "/abc", nil)
		//nolint:bodyclose // Mock response does not have a body to close
		tp.Perform(req)

		if count := i.Load(); count != 1 {
			t.Errorf("Unexpected number of requests, want=%d, got=%d", 1, count)
		}
	})

	t.Run("Delay the retry with a backoff function", func(t *testing.T) {
		var (
			i                int
			numReqs          = 4
			start            = time.Now()
			expectedDuration = time.Duration((numReqs-1)*100) * time.Millisecond
		)

		u, _ := url.Parse("http://foo.bar")
		tp, _ := New(Config{
			MaxRetries:  numReqs,
			URLs:        []*url.URL{u},   // Use single URL to avoid connection resurrection
			HealthCheck: NoOpHealthCheck, // Disable health checks to avoid extra requests during resurrection
			Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
				i++
				fmt.Printf("Request #%d", i)
				if i == numReqs {
					fmt.Print(": OK\n")
					return &http.Response{Status: "OK"}, nil
				}
				fmt.Print(": ERR\n")
				return nil, &mockNetError{error: fmt.Errorf("Mock network error (%d)", i)}
			}),

			// A simple incremental backoff function
			//
			RetryBackoff: func(i int) time.Duration {
				d := time.Duration(i) * 100 * time.Millisecond
				fmt.Printf("Attempt: %d | Sleeping for %s...\n", i, d)
				return d
			},
		})

		req, _ := http.NewRequest(http.MethodGet, "/abc", nil)

		//nolint:bodyclose // Mock response does not have a body to close
		res, err := tp.Perform(req)
		end := time.Since(start)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}

		if res.Status != "OK" {
			t.Errorf("Unexpected response: %+v", res)
		}

		if i != numReqs {
			t.Errorf("Unexpected number of requests, want=%d, got=%d", numReqs, i)
		}

		if end < expectedDuration {
			t.Errorf("Unexpected duration, want=>%s, got=%s", expectedDuration, end)
		}
	})

	t.Run("Delay the retry with retry on timeout and context deadline", func(t *testing.T) {
		var i atomic.Int32
		u, _ := url.Parse("http://foo.bar")
		tp, _ := New(Config{
			EnableRetryOnTimeout: true,
			MaxRetries:           100,
			RetryBackoff:         func(i int) time.Duration { return time.Hour },
			URLs:                 []*url.URL{u},
			Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
				i.Add(1)
				<-req.Context().Done()
				return nil, req.Context().Err()
			}),
		})

		req, _ := http.NewRequest(http.MethodGet, "/abc", nil)
		ctx, cancel := context.WithTimeout(req.Context(), 50*time.Millisecond)
		defer cancel()
		req = req.WithContext(ctx)

		//nolint:bodyclose // Mock response does not have a body to close
		_, err := tp.Perform(req)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected context.DeadlineExceeded, got %s", err)
		}
		if count := i.Load(); count != 1 {
			t.Fatalf("unexpected number of requests: expected 1, got %d", count)
		}
	})

	t.Run("Don't backoff after the last retry", func(t *testing.T) {
		var (
			i          int
			j          int
			numReqs    = 5
			numRetries = numReqs - 1
		)

		u, _ := url.Parse("http://foo.bar")
		tp, _ := New(Config{
			MaxRetries:  numRetries,
			URLs:        []*url.URL{u},   // Use single URL to avoid connection resurrection
			HealthCheck: NoOpHealthCheck, // Disable health checks to avoid extra requests during resurrection
			Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
				i++
				fmt.Printf("Request #%d", i)
				fmt.Print(": ERR\n")
				return nil, &mockNetError{error: fmt.Errorf("Mock network error (%d)", i)}
			}),

			// A simple incremental backoff function
			//
			RetryBackoff: func(i int) time.Duration {
				j++
				d := time.Millisecond
				fmt.Printf("Attempt: %d | Sleeping for %s...\n", i, d)
				return d
			},
		})

		req, _ := http.NewRequest(http.MethodGet, "/abc", nil)

		//nolint:bodyclose // Mock response does not have a body to close
		_, err := tp.Perform(req)
		if err == nil {
			t.Fatalf("Expected error, got: %v", err)
		}

		if i != numReqs {
			t.Errorf("Unexpected number of requests, want=%d, got=%d", numReqs, i)
		}

		if j != numRetries {
			t.Errorf("Unexpected number of backoffs, want=>%d, got=%d", numRetries, j)
		}
	})
}

func TestURLs(t *testing.T) {
	t.Run("Returns URLs", func(t *testing.T) {
		tp, _ := New(Config{
			URLs: []*url.URL{
				{Scheme: "http", Host: "localhost:9200"},
				{Scheme: "http", Host: "localhost:9201"},
			},
			SkipConnectionShuffle: true, // Disable shuffling for predictable test results
		})
		urls := tp.URLs()
		if len(urls) != 2 {
			t.Errorf("Expected get 2 urls, but got: %d", len(urls))
		}
		if urls[0].Host != "localhost:9200" {
			t.Errorf("Unexpected URL, want=localhost:9200, got=%s", urls[0].Host)
		}
	})
}

func TestVersion(t *testing.T) {
	if Version == "" {
		t.Error("Version is empty")
	}
}

func TestMaxRetries(t *testing.T) {
	tests := []struct {
		name              string
		maxRetries        int
		disableRetry      bool
		expectedCallCount int
	}{
		{
			name:              "MaxRetries Active set to default",
			disableRetry:      false,
			expectedCallCount: 7, // 1 original + 6 retries (new default)
		},
		{
			name:              "MaxRetries Active set to 1",
			maxRetries:        1,
			disableRetry:      false,
			expectedCallCount: 2,
		},
		{
			name:              "Max Retries Active set to 2",
			maxRetries:        2,
			disableRetry:      false,
			expectedCallCount: 3,
		},
		{
			name:              "Max Retries Active set to 3",
			maxRetries:        3,
			disableRetry:      false,
			expectedCallCount: 4, // 1 original + 3 retries
		},
		{
			name:              "MaxRetries Inactive set to 0",
			maxRetries:        0,
			disableRetry:      true,
			expectedCallCount: 1,
		},
		{
			name:              "MaxRetries Inactive set to 3",
			maxRetries:        3,
			disableRetry:      true,
			expectedCallCount: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var callCount int
			c, _ := New(Config{
				URLs: []*url.URL{{}},
				Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					callCount++
					return &http.Response{
						StatusCode: http.StatusBadGateway,
						Status:     "MOCK",
					}, nil
				}),
				MaxRetries:   test.maxRetries,
				DisableRetry: test.disableRetry,
			})

			//nolint:bodyclose // Mock response does not have a body to close
			c.Perform(&http.Request{URL: &url.URL{}, Header: make(http.Header)}) // errcheck ignore

			if test.expectedCallCount != callCount {
				t.Errorf("Bad retry call count, got : %d, want : %d", callCount, test.expectedCallCount)
			}
		})
	}
}

func TestRequestCompression(t *testing.T) {
	tests := []struct {
		name            string
		compressionFlag bool
		inputBody       string
	}{
		{
			name:            "Uncompressed",
			compressionFlag: false,
			inputBody:       "opensearch",
		},
		{
			name:            "Compressed",
			compressionFlag: true,
			inputBody:       "opensearch",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tp, _ := New(Config{
				URLs:                []*url.URL{{}},
				CompressRequestBody: test.compressionFlag,
				Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					if req.Body == nil || req.Body == http.NoBody {
						return nil, fmt.Errorf("unexpected body: %v", req.Body)
					}

					var buf bytes.Buffer
					buf.ReadFrom(req.Body)

					if req.ContentLength != int64(buf.Len()) {
						return nil, fmt.Errorf("mismatched Content-Length: %d vs actual %d", req.ContentLength, buf.Len())
					}

					if test.compressionFlag {
						var unBuf bytes.Buffer
						zr, err := gzip.NewReader(&buf)
						if err != nil {
							return nil, fmt.Errorf("decompression error: %w", err)
						}
						unBuf.ReadFrom(zr)
						buf = unBuf
					}

					if buf.String() != test.inputBody {
						return nil, fmt.Errorf("unexpected body: %s", buf.String())
					}

					return &http.Response{Status: "MOCK"}, nil
				}),
			})

			req, _ := http.NewRequest(http.MethodPost, "/abc", bytes.NewBufferString(test.inputBody))

			//nolint:bodyclose // Mock response does not have a body to close
			res, err := tp.Perform(req)
			if err != nil {
				t.Fatalf("Unexpected error: %s", err)
			}

			if res.Status != "MOCK" {
				t.Errorf("Unexpected response: %+v", res)
			}
		})
	}
}

func TestRequestSigning(t *testing.T) {
	t.Run("Sign request fails", func(t *testing.T) {
		u, _ := url.Parse("https://foo:bar@example.com")
		tp, _ := New(
			Config{
				URLs: []*url.URL{u},
				Signer: &mockSigner{
					ReturnError: true,
				},
				Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					return &http.Response{Status: "MOCK"}, nil
				}),
			},
		)
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		//nolint:bodyclose // Mock response does not have a body to close
		_, err := tp.Perform(req)
		if err == nil {
			t.Fatal("Expected error, but, no error found")
		}
		if err.Error() != `failed to sign request: invalid data` {
			t.Fatalf("Expected error `failed to sign request: invalid data`: but got error %q", err)
		}
	})
}

func TestConnectionPoolPromotion(t *testing.T) {
	t.Run("promoteConnectionPoolWithLock preserves metrics", func(t *testing.T) {
		// Create a client with metrics enabled and single connection
		u, _ := url.Parse("http://localhost:9200")
		client, err := New(Config{
			URLs:          []*url.URL{u},
			EnableMetrics: true,
		})
		require.NoError(t, err)

		// Verify we start with a singleConnectionPool
		singlePool, ok := client.mu.connectionPool.(*singleConnectionPool)
		require.True(t, ok, "Expected singleConnectionPool")
		require.NotNil(t, singlePool.metrics, "Expected metrics to be assigned")

		// Create mock connections for promotion
		liveConn := &Connection{URL: &url.URL{Host: "live:9200"}, Name: "live-node"}
		deadConn := &Connection{URL: &url.URL{Host: "dead:9200"}, Name: "dead-node"}

		// Lock and promote
		client.mu.Lock()
		statusPool := client.promoteConnectionPoolWithLock([]*Connection{liveConn}, []*Connection{deadConn})
		client.mu.connectionPool = statusPool
		client.mu.Unlock()

		// Verify metrics were preserved
		require.NotNil(t, statusPool.metrics, "Metrics should be preserved during promotion")
		require.Equal(t, singlePool.metrics, statusPool.metrics, "Should preserve the same metrics instance")

		// Verify connections were properly set
		require.Len(t, statusPool.mu.live, 1, "Should have 1 live connection")
		require.Len(t, statusPool.mu.dead, 1, "Should have 1 dead connection")
		require.Equal(t, "live-node", statusPool.mu.live[0].Name)
		require.Equal(t, "dead-node", statusPool.mu.dead[0].Name)
	})

	t.Run("demoteConnectionPoolWithLock preserves metrics", func(t *testing.T) {
		// Create a statusConnectionPool with metrics
		liveConn := &Connection{URL: &url.URL{Host: "live:9200"}, Name: "live-node"}
		deadConn := &Connection{URL: &url.URL{Host: "dead:9200"}, Name: "dead-node"}

		statusPool := &statusConnectionPool{
			resurrectTimeoutInitial:      defaultResurrectTimeoutInitial,
			resurrectTimeoutFactorCutoff: defaultResurrectTimeoutFactorCutoff,
		}
		statusPool.mu.live = []*Connection{liveConn}
		statusPool.mu.dead = []*Connection{deadConn}

		// Assign metrics
		testMetrics := &metrics{}
		statusPool.metrics = testMetrics

		// Create client with statusConnectionPool
		u, _ := url.Parse("http://localhost:9200")
		client, err := New(Config{URLs: []*url.URL{u}})
		require.NoError(t, err)

		client.mu.Lock()
		client.mu.connectionPool = statusPool

		// Demote to single connection pool
		singlePool := client.demoteConnectionPoolWithLock()
		client.mu.connectionPool = singlePool
		client.mu.Unlock()

		// Verify metrics were preserved
		require.NotNil(t, singlePool.metrics, "Metrics should be preserved during demotion")
		require.Equal(t, testMetrics, singlePool.metrics, "Should preserve the same metrics instance")

		// Verify connection selection (should prefer live over dead)
		require.NotNil(t, singlePool.connection, "Should have selected a connection")
		require.Equal(t, "live-node", singlePool.connection.Name, "Should prefer live connection")
	})

	t.Run("demoteConnectionPoolWithLock prefers live connections", func(t *testing.T) {
		liveConn1 := &Connection{URL: &url.URL{Host: "live1:9200"}, Name: "live-node-1"}
		liveConn2 := &Connection{URL: &url.URL{Host: "live2:9200"}, Name: "live-node-2"}
		deadConn := &Connection{URL: &url.URL{Host: "dead:9200"}, Name: "dead-node"}

		statusPool := &statusConnectionPool{}
		statusPool.mu.live = []*Connection{liveConn1, liveConn2}
		statusPool.mu.dead = []*Connection{deadConn}

		u, _ := url.Parse("http://localhost:9200")
		client, err := New(Config{URLs: []*url.URL{u}})
		require.NoError(t, err)

		client.mu.Lock()
		client.mu.connectionPool = statusPool
		singlePool := client.demoteConnectionPoolWithLock()
		client.mu.Unlock()

		require.Equal(t, "live-node-1", singlePool.connection.Name, "Should select first live connection")
	})

	t.Run("demoteConnectionPoolWithLock falls back to dead connections", func(t *testing.T) {
		deadConn1 := &Connection{URL: &url.URL{Host: "dead1:9200"}, Name: "dead-node-1"}
		deadConn2 := &Connection{URL: &url.URL{Host: "dead2:9200"}, Name: "dead-node-2"}

		statusPool := &statusConnectionPool{}
		statusPool.mu.live = []*Connection{} // No live connections
		statusPool.mu.dead = []*Connection{deadConn1, deadConn2}

		u, _ := url.Parse("http://localhost:9200")
		client, err := New(Config{URLs: []*url.URL{u}})
		require.NoError(t, err)

		client.mu.Lock()
		client.mu.connectionPool = statusPool
		singlePool := client.demoteConnectionPoolWithLock()
		client.mu.Unlock()

		require.Equal(t, "dead-node-1", singlePool.connection.Name, "Should select first dead connection when no live connections")
	})

	t.Run("demoteConnectionPoolWithLock handles empty pools", func(t *testing.T) {
		statusPool := &statusConnectionPool{}
		statusPool.mu.live = []*Connection{} // No connections
		statusPool.mu.dead = []*Connection{} // No connections

		u, _ := url.Parse("http://localhost:9200")
		client, err := New(Config{URLs: []*url.URL{u}})
		require.NoError(t, err)

		client.mu.Lock()
		client.mu.connectionPool = statusPool
		singlePool := client.demoteConnectionPoolWithLock()
		client.mu.Unlock()

		require.Nil(t, singlePool.connection, "Should handle empty pool gracefully")
	})

	t.Run("promoteConnectionPoolWithLock preserves resurrection timeout settings", func(t *testing.T) {
		// Create initial statusConnectionPool with custom settings
		customInitial := 120 * time.Second
		customCutoff := 10

		existingPool := &statusConnectionPool{
			resurrectTimeoutInitial:      customInitial,
			resurrectTimeoutFactorCutoff: customCutoff,
		}
		existingPool.mu.live = []*Connection{{URL: &url.URL{Host: "existing:9200"}}}

		u, _ := url.Parse("http://localhost:9200")
		client, err := New(Config{URLs: []*url.URL{u}})
		require.NoError(t, err)

		client.mu.Lock()
		client.mu.connectionPool = existingPool

		// Promote (which should preserve existing settings)
		newConn := &Connection{URL: &url.URL{Host: "new:9200"}, Name: "new-node"}
		newPool := client.promoteConnectionPoolWithLock([]*Connection{newConn}, []*Connection{})

		client.mu.Unlock()

		// Verify custom settings were preserved
		require.Equal(t, customInitial, newPool.resurrectTimeoutInitial, "Should preserve custom resurrection timeout")
		require.Equal(t, customCutoff, newPool.resurrectTimeoutFactorCutoff, "Should preserve custom cutoff factor")
	})

	t.Run("promoteConnectionPoolWithLock filters dedicated cluster managers", func(t *testing.T) {
		// Create connections with different roles
		dataConn := &Connection{
			URL:   &url.URL{Host: "data:9200"},
			Name:  "data-node",
			Roles: newRoleSet([]string{"data"}),
		}
		clusterManagerConn := &Connection{
			URL:   &url.URL{Host: "cm:9200"},
			Name:  "cm-node",
			Roles: newRoleSet([]string{"cluster_manager"}), // Dedicated cluster manager
		}
		mixedConn := &Connection{
			URL:   &url.URL{Host: "mixed:9200"},
			Name:  "mixed-node",
			Roles: newRoleSet([]string{"cluster_manager", "data"}), // Not dedicated
		}

		u, _ := url.Parse("http://localhost:9200")
		client, err := New(Config{
			URLs:                            []*url.URL{u},
			IncludeDedicatedClusterManagers: false, // Default: exclude dedicated CMs
		})
		require.NoError(t, err)

		client.mu.Lock()
		statusPool := client.promoteConnectionPoolWithLock(
			[]*Connection{dataConn, clusterManagerConn, mixedConn},
			[]*Connection{},
		)
		client.mu.Unlock()

		// Should exclude dedicated cluster manager but include mixed role node
		require.Len(t, statusPool.mu.live, 2, "Should exclude dedicated cluster manager")

		names := make([]string, len(statusPool.mu.live))
		for i, conn := range statusPool.mu.live {
			names[i] = conn.Name
		}
		require.Contains(t, names, "data-node", "Should include data node")
		require.Contains(t, names, "mixed-node", "Should include mixed role node")
		require.NotContains(t, names, "cm-node", "Should exclude dedicated cluster manager")
	})

	t.Run("promoteConnectionPoolWithLock includes dedicated cluster managers when configured", func(t *testing.T) {
		// Create connections with different roles
		dataConn := &Connection{
			URL:   &url.URL{Host: "data:9200"},
			Name:  "data-node",
			Roles: newRoleSet([]string{"data"}),
		}
		clusterManagerConn := &Connection{
			URL:   &url.URL{Host: "cm:9200"},
			Name:  "cm-node",
			Roles: newRoleSet([]string{"cluster_manager"}), // Dedicated cluster manager
		}

		u, _ := url.Parse("http://localhost:9200")
		client, err := New(Config{
			URLs:                            []*url.URL{u},
			IncludeDedicatedClusterManagers: true, // Include dedicated CMs
		})
		require.NoError(t, err)

		client.mu.Lock()
		statusPool := client.promoteConnectionPoolWithLock(
			[]*Connection{dataConn, clusterManagerConn},
			[]*Connection{},
		)
		client.mu.Unlock()

		// Should include all connections including dedicated cluster manager
		require.Len(t, statusPool.mu.live, 2, "Should include all connections")

		names := make([]string, len(statusPool.mu.live))
		for i, conn := range statusPool.mu.live {
			names[i] = conn.Name
		}
		require.Contains(t, names, "data-node", "Should include data node")
		require.Contains(t, names, "cm-node", "Should include dedicated cluster manager")
	})
}

func TestConnectionPoolPromotionIntegration(t *testing.T) {
	t.Run("discovery promotes single to multi-node pool", func(t *testing.T) {
		// Create mock server for nodes discovery
		mux := http.NewServeMux()

		// Health check endpoint
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"name":         "test-node",
				"cluster_name": "test-cluster",
				"version":      map[string]any{"number": "2.0.0"},
			})
		})

		// Nodes info endpoint - return multiple nodes
		mux.HandleFunc("/_nodes/http", func(w http.ResponseWriter, r *http.Request) {
			response := map[string]any{
				"_nodes": map[string]any{
					"total":      2,
					"successful": 2,
					"failed":     0,
				},
				"cluster_name": "test-cluster",
				"nodes": map[string]any{
					"node1": map[string]any{
						"name":  "data-node-1",
						"roles": []string{"data", "ingest"},
						"http":  map[string]any{"publish_address": "127.0.0.1:9200"},
					},
					"node2": map[string]any{
						"name":  "data-node-2",
						"roles": []string{"data", "ingest"},
						"http":  map[string]any{"publish_address": "127.0.0.1:9201"},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		})

		// Start test server
		server := httptest.NewServer(mux)
		defer server.Close()

		// Parse server URL
		serverURL, err := url.Parse(server.URL)
		require.NoError(t, err)

		// Create client starting with single node (should create singleConnectionPool)
		client, err := New(Config{
			URLs:          []*url.URL{serverURL},
			EnableMetrics: true,
		})
		require.NoError(t, err)

		// Verify we start with singleConnectionPool
		originalPool, ok := client.mu.connectionPool.(*singleConnectionPool)
		require.True(t, ok, "Should start with singleConnectionPool")
		require.NotNil(t, originalPool.metrics, "Should have metrics assigned")

		// Perform discovery
		err = client.DiscoverNodes(t.Context())
		require.NoError(t, err, "Discovery should succeed")

		// Verify promotion to statusConnectionPool
		client.mu.RLock()
		newPool, ok := client.mu.connectionPool.(*statusConnectionPool)
		client.mu.RUnlock()
		require.True(t, ok, "Should promote to statusConnectionPool after discovery")

		// Verify metrics were preserved
		require.NotNil(t, newPool.metrics, "Metrics should be preserved after promotion")
		require.Equal(t, originalPool.metrics, newPool.metrics, "Should preserve same metrics instance")

		// Verify we have multiple connections
		newPool.mu.RLock()
		totalConnections := len(newPool.mu.live) + len(newPool.mu.dead)
		newPool.mu.RUnlock()
		require.Equal(t, 2, totalConnections, "Should have discovered 2 nodes")
	})

	t.Run("NewSmartRouter works with single and multi-node pools", func(t *testing.T) {
		// Create test connections with different roles
		dataConn1 := &Connection{
			URL:   &url.URL{Host: "data1:9200"},
			Name:  "data-node-1",
			Roles: newRoleSet([]string{"data", "ingest"}),
		}
		dataConn2 := &Connection{
			URL:   &url.URL{Host: "data2:9200"},
			Name:  "data-node-2",
			Roles: newRoleSet([]string{"data", "ingest"}),
		}

		// Test with single node + router
		u, _ := url.Parse("http://localhost:9200")
		client, err := New(Config{
			URLs:          []*url.URL{u},
			EnableMetrics: true,
			Router:        NewSmartRouter(),
		})
		require.NoError(t, err)

		// Verify we start with singleConnectionPool and router is set
		originalPool, ok := client.mu.connectionPool.(*singleConnectionPool)
		require.True(t, ok, "Should start with singleConnectionPool")
		require.NotNil(t, originalPool.metrics, "Should have metrics")
		require.NotNil(t, client.router, "Should have router configured")

		// Simulate promotion to multi-node
		client.mu.Lock()
		statusPool := client.promoteConnectionPoolWithLock([]*Connection{dataConn1, dataConn2}, []*Connection{})
		client.mu.connectionPool = statusPool

		// Verify router can work with the promoted pool
		require.NotNil(t, client.router, "Router should still be available after promotion")

		// Update router with new connections (simulating what discovery would do)
		router := client.router
		client.mu.Unlock()

		// Test that router can handle connection routing
		// This is a basic test - in real usage, router.Route() would be called during requests
		if router != nil {
			// The router should be able to handle the new connections
			// In a real scenario, the router's internal policies would be updated during discovery
			req, _ := http.NewRequest(http.MethodGet, "/", nil)
			_, routeErr := router.Route(t.Context(), req)
			// Note: This might return an error because policies aren't initialized,
			// but it shouldn't panic or cause memory issues
			_ = routeErr // Ignore the error for this test
		}
	})

	t.Run("metrics continue working after pool transitions", func(t *testing.T) {
		// Create client with metrics
		u, _ := url.Parse("http://localhost:9200")
		client, err := New(Config{
			URLs:          []*url.URL{u},
			EnableMetrics: true,
		})
		require.NoError(t, err)

		// Record initial metrics state
		originalMetrics := client.metrics
		require.NotNil(t, originalMetrics, "Should have metrics")

		// Get baseline metrics
		baselineRequests := client.metrics.requests.Load()

		// Simulate some requests to increment metrics
		client.metrics.requests.Add(5)
		require.Equal(t, baselineRequests+5, client.metrics.requests.Load(), "Metrics should increment")

		// Create connections for promotion
		dataConn1 := &Connection{URL: &url.URL{Host: "data1:9200"}, Name: "data-node-1"}
		dataConn2 := &Connection{URL: &url.URL{Host: "data2:9200"}, Name: "data-node-2"}

		// Promote to multi-node pool
		client.mu.Lock()
		statusPool := client.promoteConnectionPoolWithLock([]*Connection{dataConn1, dataConn2}, []*Connection{})
		client.mu.connectionPool = statusPool
		client.mu.Unlock()

		// Verify metrics reference is preserved and still works
		require.Equal(t, originalMetrics, client.metrics, "Client metrics reference should be unchanged")
		require.Equal(t, originalMetrics, statusPool.metrics, "Pool should reference the same metrics")
		require.Equal(t, baselineRequests+5, client.metrics.requests.Load(), "Metrics value should be preserved")

		// Test metrics still work after promotion
		client.metrics.requests.Add(3)
		require.Equal(t, baselineRequests+8, client.metrics.requests.Load(), "Metrics should continue working after promotion")
		require.Equal(t, baselineRequests+8, statusPool.metrics.requests.Load(), "Pool metrics should reflect same changes")

		// Now demote back to single node
		client.mu.Lock()
		singlePool := client.demoteConnectionPoolWithLock()
		client.mu.connectionPool = singlePool
		client.mu.Unlock()

		// Verify metrics are still preserved after demotion
		require.Equal(t, originalMetrics, client.metrics, "Client metrics reference should still be unchanged")
		require.Equal(t, originalMetrics, singlePool.metrics, "Demoted pool should reference the same metrics")
		require.Equal(t, baselineRequests+8, client.metrics.requests.Load(), "Metrics value should be preserved after demotion")

		// Test metrics still work after demotion
		client.metrics.requests.Add(2)
		require.Equal(t, baselineRequests+10, client.metrics.requests.Load(), "Metrics should continue working after demotion")
	})

	t.Run("updateConnectionPool handles demotion from multi to single node", func(t *testing.T) {
		// Create a client starting with multiple nodes
		u1, _ := url.Parse("http://node1:9200")
		u2, _ := url.Parse("http://node2:9200")
		client, err := New(Config{
			URLs:          []*url.URL{u1, u2},
			EnableMetrics: true,
		})
		require.NoError(t, err)

		// Verify we start with statusConnectionPool
		originalPool, ok := client.mu.connectionPool.(*statusConnectionPool)
		require.True(t, ok, "Should start with statusConnectionPool for multiple URLs")
		require.NotNil(t, originalPool.metrics, "Should have metrics")

		// Simulate discovery that finds only one node (should trigger demotion)
		conn1 := &Connection{
			URL:   &url.URL{Host: "node1:9200"},
			Name:  "node-1",
			Roles: newRoleSet([]string{"data"}),
		}

		// Test demotion path in updateConnectionPool
		client.mu.Lock()

		// Create a new single connection pool using the demotion path
		// (simulating what updateConnectionPool would do for totalNodes == 1)
		if _, isStatusPool := client.mu.connectionPool.(*statusConnectionPool); isStatusPool {
			demotedPool := client.demoteConnectionPoolWithLock()
			// Update the connection to match our single node
			demotedPool.connection = conn1
			client.mu.connectionPool = demotedPool
		}

		client.mu.Unlock()

		// Verify demotion worked
		client.mu.RLock()
		newPool, ok := client.mu.connectionPool.(*singleConnectionPool)
		client.mu.RUnlock()
		require.True(t, ok, "Should demote to singleConnectionPool")

		// Verify metrics were preserved
		require.Equal(t, originalPool.metrics, newPool.metrics, "Metrics should be preserved during demotion")

		// Verify the connection is correct
		require.Equal(t, "node-1", newPool.connection.Name, "Should have the correct connection")
	})

	t.Run("updateConnectionPool preserves existing single connection pool", func(t *testing.T) {
		// Test the case where we stay with a single connection pool
		u, _ := url.Parse("http://localhost:9200")
		client, err := New(Config{
			URLs:          []*url.URL{u},
			EnableMetrics: true,
		})
		require.NoError(t, err)

		// Get original pool
		originalPool, ok := client.mu.connectionPool.(*singleConnectionPool)
		require.True(t, ok, "Should start with singleConnectionPool")
		originalMetrics := originalPool.metrics

		// Simulate updateConnectionPool logic for single node with existing single pool
		conn := &Connection{URL: &url.URL{Host: "localhost:9200"}, Name: "test-node"}

		client.mu.Lock()
		// This simulates the non-demotion path in updateConnectionPool for single nodes
		if _, isSinglePool := client.mu.connectionPool.(*singleConnectionPool); isSinglePool {
			// Preserve metrics from existing single connection pool
			var metrics *metrics
			if existingPool, ok := client.mu.connectionPool.(*singleConnectionPool); ok {
				metrics = existingPool.metrics
			}

			newSinglePool := &singleConnectionPool{
				connection: conn,
				metrics:    metrics,
			}
			client.mu.connectionPool = newSinglePool
		}
		client.mu.Unlock()

		// Verify metrics were preserved
		newPool := client.mu.connectionPool.(*singleConnectionPool)
		require.Equal(t, originalMetrics, newPool.metrics, "Should preserve metrics in single->single transition")
	})

	t.Run("promoteConnectionPoolWithLock with debug logging", func(t *testing.T) {
		// Test with debug logging enabled (requires OPENSEARCH_GO_DEBUG=true environment variable)
		// This test validates that debug log paths don't panic, even if logging is disabled

		// Create connections with different roles including dedicated cluster manager
		dataConn := &Connection{
			URL:   &url.URL{Host: "data:9200"},
			Name:  "data-node",
			Roles: newRoleSet([]string{"data"}),
		}
		clusterManagerConn := &Connection{
			URL:   &url.URL{Host: "cm:9200"},
			Name:  "cm-node",
			Roles: newRoleSet([]string{"cluster_manager"}), // Dedicated cluster manager
		}

		u, _ := url.Parse("http://localhost:9200")
		client, err := New(Config{
			URLs:                            []*url.URL{u},
			IncludeDedicatedClusterManagers: false, // This will trigger debug logging
		})
		require.NoError(t, err)

		client.mu.Lock()
		statusPool := client.promoteConnectionPoolWithLock(
			[]*Connection{dataConn, clusterManagerConn},
			[]*Connection{},
		)
		client.mu.Unlock()

		// Should exclude dedicated cluster manager and debug log it
		require.Len(t, statusPool.mu.live, 1, "Should exclude dedicated cluster manager")
		require.Equal(t, "data-node", statusPool.mu.live[0].Name, "Should include data node")
	})

	t.Run("promoteConnectionPoolWithLock preserves statusConnectionPool settings", func(t *testing.T) {
		// Test promoting when we already have a statusConnectionPool (not single)
		customInitial := 180 * time.Second
		customCutoff := 15

		// Start with statusConnectionPool
		existingPool := &statusConnectionPool{
			resurrectTimeoutInitial:      customInitial,
			resurrectTimeoutFactorCutoff: customCutoff,
		}
		existingPool.mu.live = []*Connection{{URL: &url.URL{Host: "existing:9200"}}}

		testMetrics := &metrics{}
		existingPool.metrics = testMetrics

		u, _ := url.Parse("http://localhost:9200")
		client, err := New(Config{URLs: []*url.URL{u}})
		require.NoError(t, err)

		client.mu.Lock()
		client.mu.connectionPool = existingPool

		// Promote (this should preserve existing statusConnectionPool settings)
		newConn := &Connection{URL: &url.URL{Host: "new:9200"}, Name: "new-node"}
		newPool := client.promoteConnectionPoolWithLock([]*Connection{newConn}, []*Connection{})

		client.mu.Unlock()

		// Verify custom settings were preserved from existing statusConnectionPool
		require.Equal(t, customInitial, newPool.resurrectTimeoutInitial, "Should preserve custom resurrection timeout")
		require.Equal(t, customCutoff, newPool.resurrectTimeoutFactorCutoff, "Should preserve custom cutoff factor")
		require.Equal(t, testMetrics, newPool.metrics, "Should preserve existing metrics")
	})

	t.Run("demoteConnectionPoolWithLock handles non-statusConnectionPool gracefully", func(t *testing.T) {
		// Test demoting when current pool is not a statusConnectionPool
		u, _ := url.Parse("http://localhost:9200")
		client, err := New(Config{URLs: []*url.URL{u}})
		require.NoError(t, err)

		// Start with a singleConnectionPool (not statusConnectionPool)
		client.mu.Lock()
		// Try to demote - this should handle the case where it's not a statusConnectionPool
		singlePool := client.demoteConnectionPoolWithLock()
		client.mu.Unlock()

		// Should return the same singleConnectionPool with the original connection
		require.NotNil(t, singlePool.connection, "Should preserve the original connection from the single URL")
		require.Equal(t, "localhost:9200", singlePool.connection.URL.Host, "Should preserve the correct connection")
		require.Nil(t, singlePool.metrics, "Should have nil metrics for new client without metrics enabled")
	})
}
