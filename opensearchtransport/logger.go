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

package opensearchtransport

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/opensearch-project/opensearch-go/v5/debuglog"
	"github.com/opensearch-project/opensearch-go/v5/internal/envvars"
)

var debugLoggerPtr atomic.Pointer[debuglog.Logger]

// Debug begins one debug record: chain fields onto the returned event and end
// the chain with Msg.
//
//	Debug().Stringer("conn", conn.URL).Err(err).Msg("Request failed")
//
// It never returns nil. With no logger installed it returns [debuglog.Nop],
// whose methods discard the record and allocate nothing, so emitting sites need
// no guard. The arguments to the chained calls are still evaluated either way,
// because Go cannot abandon a method chain partway, so a field that costs
// something to compute is passed lazily through Stringer rather than converted
// at the call site.
//
// Other packages in the module use this to emit their own configuration-time
// diagnostics through the logger the transport already installed.
func Debug() debuglog.Event {
	if p := debugLoggerPtr.Load(); p != nil {
		return (*p).Debug()
	}
	return debuglog.Nop()
}

// debugEnabled reports whether a debug logger is installed.
//
// Emitting a record does not need this: [Debug] handles the absent case itself,
// and the 88 emitting sites call it unguarded. It exists for the two callers that
// cannot, because they do work a no-op event cannot undo. The
// OPENSEARCH_GO_POLICY_DUMP tree writes to stderr directly rather than through
// the logger, and one AIMD record has to take a mutex to read the fields it
// carries.
func debugEnabled() bool { return debugLoggerPtr.Load() != nil }

func storeDebugLogger(dl debuglog.Logger) {
	if dl == nil {
		debugLoggerPtr.Store(nil)
	} else {
		debugLoggerPtr.Store(&dl)
	}
}

// resolveDebugLogger returns the debug logger a Config asks for: an explicitly
// supplied one wins, otherwise EnableDebugLogger selects the built-in text
// logger. It returns nil when the Config asks for neither, so that a logger
// already installed from the environment is left in place.
func resolveDebugLogger(cfg Config) debuglog.Logger {
	switch {
	case cfg.DebugLogger != nil:
		return cfg.DebugLogger
	case cfg.EnableDebugLogger:
		return &textDebugLogger{Output: os.Stderr}
	default:
		return nil
	}
}

func init() { //nolint:gochecknoinits // Only set implicitly once at startup
	if envvars.DebugRequested() {
		storeDebugLogger(&textDebugLogger{Output: os.Stderr})
	}
}

// Logger defines an interface for logging request and response.
type Logger interface {
	// LogRoundTrip should not modify the request or response, except for consuming and closing the body.
	// Implementations have to check for nil values in request and response.
	LogRoundTrip(*http.Request, *http.Response, error, time.Time, time.Duration) error
	// RequestBodyEnabled makes the client pass a copy of request body to the logger.
	RequestBodyEnabled() bool
	// ResponseBodyEnabled makes the client pass a copy of response body to the logger.
	ResponseBodyEnabled() bool
}

// TextLogger prints the log message in plain text.
type TextLogger struct {
	Output             io.Writer
	EnableRequestBody  bool
	EnableResponseBody bool
}

// ColorLogger prints the log message in a terminal-optimized plain text.
type ColorLogger struct {
	Output             io.Writer
	EnableRequestBody  bool
	EnableResponseBody bool
}

// CurlLogger prints the log message as a runnable curl command.
type CurlLogger struct {
	Output             io.Writer
	EnableRequestBody  bool
	EnableResponseBody bool
}

// JSONLogger prints the log message as JSON.
type JSONLogger struct {
	Output             io.Writer
	EnableRequestBody  bool
	EnableResponseBody bool
}

// textDebugLogger is the built-in [debuglog.Logger], printing records as plain
// text with a timestamp prefix. It is what OPENSEARCH_GO_LOG=debug and
// Config.EnableDebugLogger install, so the client has a working debug logger
// without any logging library in its dependency graph.
type textDebugLogger struct {
	Output io.Writer

	// mu serializes writes to Output. debuglog.Logger requires implementations to
	// be safe for concurrent use, and records are emitted from several transport
	// goroutines at once. Without it the type would be safe only for a writer
	// whose Write is itself atomic, which os.Stderr happens to be.
	mu sync.Mutex
}

// LogRoundTrip prints the information about request and response.
func (l *TextLogger) LogRoundTrip(req *http.Request, res *http.Response, err error, start time.Time, dur time.Duration) error {
	fmt.Fprintf(l.Output, "%s %s %s [status:%d request:%s]\n", // #nosec G705
		start.Format(time.RFC3339),
		req.Method,
		req.URL.String(),
		resStatusCode(res),
		dur.Truncate(time.Millisecond),
	)
	if l.RequestBodyEnabled() && req != nil && req.Body != nil && req.Body != http.NoBody {
		var buf bytes.Buffer
		if req.GetBody != nil {
			b, _ := req.GetBody()
			buf.ReadFrom(b)
		} else {
			buf.ReadFrom(req.Body)
		}
		logBodyAsText(l.Output, &buf, ">")
	}
	if l.ResponseBodyEnabled() && res != nil && res.Body != nil && res.Body != http.NoBody {
		defer res.Body.Close()
		var buf bytes.Buffer
		buf.ReadFrom(res.Body)
		logBodyAsText(l.Output, &buf, "<")
	}
	if err != nil {
		fmt.Fprintf(l.Output, "! ERROR: %v\n", err)
	}
	return nil
}

// RequestBodyEnabled returns true when the request body should be logged.
func (l *TextLogger) RequestBodyEnabled() bool { return l.EnableRequestBody }

// ResponseBodyEnabled returns true when the response body should be logged.
func (l *TextLogger) ResponseBodyEnabled() bool { return l.EnableResponseBody }

// LogRoundTrip prints the information about request and response.
func (l *ColorLogger) LogRoundTrip(req *http.Request, res *http.Response, err error, _ time.Time, dur time.Duration) error {
	query, _ := url.QueryUnescape(req.URL.RawQuery)
	if query != "" {
		query = "?" + query
	}

	var (
		status string
		color  string
	)

	status = res.Status
	switch {
	case res.StatusCode >= http.StatusContinue && res.StatusCode < http.StatusMultipleChoices:
		color = "\x1b[32m"
	case res.StatusCode >= http.StatusMultipleChoices && res.StatusCode < http.StatusInternalServerError:
		color = "\x1b[33m"
	case res.StatusCode >= http.StatusInternalServerError:
		color = "\x1b[31m"
	default:
		status = "ERROR"
		color = "\x1b[31;4m"
	}

	fmt.Fprintf(l.Output, // #nosec G705
		"%6s \x1b[1;4m%s://%s%s\x1b[0m%s %s%s\x1b[0m \x1b[2m%s\x1b[0m\n",
		req.Method,
		req.URL.Scheme,
		req.URL.Host,
		req.URL.Path,
		query,
		color,
		status,
		dur.Truncate(time.Millisecond),
	)

	if l.RequestBodyEnabled() && req != nil && req.Body != nil && req.Body != http.NoBody {
		var buf bytes.Buffer
		if req.GetBody != nil {
			b, _ := req.GetBody()
			buf.ReadFrom(b)
		} else {
			buf.ReadFrom(req.Body)
		}
		fmt.Fprint(l.Output, "\x1b[2m")
		logBodyAsText(l.Output, &buf, "       >>")
		fmt.Fprint(l.Output, "\x1b[0m")
	}

	if l.ResponseBodyEnabled() && res != nil && res.Body != nil && res.Body != http.NoBody {
		defer res.Body.Close()
		var buf bytes.Buffer
		buf.ReadFrom(res.Body)
		fmt.Fprint(l.Output, "\x1b[2m")
		logBodyAsText(l.Output, &buf, "       <<")
		fmt.Fprint(l.Output, "\x1b[0m")
	}

	if err != nil {
		fmt.Fprintf(l.Output, "\x1b[31;1m>> ERROR \x1b[31m%v\x1b[0m\n", err)
	}

	if l.RequestBodyEnabled() || l.ResponseBodyEnabled() {
		fmt.Fprintf(l.Output, "\x1b[2m%s\x1b[0m\n", strings.Repeat("-", 80))
	}
	return nil
}

// RequestBodyEnabled returns true when the request body should be logged.
func (l *ColorLogger) RequestBodyEnabled() bool { return l.EnableRequestBody }

// ResponseBodyEnabled returns true when the response body should be logged.
func (l *ColorLogger) ResponseBodyEnabled() bool { return l.EnableResponseBody }

// LogRoundTrip prints the information about request and response.
func (l *CurlLogger) LogRoundTrip(req *http.Request, res *http.Response, _ error, start time.Time, dur time.Duration) error {
	var b bytes.Buffer

	var query string
	qvalues := url.Values{}
	for k, v := range req.URL.Query() {
		if k == "pretty" {
			continue
		}
		for _, qv := range v {
			qvalues.Add(k, qv)
		}
	}
	if len(qvalues) > 0 {
		query = qvalues.Encode()
	}

	b.WriteString(`curl`)
	if req.Method == http.MethodHead {
		b.WriteString(" --head")
	} else {
		fmt.Fprintf(&b, " -X %s", req.Method) // #nosec G705
	}

	if len(req.Header) > 0 {
		for k, vv := range req.Header {
			if k == "Authorization" || k == "User-Agent" {
				continue
			}
			v := strings.Join(vv, ",")
			fmt.Fprintf(&b, " -H '%s: %s'", k, v)
		}
	}

	// If by some oddity we end up with a nil req.URL, we handle it gracefully.
	if req.URL == nil {
		b.WriteString(" '")
	} else {
		fmt.Fprintf(&b, " '%s://%s%s", req.URL.Scheme, req.URL.Host, req.URL.Path) // #nosec G705
	}
	b.WriteString("?pretty")
	if query != "" {
		fmt.Fprintf(&b, "&%s", query) // #nosec G705
	}
	b.WriteString("'")

	if req != nil && req.Body != nil && req.Body != http.NoBody {
		var buf bytes.Buffer
		if req.GetBody != nil {
			b, _ := req.GetBody()
			buf.ReadFrom(b)
		} else {
			buf.ReadFrom(req.Body)
		}

		b.Grow(buf.Len())
		b.WriteString(" -d \\\n'")
		json.Indent(&b, buf.Bytes(), "", " ")
		b.WriteString("'")
	}

	b.WriteRune('\n')

	status := res.Status

	fmt.Fprintf(&b, "# => %s [%s] %s\n", start.UTC().Format(time.RFC3339), status, dur.Truncate(time.Millisecond)) // #nosec G705
	if l.ResponseBodyEnabled() && res != nil && res.Body != nil && res.Body != http.NoBody {
		var buf bytes.Buffer
		buf.ReadFrom(res.Body)

		b.Grow(buf.Len())
		b.WriteString("# ")
		json.Indent(&b, buf.Bytes(), "# ", " ")
	}

	b.WriteString("\n")
	if l.ResponseBodyEnabled() && res != nil && res.Body != nil && res.Body != http.NoBody {
		b.WriteString("\n")
	}

	_, err := b.WriteTo(l.Output)
	return err
}

// RequestBodyEnabled returns true when the request body should be logged.
func (l *CurlLogger) RequestBodyEnabled() bool { return l.EnableRequestBody }

// ResponseBodyEnabled returns true when the response body should be logged.
func (l *CurlLogger) ResponseBodyEnabled() bool { return l.EnableResponseBody }

// LogRoundTrip prints the information about request and response.
func (l *JSONLogger) LogRoundTrip(req *http.Request, res *http.Response, err error, start time.Time, dur time.Duration) error {
	// TODO: Research performance optimization of using sync.Pool

	const bsize = 200
	b := bytes.NewBuffer(make([]byte, 0, bsize))
	v := make([]byte, 0, bsize)

	appendTime := func(t time.Time) {
		v = v[:0]
		v = t.AppendFormat(v, time.RFC3339)
		b.Write(v)
	}

	appendQuote := func(s string) {
		v = v[:0]
		v = strconv.AppendQuote(v, s)
		b.Write(v)
	}

	appendInt := func(i int64) {
		v = v[:0]
		v = strconv.AppendInt(v, i, 10)
		b.Write(v)
	}

	b.WriteRune('{')
	// -- Timestamp
	b.WriteString(`"@timestamp":"`)
	appendTime(start.UTC())
	b.WriteRune('"')
	// -- Event
	b.WriteString(`,"event":{`)
	b.WriteString(`"duration":`)
	appendInt(dur.Nanoseconds())
	b.WriteRune('}')
	// -- URL
	b.WriteString(`,"url":{`)
	b.WriteString(`"scheme":`)
	appendQuote(req.URL.Scheme)
	b.WriteString(`,"domain":`)
	appendQuote(req.URL.Hostname())
	if port := req.URL.Port(); port != "" {
		b.WriteString(`,"port":`)
		b.WriteString(port)
	}
	b.WriteString(`,"path":`)
	appendQuote(req.URL.Path)
	b.WriteString(`,"query":`)
	appendQuote(req.URL.RawQuery)
	b.WriteRune('}') // Close "url"
	// -- HTTP
	b.WriteString(`,"http":`)
	// ---- Request
	b.WriteString(`{"request":{`)
	b.WriteString(`"method":`)
	appendQuote(req.Method)
	if l.RequestBodyEnabled() && req != nil && req.Body != nil && req.Body != http.NoBody {
		var buf bytes.Buffer
		if req.GetBody != nil {
			b, _ := req.GetBody()
			buf.ReadFrom(b)
		} else {
			buf.ReadFrom(req.Body)
		}

		b.Grow(buf.Len() + 8)
		b.WriteString(`,"body":`)
		appendQuote(buf.String())
	}
	b.WriteRune('}') // Close "http.request"
	// ---- Response
	b.WriteString(`,"response":{`)
	b.WriteString(`"status_code":`)
	appendInt(int64(resStatusCode(res)))
	if l.ResponseBodyEnabled() && res != nil && res.Body != nil && res.Body != http.NoBody {
		defer res.Body.Close()
		var buf bytes.Buffer
		buf.ReadFrom(res.Body)

		b.Grow(buf.Len() + 8)
		b.WriteString(`,"body":`)
		appendQuote(buf.String())
	}
	b.WriteRune('}') // Close "http.response"
	b.WriteRune('}') // Close "http"
	// -- Error
	if err != nil {
		b.WriteString(`,"error":{"message":`)
		appendQuote(err.Error())
		b.WriteRune('}') // Close "error"
	}
	b.WriteRune('}')
	b.WriteRune('\n')
	b.WriteTo(l.Output)

	return nil
}

// RequestBodyEnabled returns true when the request body should be logged.
func (l *JSONLogger) RequestBodyEnabled() bool { return l.EnableRequestBody }

// ResponseBodyEnabled returns true when the response body should be logged.
func (l *JSONLogger) ResponseBodyEnabled() bool { return l.EnableResponseBody }

// textDebugEventPool holds events between records so that a logger left switched
// on does not allocate one, plus its two buffers, per record.
//
// An event is returned by [textDebugEvent.Msg]. A chain that never reaches Msg
// simply leaves its event to the garbage collector, which is why the pool is a
// performance measure and not a correctness one.
var textDebugEventPool = sync.Pool{New: func() any { return new(textDebugEvent) }}

// Debug begins a record. Fields accumulate on the returned event and Msg writes
// the assembled line.
func (l *textDebugLogger) Debug() debuglog.Event {
	e, _ := textDebugEventPool.Get().(*textDebugEvent)
	e.logger = l
	e.fields = e.fields[:0]
	return e
}

// textDebugEvent accumulates one record for [textDebugLogger] as " key=value"
// fragments, rendering each value the way the fmt package would.
//
// Values are appended with the strconv.Append functions rather than through
// fmt.Fprintf, so the built-in logger neither boxes a value it is handed (the
// cost the typed [debuglog.Event] methods exist to avoid) nor allocates a string
// per field on the way into the buffer.
type textDebugEvent struct {
	logger *textDebugLogger

	// fields holds the rendered " key=value" fragments in call order. line holds
	// the whole record, assembled by Msg, which needs its own buffer because the
	// timestamp and message precede fields that have already been appended.
	//
	// Both survive in textDebugEventPool across records, so a steady stream of
	// records reuses one pair of buffers rather than allocating per record.
	fields []byte
	line   []byte
}

// maxPooledDebugBuffer bounds the buffer capacity the pool retains. A record
// carrying an unusually long value (a discovery response, a policy tree) would
// otherwise park its whole buffer in the pool for the life of the process, so an
// event grown past this is dropped instead and the next one starts fresh.
//
// 4 KiB is comfortably above every record the client emits; the longest observed
// is a few hundred bytes.
const maxPooledDebugBuffer = 4 << 10

// key appends the separator, the key, and the "=" that the value follows.
func (e *textDebugEvent) key(key string) {
	e.fields = append(e.fields, ' ')
	e.fields = append(e.fields, key...)
	e.fields = append(e.fields, '=')
}

// field appends one pair whose value is already a string.
func (e *textDebugEvent) field(key, val string) debuglog.Event {
	e.key(key)
	e.fields = append(e.fields, val...)
	return e
}

// Str implements [debuglog.Event].
func (e *textDebugEvent) Str(key, val string) debuglog.Event { return e.field(key, val) }

// Strs implements [debuglog.Event], rendering the slice as fmt's %v does:
// space-separated inside square brackets.
func (e *textDebugEvent) Strs(key string, val []string) debuglog.Event {
	e.key(key)
	e.fields = append(e.fields, '[')
	for i, s := range val {
		if i > 0 {
			e.fields = append(e.fields, ' ')
		}
		e.fields = append(e.fields, s...)
	}
	e.fields = append(e.fields, ']')
	return e
}

// Int implements [debuglog.Event].
func (e *textDebugEvent) Int(key string, val int) debuglog.Event {
	e.key(key)
	e.fields = strconv.AppendInt(e.fields, int64(val), 10)
	return e
}

// Int32 implements [debuglog.Event].
func (e *textDebugEvent) Int32(key string, val int32) debuglog.Event {
	e.key(key)
	e.fields = strconv.AppendInt(e.fields, int64(val), 10)
	return e
}

// Int64 implements [debuglog.Event].
func (e *textDebugEvent) Int64(key string, val int64) debuglog.Event {
	e.key(key)
	e.fields = strconv.AppendInt(e.fields, val, 10)
	return e
}

// Uint32 implements [debuglog.Event].
func (e *textDebugEvent) Uint32(key string, val uint32) debuglog.Event {
	e.key(key)
	e.fields = strconv.AppendUint(e.fields, uint64(val), 10)
	return e
}

// Float64 implements [debuglog.Event].
func (e *textDebugEvent) Float64(key string, val float64) debuglog.Event {
	e.key(key)
	e.fields = strconv.AppendFloat(e.fields, val, 'g', -1, 64)
	return e
}

// Dur implements [debuglog.Event].
func (e *textDebugEvent) Dur(key string, val time.Duration) debuglog.Event {
	return e.field(key, val.String())
}

// Time implements [debuglog.Event].
func (e *textDebugEvent) Time(key string, val time.Time) debuglog.Event {
	return e.field(key, val.String())
}

// Stringer implements [debuglog.Event], rendering a nil value as <nil> rather
// than panicking.
func (e *textDebugEvent) Stringer(key string, val fmt.Stringer) debuglog.Event {
	return e.field(key, debuglog.StringerText(val))
}

// Err implements [debuglog.Event], recording the error under the key "err".
func (e *textDebugEvent) Err(err error) debuglog.Event {
	if err == nil {
		return e.field(errFieldKey, debuglog.NilText)
	}
	return e.field(errFieldKey, err.Error())
}

// Msg writes msg and the accumulated fields as one line, prefixed with a
// timestamp and terminated with a newline, and returns the event to the pool.
//
// The line is assembled before the write and the write is serialized by the
// logger's mutex, so concurrent callers neither interleave fragments nor race on
// Output.
func (e *textDebugEvent) Msg(msg string) {
	e.line = append(e.line[:0], '[')
	e.line = time.Now().UTC().AppendFormat(e.line, "15:04:05.000")
	e.line = append(e.line, "] DEBUG    "...)
	e.line = append(e.line, msg...)
	e.line = append(e.line, e.fields...)
	e.line = append(e.line, '\n')

	e.logger.mu.Lock()
	// Msg returns no error: a debug logger that cannot write has nowhere left to
	// report it.
	_, _ = e.logger.Output.Write(e.line)
	e.logger.mu.Unlock()

	// The event is unreachable to the caller from here: Msg ends the chain, and
	// nothing it returned escaped. Clear the logger so a pooled event holds no
	// reference to a writer the application may be finished with.
	e.logger = nil
	if cap(e.fields) <= maxPooledDebugBuffer && cap(e.line) <= maxPooledDebugBuffer {
		textDebugEventPool.Put(e)
	}
}

// errFieldKey is the key [debuglog.Event.Err] records an error under in the
// built-in logger. An adapter uses whatever key its own library configures, so
// this is not shared through debuglog. A nil value renders as
// [debuglog.NilText], which is.
const errFieldKey = "err"

func logBodyAsText(dst io.Writer, body io.Reader, prefix string) {
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		s := scanner.Text()
		if s != "" {
			fmt.Fprintf(dst, "%s %s\n", prefix, s) // #nosec G705
		}
	}
}

func duplicateBody(body io.ReadCloser) (io.ReadCloser, io.ReadCloser, error) {
	var (
		b1 bytes.Buffer
		b2 bytes.Buffer
		tr = io.TeeReader(body, &b2)
	)

	if _, err := b1.ReadFrom(tr); err != nil {
		return io.NopCloser(io.MultiReader(&b1, errorReader{err: err})), io.NopCloser(io.MultiReader(&b2, errorReader{err: err})), err
	}

	defer func() { body.Close() }()

	return io.NopCloser(&b1), io.NopCloser(&b2), nil
}

func resStatusCode(res *http.Response) int {
	if res == nil {
		return -1
	}
	return res.StatusCode
}

type errorReader struct{ err error }

func (r errorReader) Read(_ []byte) (int, error) { return 0, r.err }
