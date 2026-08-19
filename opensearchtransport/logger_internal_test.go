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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
)

var (
	_ = fmt.Print
	_ = os.Stdout
)

func TestTransportLogger(t *testing.T) {
	newRoundTripper := func() http.RoundTripper {
		return mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				Status:        fmt.Sprintf("%d %s", http.StatusOK, http.StatusText(http.StatusOK)),
				StatusCode:    http.StatusOK,
				ContentLength: 13,
				Header:        http.Header(map[string][]string{"Content-Type": {"application/json"}}),
				Body:          io.NopCloser(strings.NewReader(`{"foo":"bar"}`)),
			}, nil
		})
	}

	t.Run("Defaults", func(t *testing.T) {
		var wg sync.WaitGroup

		tp, _ := New(Config{
			URLs:              []*url.URL{{Scheme: "http", Host: "foo"}},
			NodeStatsInterval: -1,
			Transport:         newRoundTripper(),
			// Logger: io.Discard,
		})
		t.Cleanup(func() { _ = tp.Close() })

		for range 100 {
			wg.Go(func() {
				req, _ := http.NewRequest(http.MethodGet, "/abc", nil)
				resp, err := tp.Stream(req)
				if err != nil {
					t.Errorf("Unexpected error: %s", err)
					return
				}
				defer resp.Body.Close()
			})
		}
		wg.Wait()
	})

	t.Run("Nil", func(t *testing.T) {
		tp, _ := New(Config{
			URLs:              []*url.URL{{Scheme: "http", Host: "foo"}},
			NodeStatsInterval: -1,
			Transport:         newRoundTripper(),
			Logger:            nil,
		})
		t.Cleanup(func() { _ = tp.Close() })

		req, _ := http.NewRequest(http.MethodGet, "/abc", nil)
		resp, err := tp.Stream(req)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}
		defer resp.Body.Close()
	})

	t.Run("No HTTP response", func(t *testing.T) {
		tp, _ := New(Config{
			URLs:              []*url.URL{{Scheme: "http", Host: "foo"}},
			NodeStatsInterval: -1,
			Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
				return nil, errors.New("Mock error")
			}),
			Logger: &TextLogger{Output: io.Discard},
		})
		t.Cleanup(func() { _ = tp.Close() })

		req, _ := http.NewRequest(http.MethodGet, "/abc", nil)
		resp, err := tp.Stream(req)
		if err == nil {
			defer resp.Body.Close()
			t.Errorf("Expected error: %v", err)
		}
		if resp != nil {
			t.Errorf("Expected nil response, got: %v", err)
		}
	})

	t.Run("Keep response body", func(t *testing.T) {
		var dst strings.Builder

		tp, _ := New(Config{
			URLs:              []*url.URL{{Scheme: "http", Host: "foo"}},
			NodeStatsInterval: -1,
			Transport:         newRoundTripper(),
			Logger:            &TextLogger{Output: &dst, EnableRequestBody: true, EnableResponseBody: true},
		})
		t.Cleanup(func() { _ = tp.Close() })

		req, _ := http.NewRequest(http.MethodGet, "/abc?q=a,b", nil)
		req.Body = io.NopCloser(strings.NewReader(`{"query":"42"}`))

		res, err := tp.Stream(req)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}
		defer res.Body.Close()

		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("Error reading response body: %s", err)
		}

		if len(dst.String()) < 1 {
			t.Errorf("Log is empty: %#v", dst.String())
		}

		if len(body) < 1 {
			t.Fatalf("Body is empty: %#v", body)
		}
	})

	t.Run("Text with body", func(t *testing.T) {
		var dst strings.Builder

		tp, _ := New(Config{
			URLs:              []*url.URL{{Scheme: "http", Host: "foo"}},
			NodeStatsInterval: -1,
			Transport:         newRoundTripper(),
			Logger:            &TextLogger{Output: &dst, EnableRequestBody: true, EnableResponseBody: true},
		})
		t.Cleanup(func() { _ = tp.Close() })

		req, _ := http.NewRequest(http.MethodGet, "/abc?q=a,b", nil)
		req.Body = io.NopCloser(strings.NewReader(`{"query":"42"}`))

		res, err := tp.Stream(req)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}
		defer res.Body.Close()

		_, err = io.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("Error reading response body: %s", err)
		}

		output := dst.String()
		output = strings.TrimSuffix(output, "\n")
		// fmt.Println(output)

		lines := strings.Split(output, "\n")

		if len(lines) != 3 {
			t.Fatalf("Expected 3 lines, got %d", len(lines))
		}

		if !strings.Contains(lines[0], "GET http://foo/abc?q=a,b") {
			t.Errorf("Unexpected output: %s", lines[0])
		}

		if lines[1] != `> {"query":"42"}` {
			t.Errorf("Unexpected output: %s", lines[1])
		}

		if lines[2] != `< {"foo":"bar"}` {
			t.Errorf("Unexpected output: %s", lines[1])
		}
	})

	t.Run("Color with body", func(t *testing.T) {
		var dst strings.Builder

		tp, _ := New(Config{
			URLs:              []*url.URL{{Scheme: "http", Host: "foo"}},
			NodeStatsInterval: -1,
			Transport:         newRoundTripper(),
			Logger:            &ColorLogger{Output: &dst, EnableRequestBody: true, EnableResponseBody: true},
		})
		t.Cleanup(func() { _ = tp.Close() })

		req, _ := http.NewRequest(http.MethodGet, "/abc?q=a,b", nil)
		req.Body = io.NopCloser(strings.NewReader(`{"query":"42"}`))

		res, err := tp.Stream(req)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}
		defer res.Body.Close()

		_, err = io.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("Error reading response body: %s", err)
		}

		var output string
		stripANSI := regexp.MustCompile("(?sm)\x1b\\[.+?m([^\x1b]+?)|\x1b\\[0m")
		for v := range strings.SplitSeq(dst.String(), "\n") {
			if v != "" {
				output += stripANSI.ReplaceAllString(v, "$1")
				if !strings.HasSuffix(output, "\n") {
					output += "\n"
				}
			}
		}
		output = strings.TrimSuffix(output, "\n")
		// fmt.Println(output)

		lines := strings.Split(output, "\n")

		if len(lines) != 4 {
			t.Fatalf("Expected 4 lines, got %d", len(lines))
		}

		if !strings.Contains(lines[0], "GET http://foo/abc?q=a,b") {
			t.Errorf("Unexpected output: %s", lines[0])
		}

		if !strings.Contains(lines[1], `>> {"query":"42"}`) {
			t.Errorf("Unexpected output: %s", lines[1])
		}

		if !strings.Contains(lines[2], `<< {"foo":"bar"}`) {
			t.Errorf("Unexpected output: %s", lines[2])
		}
	})

	t.Run("Curl", func(t *testing.T) {
		var dst strings.Builder

		tp, _ := New(Config{
			URLs:              []*url.URL{{Scheme: "http", Host: "foo"}},
			NodeStatsInterval: -1,
			Transport:         newRoundTripper(),
			Logger:            &CurlLogger{Output: &dst, EnableRequestBody: true, EnableResponseBody: true},
		})
		t.Cleanup(func() { _ = tp.Close() })

		req, _ := http.NewRequest(http.MethodGet, "/abc?q=a,b", nil)
		req.Body = io.NopCloser(strings.NewReader(`{"query":"42"}`))

		res, err := tp.Stream(req)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}
		defer res.Body.Close()

		_, err = io.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("Error reading response body: %s", err)
		}

		output := dst.String()
		output = strings.TrimSuffix(output, "\n")

		lines := strings.Split(output, "\n")

		if len(lines) != 9 {
			t.Fatalf("Expected 9 lines, got %d", len(lines))
		}

		if !strings.Contains(lines[0], "curl -X GET 'http://foo/abc?pretty&q=a%2Cb'") {
			t.Errorf("Unexpected output: %s", lines[0])
		}
	})

	t.Run("JSON", func(t *testing.T) {
		var dst strings.Builder

		tp, _ := New(Config{
			URLs:              []*url.URL{{Scheme: "http", Host: "foo"}},
			NodeStatsInterval: -1,
			Transport:         newRoundTripper(),
			Logger:            &JSONLogger{Output: &dst},
		})
		t.Cleanup(func() { _ = tp.Close() })

		req, _ := http.NewRequest(http.MethodGet, "/abc?q=a,b", nil)
		req.Body = io.NopCloser(strings.NewReader(`{"query":"42"}`))
		resp, err := tp.Stream(req)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}
		defer resp.Body.Close()

		output := dst.String()
		output = strings.TrimSuffix(output, "\n")
		// fmt.Println(output)

		lines := strings.Split(output, "\n")

		if len(lines) != 1 {
			t.Fatalf("Expected 1 line, got %d", len(lines))
		}

		var j map[string]any
		if err := json.Unmarshal([]byte(output), &j); err != nil {
			t.Errorf("Error decoding JSON: %s", err)
		}

		domain := j["url"].(map[string]any)["domain"]
		if domain != "foo" {
			t.Errorf("Unexpected JSON output: %s", domain)
		}
	})

	t.Run("JSON with request body", func(t *testing.T) {
		var dst strings.Builder

		tp, _ := New(Config{
			URLs:              []*url.URL{{Scheme: "http", Host: "foo"}},
			NodeStatsInterval: -1,
			Transport:         newRoundTripper(),
			Logger:            &JSONLogger{Output: &dst, EnableRequestBody: true},
		})
		t.Cleanup(func() { _ = tp.Close() })

		req, _ := http.NewRequest(http.MethodGet, "/abc?q=a,b", nil)
		req.Body = io.NopCloser(strings.NewReader(`{"query":"42"}`))

		res, err := tp.Stream(req)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}
		defer res.Body.Close()

		_, err = io.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("Error reading response body: %s", err)
		}

		output := dst.String()
		output = strings.TrimSuffix(output, "\n")
		// fmt.Println(output)

		lines := strings.Split(output, "\n")

		if len(lines) != 1 {
			t.Fatalf("Expected 1 line, got %d", len(lines))
		}

		var j map[string]any
		if err := json.Unmarshal([]byte(output), &j); err != nil {
			t.Errorf("Error decoding JSON: %s", err)
		}

		body := j["http"].(map[string]any)["request"].(map[string]any)["body"].(string)
		if !strings.Contains(body, "query") {
			t.Errorf("Unexpected JSON output: %s", body)
		}
	})

	t.Run("Custom", func(t *testing.T) {
		var dst strings.Builder

		tp, _ := New(Config{
			URLs:              []*url.URL{{Scheme: "http", Host: "foo"}},
			NodeStatsInterval: -1,
			Transport:         newRoundTripper(),
			Logger:            &CustomLogger{Output: &dst},
		})
		t.Cleanup(func() { _ = tp.Close() })

		req, _ := http.NewRequest(http.MethodGet, "/abc?q=a,b", nil)
		req.Body = io.NopCloser(strings.NewReader(`{"query":"42"}`))

		res, err := tp.Stream(req)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}
		defer res.Body.Close()

		if !strings.HasPrefix(dst.String(), "GET http://foo/abc?q=a,b") {
			t.Errorf("Unexpected output: %s", dst.String())
		}
	})

	t.Run("Duplicate body", func(t *testing.T) {
		input := ResponseBody{content: strings.NewReader("FOOBAR")}

		b1, b2, err := duplicateBody(&input)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}
		if !input.closed {
			t.Errorf("Expected input to be closed: %#v", input)
		}

		read, _ := io.ReadAll(&input)
		if len(read) > 0 {
			t.Errorf("Expected input to be drained: %#v", input.content)
		}

		b1r, _ := io.ReadAll(b1)
		b2r, _ := io.ReadAll(b2)
		if len(b1r) != 6 || len(b2r) != 6 {
			t.Errorf(
				"Unexpected duplicate content, b1=%q (%db), b2=%q (%db)",
				string(b1r), len(b1r), string(b2r), len(b2r),
			)
		}
	})

	t.Run("Duplicate body with error", func(t *testing.T) {
		input := ResponseBody{content: &ErrorReader{r: strings.NewReader("FOOBAR")}}

		b1, b2, err := duplicateBody(&input)
		if err == nil {
			t.Errorf("Expected error, got: %v", err)
		}
		if err.Error() != "MOCK ERROR" {
			t.Errorf("Unexpected error value, expected [ERROR MOCK], got [%s]", err.Error())
		}

		read, _ := io.ReadAll(&input)
		if string(read) != "BAR" {
			t.Errorf("Unexpected undrained part: %q", read)
		}

		b2r, _ := io.ReadAll(b2)
		if string(b2r) != "FOO" {
			t.Errorf("Unexpected value, b2=%q", string(b2r))
		}

		b1c, err := io.ReadAll(b1)
		if string(b1c) != "FOO" {
			t.Errorf("Unexpected value, b1=%q", string(b1c))
		}
		if err == nil {
			t.Errorf("Expected error when reading b1, got: %v", err)
		}
		if err.Error() != "MOCK ERROR" {
			t.Errorf("Unexpected error value, expected [ERROR MOCK], got [%s]", err.Error())
		}
	})
}

// debugRecordPrefixLen is the width of the fixed timestamp-and-level prefix
// textDebugLogger writes ahead of every record.
const debugRecordPrefixLen = len("[15:04:05.000] DEBUG    ")

func TestTextDebugLogger(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  string
		kv   []any
		want string
	}{
		{
			name: "message only",
			msg:  "Discovery: starting",
			want: "Discovery: starting\n",
		},
		{
			name: "key value pairs",
			msg:  "Node overloaded",
			kv:   []any{"conn", "https://localhost:9200", "heap_used_percent", 93},
			want: "Node overloaded conn=https://localhost:9200 heap_used_percent=93\n",
		},
		{
			name: "dangling key",
			msg:  "Pool resurrect",
			kv:   []any{"conn", "node-1", "state"},
			want: "Pool resurrect conn=node-1 !BADKEY=state\n",
		},
		{
			// slog consumes only the bad key, so the argument after it starts a
			// fresh pair instead of being swallowed as its value.
			name: "non-string key resyncs on the next pair",
			msg:  "Pool resurrect",
			kv:   []any{42, "conn", "node-1"},
			want: "Pool resurrect !BADKEY=42 conn=node-1\n",
		},
		{
			name: "non-string key last",
			msg:  "Pool resurrect",
			kv:   []any{"conn", "node-1", 42},
			want: "Pool resurrect conn=node-1 !BADKEY=42\n",
		},
		{
			name: "error value",
			msg:  "Discovery failed",
			kv:   []any{"err", errors.New("connection refused")},
			want: "Discovery failed err=connection refused\n",
		},
		{
			name: "empty kv",
			msg:  "Warmup complete",
			kv:   []any{},
			want: "Warmup complete\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			(&textDebugLogger{Output: &buf}).Debug(tt.msg, tt.kv...)

			got := buf.String()
			require.Greater(t, len(got), debugRecordPrefixLen)
			require.Regexp(t, `^\[\d{2}:\d{2}:\d{2}\.\d{3}\] DEBUG {4}$`, got[:debugRecordPrefixLen])
			require.Equal(t, tt.want, got[debugRecordPrefixLen:])
		})
	}
}

func TestResolveDebugLogger(t *testing.T) {
	t.Parallel()

	supplied := &testDebugLogger{}

	tests := []struct {
		name string
		cfg  Config
		want DebugLogger
	}{
		{
			name: "neither set installs nothing",
			cfg:  Config{},
			want: nil,
		},
		{
			name: "EnableDebugLogger selects the built-in logger",
			cfg:  Config{EnableDebugLogger: true},
			want: &textDebugLogger{Output: os.Stderr},
		},
		{
			name: "supplied logger is used",
			cfg:  Config{DebugLogger: supplied},
			want: supplied,
		},
		{
			name: "supplied logger wins over EnableDebugLogger",
			cfg:  Config{DebugLogger: supplied, EnableDebugLogger: true},
			want: supplied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, resolveDebugLogger(tt.cfg))
		})
	}
}

// TestTextDebugLoggerConcurrent pins the concurrency requirement DebugLogger
// states: records reach the output whole, and the write itself is serialized, so
// the type is safe with any writer rather than only with an atomic one.
func TestTextDebugLoggerConcurrent(t *testing.T) {
	t.Parallel()

	const records = 50

	var buf bytes.Buffer
	logger := &textDebugLogger{Output: &buf}

	var wg sync.WaitGroup
	for i := range records {
		wg.Go(func() { logger.Debug("concurrent record", "i", i) })
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	require.Len(t, lines, records)
	for _, line := range lines {
		require.Regexp(t, `^\[\d{2}:\d{2}:\d{2}\.\d{3}\] DEBUG {4}concurrent record i=\d+$`, line)
	}
}

// TestDebugFunc pins the func-shaped path into DebugLogger, which is how a
// caller whose logging is a function rather than a type plugs in.
func TestDebugFunc(t *testing.T) {
	t.Parallel()

	var (
		gotMsg string
		gotKV  []any
	)

	var dl DebugLogger = DebugFunc(func(msg string, kv ...any) {
		gotMsg, gotKV = msg, kv
	})
	dl.Debug("Node overloaded", "conn", "https://localhost:9200", "heap_used_percent", 93)

	require.Equal(t, "Node overloaded", gotMsg)
	require.Equal(t, []any{"conn", "https://localhost:9200", "heap_used_percent", 93}, gotKV)
}

type CustomLogger struct {
	Output io.Writer
}

func (l *CustomLogger) LogRoundTrip(
	req *http.Request,
	res *http.Response,
	_ error,
	_ time.Time,
	_ time.Duration,
) error {
	fmt.Fprintln(l.Output, req.Method, req.URL, "->", res.Status)
	return nil
}

func (l *CustomLogger) RequestBodyEnabled() bool  { return false }
func (l *CustomLogger) ResponseBodyEnabled() bool { return false }

type ResponseBody struct {
	content io.Reader
	closed  bool
}

func (r *ResponseBody) Read(p []byte) (int, error) {
	return r.content.Read(p)
}

func (r *ResponseBody) Close() error {
	r.closed = true
	return nil
}

type ErrorReader struct {
	r io.Reader
}

func (r *ErrorReader) Read(p []byte) (int, error) {
	lr := io.LimitReader(r.r, 3)
	c, _ := lr.Read(p)
	return c, errors.New("MOCK ERROR")
}
