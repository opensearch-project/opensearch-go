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

	"github.com/opensearch-project/opensearch-go/v5/debuglog"
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

// TestTextDebugLogger pins the built-in logger's rendering, one row per
// debuglog.Event method. There are no malformed-pair cases to cover: the typed
// methods make key/value pairing a compile-time fact, so a dangling key or a
// non-string key cannot be constructed.
func TestTextDebugLogger(t *testing.T) {
	t.Parallel()

	var (
		fixedTime = time.Date(2026, 8, 19, 4, 13, 43, 0, time.UTC)
		nodeURL   = &url.URL{Scheme: "https", Host: "localhost:9200"}
		nilURL    *url.URL
	)

	tests := []struct {
		name   string
		msg    string
		fields func(debuglog.Event) debuglog.Event
		want   string
	}{
		{
			name: "message only",
			msg:  "Discovery: starting",
			want: "Discovery: starting\n",
		},
		{
			name: "string and int pair",
			msg:  "Node overloaded",
			fields: func(e debuglog.Event) debuglog.Event {
				return e.Str("conn", "https://localhost:9200").Int("heap_used_percent", 93)
			},
			want: "Node overloaded conn=https://localhost:9200 heap_used_percent=93\n",
		},
		{
			name:   "string slice",
			msg:    "Discovery: connection removed",
			fields: func(e debuglog.Event) debuglog.Event { return e.Strs("roles", []string{"data", "ingest"}) },
			want:   "Discovery: connection removed roles=[data ingest]\n",
		},
		{
			name: "sized integers",
			msg:  "AIMD: adjusted congestion window",
			fields: func(e debuglog.Event) debuglog.Event {
				return e.Int32("cwnd_to", 8).Int64("tripped", 3).Uint32("stream_id", 7)
			},
			want: "AIMD: adjusted congestion window cwnd_to=8 tripped=3 stream_id=7\n",
		},
		{
			name:   "float",
			msg:    "Node overloaded: breaker size over threshold",
			fields: func(e debuglog.Event) debuglog.Event { return e.Float64("ratio", 0.85) },
			want:   "Node overloaded: breaker size over threshold ratio=0.85\n",
		},
		{
			name: "duration",
			msg:  "Promoted singleServerPool to multiServerPool",
			fields: func(e debuglog.Event) debuglog.Event {
				return e.Dur("resurrect_timeout_initial", 1500*time.Millisecond)
			},
			want: "Promoted singleServerPool to multiServerPool resurrect_timeout_initial=1.5s\n",
		},
		{
			name:   "timestamp",
			msg:    "resetDeadConnViability: cleared lcViable",
			fields: func(e debuglog.Event) debuglog.Event { return e.Time("dead_since", fixedTime) },
			want:   "resetDeadConnViability: cleared lcViable dead_since=2026-08-19 04:13:43 +0000 UTC\n",
		},
		{
			name:   "stringer",
			msg:    "Request failed",
			fields: func(e debuglog.Event) debuglog.Event { return e.Stringer("conn", nodeURL) },
			want:   "Request failed conn=https://localhost:9200\n",
		},
		{
			// A typed-nil pointer satisfies fmt.Stringer while (*url.URL).String
			// dereferences its receiver, so the nil has to be caught before String
			// is called. Debug logging must not be able to panic the program.
			name:   "nil stringer renders rather than panicking",
			msg:    "Request failed",
			fields: func(e debuglog.Event) debuglog.Event { return e.Stringer("conn", nilURL) },
			want:   "Request failed conn=<nil>\n",
		},
		{
			name:   "error",
			msg:    "Discovery failed",
			fields: func(e debuglog.Event) debuglog.Event { return e.Err(errors.New("connection refused")) },
			want:   "Discovery failed err=connection refused\n",
		},
		{
			name:   "nil error",
			msg:    "Discovery failed",
			fields: func(e debuglog.Event) debuglog.Event { return e.Err(nil) },
			want:   "Discovery failed err=<nil>\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			event := (&textDebugLogger{Output: &buf}).Debug()
			if tt.fields != nil {
				event = tt.fields(event)
			}
			event.Msg(tt.msg)

			got := buf.String()
			require.Greater(t, len(got), debugRecordPrefixLen)
			require.Regexp(t, `^\[\d{2}:\d{2}:\d{2}\.\d{3}\] DEBUG {4}$`, got[:debugRecordPrefixLen])
			require.Equal(t, tt.want, got[debugRecordPrefixLen:])
		})
	}
}

// TestTextDebugLoggerChainDiscarded pins that a chain which never reaches Msg
// writes nothing. The compiler cannot catch a missing terminator, so this records
// the consequence the debuglog chain guard exists to prevent.
//
// The event is held in a variable rather than chained inline because that guard
// sweeps this repository for exactly the inline shape. Keep it this way, or the
// guard reports this test as the defect it is describing.
func TestTextDebugLoggerChainDiscarded(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	event := (&textDebugLogger{Output: &buf}).Debug()
	event.Str("conn", "node-1").Int("attempts", 2)

	require.Empty(t, buf.String())
}

func TestResolveDebugLogger(t *testing.T) {
	t.Parallel()

	supplied := &testDebugLogger{}

	tests := []struct {
		name string
		cfg  Config
		want debuglog.Logger
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

// TestTextDebugLoggerConcurrent pins the concurrency requirement debuglog.Logger
// states: records reach the output whole, and the write itself is serialized, so
// the type is safe with any writer rather than only with an atomic one.
func TestTextDebugLoggerConcurrent(t *testing.T) {
	t.Parallel()

	const records = 50

	var buf bytes.Buffer
	logger := &textDebugLogger{Output: &buf}

	var wg sync.WaitGroup
	for i := range records {
		wg.Go(func() { logger.Debug().Int("i", i).Msg("concurrent record") })
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	require.Len(t, lines, records)
	for _, line := range lines {
		require.Regexp(t, `^\[\d{2}:\d{2}:\d{2}\.\d{3}\] DEBUG {4}concurrent record i=\d+$`, line)
	}
}

// TestTextDebugLoggerReusesEvents pins the one hazard pooling introduces: an
// event handed back by Msg and picked up again by the next Debug must carry none
// of the previous record's fields.
//
// The records differ in field count so that a stale buffer shows up either way
// round, whichever of them the pool happens to serve first.
func TestTextDebugLoggerReusesEvents(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := &textDebugLogger{Output: &buf}

	logger.Debug().Str("conn", "node-1").Int("attempts", 2).Msg("first")
	logger.Debug().Str("conn", "node-2").Msg("second")
	logger.Debug().Msg("third")

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	require.Len(t, lines, 3)
	require.Equal(t, "first conn=node-1 attempts=2", lines[0][debugRecordPrefixLen:])
	require.Equal(t, "second conn=node-2", lines[1][debugRecordPrefixLen:])
	require.Equal(t, "third", lines[2][debugRecordPrefixLen:])
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
