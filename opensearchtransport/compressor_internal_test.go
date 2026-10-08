// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

var (
	errFakeNew   = errors.New("fake NewEncoder failure")
	errFakeWrite = errors.New("fake Write failure")
	errFakeClose = errors.New("fake Close failure")
)

// fakeCompressor is a Compressor over compress/gzip whose encoders record the
// calls they receive and can be made to fail.
//
// writeErr and closeErr are read when an encoder's Write or Close runs, so a
// test sets them between requests while no request is in flight.
type fakeCompressor struct {
	encoding   string
	newErr     error
	nilEncoder bool // NewEncoder returns a nil Encoder and a nil error
	writeErr   error
	closeErr   error

	// overlaps counts encoder calls that started while another call on the same
	// encoder was still running.
	overlaps atomic.Int64

	mu struct {
		sync.Mutex
		encoders []*fakeEncoder
	}
}

func (c *fakeCompressor) ContentEncoding() string { return c.encoding }

func (c *fakeCompressor) NewEncoder(w io.Writer) (Encoder, error) {
	if c.newErr != nil {
		return nil, c.newErr
	}
	if c.nilEncoder {
		return nil, nil //nolint:nilnil // models a Compressor that breaks the NewEncoder contract
	}

	e := &fakeEncoder{c: c, zw: gzip.NewWriter(w)}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mu.encoders = append(c.mu.encoders, e)
	return e, nil
}

// encoders returns the encoders created so far, in creation order.
func (c *fakeCompressor) encoders() []*fakeEncoder {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*fakeEncoder(nil), c.mu.encoders...)
}

// fakeEncoder records its calls as one byte each: 'w' Write, 'c' Close, 'r'
// Reset.
type fakeEncoder struct {
	c        *fakeCompressor
	zw       *gzip.Writer
	inflight atomic.Int32

	mu struct {
		sync.Mutex
		events []byte
	}
}

// enter records event and returns the func that ends the call. An Encoder is
// used by one goroutine at a time, so a call that starts while another is
// running counts as an overlap.
func (e *fakeEncoder) enter(event byte) func() {
	if e.inflight.Add(1) > 1 {
		e.c.overlaps.Add(1)
	}
	e.mu.Lock()
	e.mu.events = append(e.mu.events, event)
	e.mu.Unlock()
	return func() { e.inflight.Add(-1) }
}

// events returns the calls received so far.
func (e *fakeEncoder) events() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return string(e.mu.events)
}

func (e *fakeEncoder) Write(p []byte) (int, error) {
	defer e.enter('w')()
	if e.c.writeErr != nil {
		return 0, e.c.writeErr
	}
	return e.zw.Write(p)
}

func (e *fakeEncoder) Close() error {
	defer e.enter('c')()
	err := e.zw.Close()
	if e.c.closeErr != nil {
		return e.c.closeErr
	}
	return err
}

func (e *fakeEncoder) Reset(w io.Writer) {
	defer e.enter('r')()
	e.zw.Reset(w)
}

// encoderCallOrder matches the call order of an Encoder that is closed before
// every Reset: any writes, a Close, then any number of Reset, writes, Close.
const encoderCallOrder = `^w*c(rw*c)*$`

// healthCheckBody is a root-endpoint response the transport accepts as a
// healthy node, for the health check requests a test server also receives.
const healthCheckBody = `{"name":"n","cluster_name":"c","version":{"number":"3.0.0"}}`

// healthCheckResponse answers a request that is not one of the test's own.
func healthCheckResponse() *http.Response {
	return &http.Response{
		Status:     "MOCK",
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(healthCheckBody)),
	}
}

// wireRecorder is a RoundTripper that gunzips each request body and records the
// plaintext.
type wireRecorder struct {
	mu struct {
		sync.Mutex
		bodies []string
	}
}

func (r *wireRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	// Verify only the test's own requests. Anything else, such as the
	// transport's health check, is answered and not recorded.
	if req.Method != http.MethodPost || req.URL.Path != "/abc" {
		return healthCheckResponse(), nil
	}
	if got := req.Header.Get(headerContentEncoding); got != encodingGzip {
		return nil, fmt.Errorf("Content-Encoding is %q", got)
	}
	zr, err := gzip.NewReader(req.Body)
	if err != nil {
		return nil, err
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.mu.bodies = append(r.mu.bodies, string(plain))
	r.mu.Unlock()
	return &http.Response{Status: "MOCK", StatusCode: http.StatusOK, Body: http.NoBody}, nil
}

// bodies returns the plaintext bodies received so far.
func (r *wireRecorder) bodies() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.mu.bodies...)
}

// newCompressorTransport returns a Transport that compresses with c and sends
// to rt.
func newCompressorTransport(t *testing.T, c Compressor, rt http.RoundTripper) *Transport {
	t.Helper()

	tp, err := New(Config{
		URLs:              []*url.URL{{Scheme: "https", Host: "foo.com"}},
		Compressor:        c,
		DisableRetry:      true,
		NodeStatsInterval: -1,
		Transport:         rt,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tp.Close() })
	return tp
}

// streamBody sends body as a POST through tp.
func streamBody(tp *Transport, body string) error {
	req, err := http.NewRequest(http.MethodPost, "/abc", strings.NewReader(body))
	if err != nil {
		return err
	}
	res, err := tp.Stream(req)
	if err != nil {
		return err
	}
	return res.Body.Close()
}

func TestNewCompressorValidation(t *testing.T) {
	t.Parallel()

	gz, err := GZip(gzip.BestSpeed)
	require.NoError(t, err)

	tests := []struct {
		name         string
		compressor   Compressor
		legacyFlag   bool
		wantErr      error  // the error New must wrap, when set
		wantErrText  string // text the error must contain, when set
		wantEncoding string // the Content-Encoding the transport sends; "" means compression is off
	}{
		{name: "empty encoding", compressor: &fakeCompressor{}, wantErrText: `"" is not a valid HTTP token`},
		{name: "space in encoding", compressor: &fakeCompressor{encoding: "gz ip"}, wantErrText: `"gz ip" is not a valid HTTP token`},
		{name: "line break in encoding", compressor: &fakeCompressor{encoding: "gzip\r\n"}, wantErrText: "is not a valid HTTP token"},
		{name: "non-ASCII encoding", compressor: &fakeCompressor{encoding: "gzïp"}, wantErrText: "is not a valid HTTP token"},
		{name: "separator in encoding", compressor: &fakeCompressor{encoding: "gzip,br"}, wantErrText: "is not a valid HTTP token"},
		{name: "NewEncoder fails", compressor: &fakeCompressor{encoding: "x-custom", newErr: errFakeNew}, wantErr: errFakeNew},
		{
			name:        "NewEncoder returns a nil Encoder",
			compressor:  &fakeCompressor{encoding: "x-custom", nilEncoder: true},
			wantErrText: "nil Encoder",
		},
		{name: "probe Close fails", compressor: &fakeCompressor{encoding: "x-custom", closeErr: errFakeClose}, wantErr: errFakeClose},
		{name: "custom token", compressor: &fakeCompressor{encoding: "x-custom"}, wantEncoding: "x-custom"},
		{name: "token with every punctuation byte", compressor: &fakeCompressor{encoding: "!#$%&'*+-.^_`|~"}, wantEncoding: "!#$%&'*+-.^_`|~"},
		{name: "GZip", compressor: gz, wantEncoding: encodingGzip},
		{name: "None", compressor: None()},
		{name: "None overrides the legacy flag", compressor: None(), legacyFlag: true},
		{name: "nil", compressor: nil},
		{name: "nil with the legacy flag", compressor: nil, legacyFlag: true, wantEncoding: encodingGzip},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tp, err := New(Config{
				URLs:                []*url.URL{{Scheme: "https", Host: "foo.com"}},
				Compressor:          tt.compressor,
				CompressRequestBody: tt.legacyFlag,
				NodeStatsInterval:   -1,
				Transport:           &wireRecorder{},
			})
			if tt.wantErr != nil || tt.wantErrText != "" {
				require.Error(t, err)
				require.Nil(t, tp)
				if tt.wantErr != nil {
					require.ErrorIs(t, err, tt.wantErr)
				}
				if tt.wantErrText != "" {
					require.ErrorContains(t, err, tt.wantErrText)
				}
				return
			}
			require.NoError(t, err)
			t.Cleanup(func() { _ = tp.Close() })

			if tt.wantEncoding == "" {
				require.Nil(t, tp.compressor)
				return
			}
			require.NotNil(t, tp.compressor)
			require.Equal(t, tt.wantEncoding, tp.compressor.encoding)
		})
	}
}

// TestCompressorEncoderLifecycle pins the call order the Encoder godoc
// promises: an Encoder is closed before it is reset for the next body, and every
// body reaches the wire intact. sync.Pool may drop a Put, so the rounds make a
// stream that never gets a reset Encoder vanishingly unlikely, and the call
// order is checked on every Encoder created.
func TestCompressorEncoderLifecycle(t *testing.T) {
	t.Parallel()

	const rounds = 32

	c := &fakeCompressor{encoding: encodingGzip}
	rec := &wireRecorder{}
	tp := newCompressorTransport(t, c, rec)

	want := make([]string, 0, rounds)
	for i := range rounds {
		body := fmt.Sprintf("body %d", i)
		want = append(want, body)
		require.NoError(t, streamBody(tp, body))
	}

	require.Equal(t, want, rec.bodies())
	var resets int
	for i, e := range c.encoders() {
		events := e.events()
		require.Regexp(t, encoderCallOrder, events, "encoder %d", i)
		resets += strings.Count(events, "r")
	}
	require.Positive(t, resets, "no Encoder was reset, so the pool never reused one")
}

// TestCompressorEncoderDiscardedOnError verifies that an Encoder whose Write or
// Close failed is closed once, never reset or reused, and does not stop the next
// request from compressing.
func TestCompressorEncoderDiscardedOnError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fail func(c *fakeCompressor, err error)
		err  error
	}{
		{name: "Write fails", fail: func(c *fakeCompressor, err error) { c.writeErr = err }, err: errFakeWrite},
		{name: "Close fails", fail: func(c *fakeCompressor, err error) { c.closeErr = err }, err: errFakeClose},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &fakeCompressor{encoding: encodingGzip}
			rec := &wireRecorder{}
			tp := newCompressorTransport(t, c, rec)

			// Encoders New created to validate c are not the request's.
			created := len(c.encoders())

			tt.fail(c, tt.err)
			require.ErrorIs(t, streamBody(tp, "failing body"), tt.err)
			failed := c.encoders()[created]

			tt.fail(c, nil)
			require.NoError(t, streamBody(tp, "next body"))

			require.Equal(t, []string{"next body"}, rec.bodies())
			require.Len(t, c.encoders(), created+2, "the next request must not reuse the failed encoder")
			require.Equal(t, "wc", failed.events())
		})
	}
}

// TestCompressorConcurrentStreams verifies that concurrent requests never share
// an Encoder: no call on an Encoder overlaps another, and every body round-trips.
// Run under -race, which also flags a writer shared between goroutines.
func TestCompressorConcurrentStreams(t *testing.T) {
	t.Parallel()

	const (
		workers   = 8
		perWorker = 10
	)

	c := &fakeCompressor{encoding: encodingGzip}
	rec := &wireRecorder{}
	tp := newCompressorTransport(t, c, rec)

	var g errgroup.Group
	want := make([]string, 0, workers*perWorker)
	body := func(worker, i int) string {
		return strings.Repeat(fmt.Sprintf("worker-%d-request-%d|", worker, i), 64)
	}
	for w := range workers {
		for i := range perWorker {
			want = append(want, body(w, i))
		}
		g.Go(func() error {
			for i := range perWorker {
				if err := streamBody(tp, body(w, i)); err != nil {
					return err
				}
			}
			return nil
		})
	}

	require.NoError(t, g.Wait())
	require.Equal(t, int64(0), c.overlaps.Load())
	require.ElementsMatch(t, want, rec.bodies())
	for i, e := range c.encoders() {
		require.Regexp(t, encoderCallOrder, e.events(), "encoder %d", i)
	}
}
