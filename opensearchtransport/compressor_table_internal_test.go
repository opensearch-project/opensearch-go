// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// encodingDefault is the Content-Encoding of the Default Compressor the tests
// use, so the wire tells it from the rule Compressor's encodingGzip.
const encodingDefault = "x-default"

// newTestTable returns a table with one rule, "3.0.0" to a gzip Compressor, and
// def as Default.
func newTestTable(def Compressor) *CompressorTable {
	return &CompressorTable{
		Rules:   []CompressorRule{{MinVersion: "3.0.0", Compressor: &fakeCompressor{encoding: encodingGzip}}},
		Default: def,
	}
}

// wireBody is what the server received for one request.
type wireBody struct {
	encoding string
	body     string // plaintext
}

// tableCluster is a RoundTripper for one URL in front of a cluster: GET /_nodes
// lists the cluster's node versions, or answers nodesStatus when it is set, and
// each POST /abc is recorded.
type tableCluster struct {
	probes atomic.Int64 // GET /_nodes requests received

	// onPost, when set, runs for each POST /abc and returns its status.
	onPost func() int

	mu struct {
		sync.Mutex
		versions    []string
		nodesStatus int
		nodesErr    error         // when set, GET /_nodes fails with it
		nodesBody   string        // when set, GET /_nodes answers 200 with it
		hold        chan struct{} // when set, GET /_nodes waits for it to close
		wire        []wireBody
	}
}

func newTableCluster(nodesStatus int, versions ...string) *tableCluster {
	c := &tableCluster{}
	c.mu.versions = versions
	c.mu.nodesStatus = nodesStatus
	return c
}

func (c *tableCluster) setVersions(versions ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mu.versions = versions
}

// setNodesAnswer makes GET /_nodes fail with err when it is set, or answer 200
// with body when it is not empty.
func (c *tableCluster) setNodesAnswer(err error, body string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mu.nodesErr, c.mu.nodesBody = err, body
}

// holdNodes makes GET /_nodes wait until the returned release func is called.
func (c *tableCluster) holdNodes() func() {
	ch := make(chan struct{})
	c.mu.Lock()
	c.mu.hold = ch
	c.mu.Unlock()
	return func() { close(ch) }
}

// streamHeld sends body while the probe it starts is held, so the request sees
// only what the table knew before it.
func streamHeld(tp *Transport, cluster *tableCluster, body string) error {
	release := cluster.holdNodes()
	defer release()
	return streamBody(tp, body)
}

// nodesResponse is the cluster's answer to GET /_nodes.
func (c *tableCluster) nodesResponse() (*http.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.mu.nodesErr != nil {
		return nil, c.mu.nodesErr
	}
	if c.mu.nodesBody != "" {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(c.mu.nodesBody))}, nil
	}
	if c.mu.nodesStatus != 0 {
		return &http.Response{StatusCode: c.mu.nodesStatus, Body: http.NoBody}, nil
	}
	nodes := map[string]map[string]string{}
	for i, v := range c.mu.versions {
		nodes[fmt.Sprintf("node-%d", i)] = map[string]string{"version": v}
	}
	body, err := json.Marshal(map[string]any{"nodes": nodes})
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
}

func (c *tableCluster) RoundTrip(req *http.Request) (*http.Response, error) {
	// A RoundTripper closes the request body, which releases a reader over a
	// body held in memory.
	if req.Body != nil {
		defer req.Body.Close()
	}
	if req.Method == http.MethodGet && req.URL.Path == "/_nodes" {
		c.probes.Add(1)
		if got := req.URL.Query().Get("filter_path"); got != "nodes.*.version" {
			return nil, fmt.Errorf("filter_path is %q", got)
		}
		c.mu.Lock()
		hold := c.mu.hold
		c.mu.hold = nil
		c.mu.Unlock()
		if hold != nil {
			<-hold
		}

		return c.nodesResponse()
	}
	if req.Method != http.MethodPost || req.URL.Path != "/abc" {
		return healthCheckResponse(), nil
	}

	w := wireBody{encoding: req.Header.Get(headerContentEncoding)}
	var src io.Reader = req.Body
	if w.encoding != "" {
		zr, err := gzip.NewReader(req.Body)
		if err != nil {
			return nil, err
		}
		src = zr
	}
	plain, err := io.ReadAll(src)
	if err != nil {
		return nil, err
	}
	if w.encoding == "" && int64(len(plain)) != req.ContentLength {
		return nil, fmt.Errorf("ContentLength %d for a %d-byte body", req.ContentLength, len(plain))
	}
	w.body = string(plain)

	c.mu.Lock()
	c.mu.wire = append(c.mu.wire, w)
	c.mu.Unlock()
	status := http.StatusOK
	if c.onPost != nil {
		status = c.onPost()
	}
	return &http.Response{StatusCode: status, Body: http.NoBody}, nil
}

func (c *tableCluster) wire() []wireBody {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]wireBody(nil), c.mu.wire...)
}

// waitResolved waits for tp's latest probe to answer with the Compressor want.
func waitResolved(t *testing.T, tp *Transport, want *requestCompressor) {
	t.Helper()
	require.Eventually(t, func() bool {
		r := tp.table.result.Load()
		return r != nil && r.rc == want && r.startedAt == tp.table.probedAt.Load()
	}, 5*time.Second, 10*time.Millisecond)
}

// ageProbe moves tp's latest probe, and the result it found, one
// versionProbeInterval into the past, as if that much time had passed.
func ageProbe(tp *Transport) {
	tbl := tp.table
	r := *tbl.result.Load()
	r.startedAt -= int64(versionProbeInterval)
	tbl.probedAt.Store(r.startedAt)
	tbl.result.Store(&r)
}

// setResolved stores rc as what tp's latest probe found.
func setResolved(tp *Transport, rc *requestCompressor) {
	tp.table.result.Store(&probeResult{rc: rc, startedAt: tp.table.probedAt.Load()})
}

// newTableTransport returns a Transport with one URL in front of cluster, making
// up to maxRetries retries.
func newTableTransport(t *testing.T, table *CompressorTable, cluster *tableCluster, maxRetries int) *Transport {
	t.Helper()

	tp, err := New(Config{
		URLs:              []*url.URL{{Scheme: "https", Host: "lb"}},
		CompressorTable:   table,
		CompressMinBytes:  -1,
		MaxRetries:        maxRetries,
		DisableRetry:      maxRetries == 0,
		NodeStatsInterval: -1,
		Transport:         cluster,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tp.Close() })
	return tp
}

// newSeedFallbackTransport returns a Transport with one URL in front of cluster
// and a Router that finds no connection, so every request goes through the seed
// fallback.
func newSeedFallbackTransport(t *testing.T, table *CompressorTable, cluster *tableCluster) *Transport {
	t.Helper()

	tp, err := New(Config{
		URLs:              []*url.URL{{Scheme: "https", Host: "lb"}},
		CompressorTable:   table,
		CompressMinBytes:  -1,
		DisableRetry:      true,
		HealthCheck:       NoOpHealthCheck,
		NodeStatsInterval: -1,
		Router:            &emptyRouter{},
		Transport:         cluster,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tp.Close() })
	return tp
}

// TestCompressorTable checks that a table's rule Compressor applies only once GET
// /_nodes reports versions that all resolve to it. The first request always
// goes out with Default, since the probe it starts is held until the request has
// gone out.
func TestCompressorTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		nodesStatus  int    // answer to GET /_nodes; 0 lists versions
		nodesErr     error  // GET /_nodes fails with it
		nodesBody    string // GET /_nodes answers 200 with it
		versions     []string
		wantRule     bool   // the probe resolves to the rule Compressor, not Default
		wantEncoding string // for the request after the probe
	}{
		{name: "every node supported", versions: []string{"3.0.0", "3.1.0"}, wantRule: true, wantEncoding: encodingGzip},
		{name: "one older node behind the URL", versions: []string{"3.0.0", "2.19.6", "3.1.0"}, wantEncoding: encodingDefault},
		{name: "node without a version", versions: []string{"3.0.0", ""}, wantEncoding: encodingDefault},
		{name: "no nodes listed", wantEncoding: encodingDefault},
		{name: "probe forbidden", nodesStatus: http.StatusForbidden, wantEncoding: encodingDefault},
		{name: "probe transport error", nodesErr: errFakeRoundTrip, wantEncoding: encodingDefault},
		{name: "probe answers invalid JSON", nodesBody: "{not json", wantEncoding: encodingDefault},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cluster := newTableCluster(tt.nodesStatus, tt.versions...)
			cluster.setNodesAnswer(tt.nodesErr, tt.nodesBody)
			tp := newTableTransport(t, newTestTable(&fakeCompressor{encoding: encodingDefault}), cluster, 0)

			want := tp.table.defaultCompressor
			if tt.wantRule {
				want = tp.table.rules[0].rc
			}

			require.NoError(t, streamHeld(tp, cluster, "first"))
			waitResolved(t, tp, want)
			require.NoError(t, streamBody(tp, "second"))

			require.Equal(t, []wireBody{{encoding: encodingDefault, body: "first"}, {encoding: tt.wantEncoding, body: "second"}}, cluster.wire())
		})
	}
}

// TestCompressorTableDefaultNil checks that a nil Default sends bodies
// uncompressed, before the first probe answers and for a cluster no rule fits.
func TestCompressorTableDefaultNil(t *testing.T) {
	t.Parallel()

	cluster := newTableCluster(0, "2.19.6")
	tp := newTableTransport(t, newTestTable(nil), cluster, 0)

	require.NoError(t, streamBody(tp, "first"))
	waitResolved(t, tp, tp.table.defaultCompressor)
	require.NoError(t, streamBody(tp, "second"))

	require.Equal(t, []wireBody{{body: "first"}, {body: "second"}}, cluster.wire())
}

// TestCompressorTableMinBytes checks that CompressMinBytes applies to the rule
// Compressors and to Default alike.
func TestCompressorTableMinBytes(t *testing.T) {
	t.Parallel()

	cluster := newTableCluster(0, "3.0.0")
	tp, err := New(Config{
		URLs:              []*url.URL{{Scheme: "https", Host: "lb"}},
		CompressorTable:   newTestTable(&fakeCompressor{encoding: encodingDefault}),
		CompressMinBytes:  8,
		NodeStatsInterval: -1,
		Transport:         cluster,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tp.Close() })

	// Default while the probe is held, then the rule Compressor once it answers.
	release := cluster.holdNodes()
	require.NoError(t, streamBody(tp, "tiny"))
	require.NoError(t, streamBody(tp, "long enough"))
	release()
	waitResolved(t, tp, tp.table.rules[0].rc)
	require.NoError(t, streamBody(tp, "tiny"))
	require.NoError(t, streamBody(tp, "long enough"))

	require.Equal(t, []wireBody{
		{body: "tiny"},
		{encoding: encodingDefault, body: "long enough"},
		{body: "tiny"},
		{encoding: encodingGzip, body: "long enough"},
	}, cluster.wire())
}

// TestCompressorTableMixedRules checks that the Compressor applies only when
// every node resolves to the same one: nodes between two rules' versions
// resolve to different Compressors, so the cluster resolves to Default.
func TestCompressorTableMixedRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		versions []string
		wantFrom string // a version that resolves to the Compressor the cluster does
	}{
		{name: "nodes under different rules", versions: []string{"3.2.0", "3.6.0"}, wantFrom: ""},
		{name: "nodes under one rule", versions: []string{"3.6.0", "3.7.0"}, wantFrom: "3.5.0"},
		{name: "nodes under the lower rule", versions: []string{"3.1.0", "3.2.0"}, wantFrom: "3.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cluster := newTableCluster(0, tt.versions...)
			tp := newTableTransport(t, &CompressorTable{
				Rules: []CompressorRule{
					{MinVersion: "3.0.0", Compressor: &fakeCompressor{encoding: "x-a"}},
					{MinVersion: "3.5.0", Compressor: &fakeCompressor{encoding: "x-b"}},
				},
				Default: &fakeCompressor{encoding: encodingDefault},
			}, cluster, 0)

			require.NoError(t, streamHeld(tp, cluster, "first"))
			waitResolved(t, tp, tp.table.lookup(tt.wantFrom))
		})
	}
}

// TestCompressorTableReprobe checks that the table probes at most once per
// versionProbeInterval, that a result older than that is not used while the
// next probe runs, and that the next probe's result takes over: the rule
// Compressor stops when an older node has joined and resumes once it is gone.
func TestCompressorTableReprobe(t *testing.T) {
	t.Parallel()

	cluster := newTableCluster(0, "3.0.0")
	tp := newTableTransport(t, newTestTable(None()), cluster, 0)
	rule := tp.table.rules[0].rc

	require.NoError(t, streamHeld(tp, cluster, "first"))
	waitResolved(t, tp, rule)
	require.NoError(t, streamBody(tp, "second"))
	require.NoError(t, streamBody(tp, "third"))
	require.Equal(t, int64(1), cluster.probes.Load(), "probed again within versionProbeInterval")

	// An older node joins and the interval lapses. The next probe is held, so
	// "fourth" goes out with Default while the old result is all there is.
	cluster.setVersions("3.0.0", "2.19.6")
	release := cluster.holdNodes()
	ageProbe(tp)
	require.NoError(t, streamBody(tp, "fourth"))
	release()
	waitResolved(t, tp, tp.table.defaultCompressor)
	require.NoError(t, streamBody(tp, "fifth"))

	// The older node leaves.
	cluster.setVersions("3.0.0")
	ageProbe(tp)
	require.NoError(t, streamHeld(tp, cluster, "sixth"))
	waitResolved(t, tp, rule)
	require.NoError(t, streamBody(tp, "seventh"))

	require.Equal(t, int64(3), cluster.probes.Load())
	require.Equal(t, []wireBody{
		{body: "first"},
		{encoding: encodingGzip, body: "second"},
		{encoding: encodingGzip, body: "third"},
		{body: "fourth"},
		{body: "fifth"},
		{body: "sixth"},
		{encoding: encodingGzip, body: "seventh"},
	}, cluster.wire())
}

// TestCompressorTableRetryAfterResolutionChanges checks that a retry made after
// the cluster resolved to Default goes out with Default's encoding, not the
// rule Compressor's the attempt before it carried.
func TestCompressorTableRetryAfterResolutionChanges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		def       Compressor
		wantRetry wireBody
	}{
		{name: "Default nil", def: nil, wantRetry: wireBody{body: "retried"}},
		{
			name:      "Default compresses",
			def:       &fakeCompressor{encoding: encodingDefault},
			wantRetry: wireBody{encoding: encodingDefault, body: "retried"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cluster := newTableCluster(0, "3.0.0")
			tp := newTableTransport(t, newTestTable(tt.def), cluster, 1)

			require.NoError(t, streamBody(tp, "prime"))
			waitResolved(t, tp, tp.table.rules[0].rc)

			// The first attempt uses the rule Compressor; it answers 503 and the
			// cluster resolves to Default, so the retry must use that.
			var posts atomic.Int64
			cluster.onPost = func() int {
				if posts.Add(1) == 1 {
					setResolved(tp, tp.table.defaultCompressor)
					return http.StatusServiceUnavailable
				}
				return http.StatusOK
			}
			require.NoError(t, streamBody(tp, "retried"))

			wire := cluster.wire()
			require.Equal(t, []wireBody{{encoding: encodingGzip, body: "retried"}, tt.wantRetry}, wire[len(wire)-2:])
		})
	}
}

// heldBody is a body held in memory, as the bulk indexer's is: it counts the
// reads of the body itself and the references to it, the body's own and one
// for each reader NewReader returned, that have not been closed.
type heldBody struct {
	data  string
	r     strings.Reader
	reads atomic.Int64
	open  atomic.Int64
	once  sync.Once
}

func newHeldBody(s string) *heldBody {
	b := &heldBody{data: s}
	b.r.Reset(s)
	b.open.Store(1)
	return b
}

func (b *heldBody) Read(p []byte) (int, error) {
	b.reads.Add(1)
	return b.r.Read(p)
}

func (b *heldBody) Len() int { return b.r.Len() }

func (b *heldBody) NewReader() io.Reader {
	b.open.Add(1)
	return &heldReader{Reader: strings.NewReader(b.data), b: b}
}

//nolint:unparam // io.Closer's signature.
func (b *heldBody) Close() error {
	b.once.Do(func() { b.open.Add(-1) })
	return nil
}

type heldReader struct {
	io.Reader
	b    *heldBody
	once sync.Once
}

//nolint:unparam // io.Closer's signature.
func (r *heldReader) Close() error {
	r.once.Do(func() { r.b.open.Add(-1) })
	return nil
}

// TestCompressorTableHeldBody checks that a body already held in memory is
// encoded where it is rather than copied, for the rule Compressor and for a retry
// with Default, and that every reference to it is released once the request
// returns.
func TestCompressorTableHeldBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		def       Compressor
		wantRetry wireBody // what each retry, made after the cluster resolved to Default, sends
	}{
		{name: "Default nil", def: nil, wantRetry: wireBody{body: "held"}},
		{
			name:      "Default compresses",
			def:       &fakeCompressor{encoding: encodingDefault},
			wantRetry: wireBody{encoding: encodingDefault, body: "held"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cluster := newTableCluster(0, "3.0.0")
			tp := newTableTransport(t, newTestTable(tt.def), cluster, 2)

			require.NoError(t, streamBody(tp, "prime"))
			waitResolved(t, tp, tp.table.rules[0].rc)

			// The first attempt uses the rule Compressor and the cluster then
			// resolves to Default, so both retries use that; the second replays
			// the first's body.
			var posts atomic.Int64
			cluster.onPost = func() int {
				switch posts.Add(1) {
				case 1:
					setResolved(tp, tp.table.defaultCompressor)
					return http.StatusServiceUnavailable
				case 2:
					return http.StatusServiceUnavailable
				}
				return http.StatusOK
			}
			body := newHeldBody("held")
			req, err := http.NewRequest(http.MethodPost, "/abc", body)
			require.NoError(t, err)
			res, err := tp.Stream(req)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())

			// After "prime".
			require.Equal(t, []wireBody{{encoding: encodingGzip, body: "held"}, tt.wantRetry, tt.wantRetry}, cluster.wire()[1:])
			require.Zero(t, body.reads.Load(), "the body was copied")
			require.Zero(t, body.open.Load(), "a reference to the body was not released")
		})
	}
}

// TestCompressorTableSeedFallbackHeldBody checks that a body held in memory,
// sent through the seed fallback once the cluster-wide read has resolved to the
// rule Compressor, is encoded where it is and releases every reference.
func TestCompressorTableSeedFallbackHeldBody(t *testing.T) {
	t.Parallel()

	cluster := newTableCluster(0, "3.0.0")
	tp := newSeedFallbackTransport(t, newTestTable(&fakeCompressor{encoding: encodingDefault}), cluster)

	require.NoError(t, streamBody(tp, "prime"))
	waitResolved(t, tp, tp.table.rules[0].rc)

	body := newHeldBody("held")
	req, err := http.NewRequest(http.MethodPost, "/abc", body)
	require.NoError(t, err)
	res, err := tp.Stream(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.True(t, tp.discoveryNeeded.Load(), "the request did not go through the seed fallback")
	require.Equal(t, wireBody{encoding: encodingGzip, body: "held"}, cluster.wire()[1])
	require.Zero(t, body.reads.Load(), "the body was copied")
	require.Zero(t, body.open.Load(), "a reference to the body was not released")
}

// TestCompressorTableEncodeError checks that an Encoder that fails on Write
// fails the request with the compress error, that a body that fails on Read
// fails it with the read error, and that a held body's references are all
// released.
func TestCompressorTableEncodeError(t *testing.T) {
	t.Parallel()

	viaPool := func(t *testing.T, table *CompressorTable, cluster *tableCluster) *Transport {
		t.Helper()
		return newTableTransport(t, table, cluster, 0)
	}

	tests := []struct {
		name         string
		held         bool
		unreadable   bool // the body fails every Read
		newTransport func(*testing.T, *CompressorTable, *tableCluster) *Transport
		wantText     string // in the error
	}{
		{name: "held body", held: true, newTransport: viaPool, wantText: "failed to compress request body"},
		{name: "plain body", newTransport: viaPool, wantText: "failed to compress request body"},
		{name: "unreadable body", unreadable: true, newTransport: viaPool, wantText: "failed to read request body"},
		{name: "seed fallback", newTransport: newSeedFallbackTransport, wantText: "failed to compress request body"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// The first request uses Default, since the probe it starts is held
			// until the request has failed.
			cluster := newTableCluster(0, "3.0.0")
			tp := tt.newTransport(t, newTestTable(&fakeCompressor{encoding: encodingDefault, writeErr: errFakeWrite}), cluster)

			held := newHeldBody("held")
			var body io.Reader = strings.NewReader("plain")
			switch {
			case tt.held:
				body = held
			case tt.unreadable:
				body = iotestErrReader{}
			}
			req, err := http.NewRequest(http.MethodPost, "/abc", body)
			require.NoError(t, err)
			release := cluster.holdNodes()
			//nolint:bodyclose // the request fails before a response exists
			_, err = tp.Stream(req)
			release()
			require.ErrorIs(t, err, errFakeWrite)
			require.ErrorContains(t, err, tt.wantText)
			require.Empty(t, cluster.wire())
			if tt.held {
				require.Zero(t, held.open.Load(), "a reference to the body was not released")
			}
		})
	}
}

// TestCompressorTableLookup checks that lookup returns the Compressor of the
// highest MinVersion at or below a server version, and Default when there is
// none or the version is not semver.
func TestCompressorTableLookup(t *testing.T) {
	t.Parallel()

	tbl, err := newCompressorTable(&CompressorTable{
		// Out of order, and with and without the "v".
		Rules: []CompressorRule{
			{MinVersion: "3.5.0", Compressor: &fakeCompressor{encoding: "x-b"}},
			{MinVersion: "v3.0.0", Compressor: &fakeCompressor{encoding: "x-a"}},
		},
		Default: &fakeCompressor{encoding: "x-default"},
	})
	require.NoError(t, err)

	tests := []struct {
		name    string
		version string
		want    string
	}{
		{name: "exact lower floor", version: "3.0.0", want: "x-a"},
		{name: "between floors", version: "3.4.9", want: "x-a"},
		{name: "exact upper floor", version: "3.5.0", want: "x-b"},
		{name: "above all", version: "9.1.0", want: "x-b"},
		{name: "below all", version: "2.19.6", want: "x-default"},
		{name: "pre-release of the lower floor", version: "3.0.0-SNAPSHOT", want: "x-default"},
		{name: "pre-release above the lower floor", version: "3.5.0-SNAPSHOT", want: "x-a"},
		{name: "with v", version: "v3.5.0", want: "x-b"},
		{name: "not semver", version: "three", want: "x-default"},
		{name: "empty", version: "", want: "x-default"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, tbl.lookup(tt.version).encoding)
		})
	}
}

// TestCompressorTableValidation checks that New rejects a table or Config that
// is not usable.
func TestCompressorTableValidation(t *testing.T) {
	t.Parallel()

	ok := &fakeCompressor{encoding: encodingGzip}

	tests := []struct {
		name        string
		compressor  Compressor
		table       *CompressorTable
		wantErr     error  // the error New must wrap, when set
		wantErrText string // text the error must contain
	}{
		{
			name:        "invalid MinVersion",
			table:       &CompressorTable{Rules: []CompressorRule{{MinVersion: "latest", Compressor: ok}}},
			wantErrText: `MinVersion "latest"`,
		},
		{
			name:        "empty MinVersion",
			table:       &CompressorTable{Rules: []CompressorRule{{MinVersion: "", Compressor: ok}}},
			wantErrText: `MinVersion ""`,
		},
		{
			name: "duplicate MinVersion after normalization",
			table: &CompressorTable{Rules: []CompressorRule{
				{MinVersion: "v3.0.0", Compressor: ok},
				{MinVersion: "3.0.0", Compressor: ok},
			}},
			wantErrText: `duplicate MinVersion "3.0.0"`,
		},
		{name: "no rules", table: &CompressorTable{Default: ok}, wantErrText: "no Rules"},
		{
			name:        "broken rule Compressor",
			table:       &CompressorTable{Rules: []CompressorRule{{MinVersion: "3.0.0", Compressor: &fakeCompressor{encoding: "gz ip"}}}},
			wantErrText: `content encoding "gz ip"`,
		},
		{
			name: "rule Compressor NewEncoder fails",
			table: &CompressorTable{Rules: []CompressorRule{{
				MinVersion: "3.0.0",
				Compressor: &fakeCompressor{encoding: "x-custom", newErr: errFakeNew},
			}}},
			wantErr:     errFakeNew,
			wantErrText: "Rules[0]",
		},
		{
			name: "broken Default",
			table: &CompressorTable{
				Rules:   []CompressorRule{{MinVersion: "3.0.0", Compressor: ok}},
				Default: &fakeCompressor{encoding: "gz ip"},
			},
			wantErrText: `Default: opensearchtransport: Compressor content encoding "gz ip"`,
		},
		{
			name:        "Compressor and CompressorTable together",
			compressor:  ok,
			table:       newTestTable(nil),
			wantErrText: "Compressor and CompressorTable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tp, err := New(Config{
				URLs:              []*url.URL{{Scheme: "https", Host: "lb"}},
				Compressor:        tt.compressor,
				CompressorTable:   tt.table,
				CompressMinBytes:  -1,
				NodeStatsInterval: -1,
				Transport:         newTableCluster(0),
			})
			require.Nil(t, tp)
			require.ErrorContains(t, err, tt.wantErrText)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			}
		})
	}
}

// TestCompressorTableNilEntries checks that a nil Compressor in a rule or as
// Default means uncompressed.
func TestCompressorTableNilEntries(t *testing.T) {
	t.Parallel()

	tbl, err := newCompressorTable(&CompressorTable{Rules: []CompressorRule{{MinVersion: "3.0.0"}}})
	require.NoError(t, err)
	require.Empty(t, tbl.lookup("3.1.0").encoding)
	require.Empty(t, tbl.lookup("2.0.0").encoding)
}

// encodes returns how many bodies c's encoders have finished, counted by their
// Close calls, which includes the one validating c when the transport is built.
func encodes(c *fakeCompressor) int {
	n := 0
	for _, e := range c.encoders() {
		n += strings.Count(e.events(), "c")
	}
	return n
}

// TestCompressorTableEncodesOnlyWhenCompressorChanges checks that attempts
// resolving to the same Compressor share one encoding of the body, and that an
// attempt resolving to another Compressor encodes it again.
func TestCompressorTableEncodesOnlyWhenCompressorChanges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		changes         bool // the cluster resolves to Default after the first attempt
		wantRuleEncodes int
		wantDefEncodes  int
	}{
		{name: "same Compressor", changes: false, wantRuleEncodes: 1, wantDefEncodes: 0},
		{name: "Compressor changes", changes: true, wantRuleEncodes: 1, wantDefEncodes: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rule := &fakeCompressor{encoding: encodingGzip}
			def := &fakeCompressor{encoding: encodingDefault}
			cluster := newTableCluster(0, "3.0.0")
			tp := newTableTransport(t, &CompressorTable{
				Rules:   []CompressorRule{{MinVersion: "3.0.0", Compressor: rule}},
				Default: def,
			}, cluster, 2)

			require.NoError(t, streamBody(tp, "prime"))
			waitResolved(t, tp, tp.table.rules[0].rc)
			ruleBefore, defBefore := encodes(rule), encodes(def)

			// Three attempts: the first two answer 503.
			var posts atomic.Int64
			cluster.onPost = func() int {
				if posts.Add(1) == 1 && tt.changes {
					setResolved(tp, tp.table.defaultCompressor)
				}
				if posts.Load() < 3 {
					return http.StatusServiceUnavailable
				}
				return http.StatusOK
			}
			require.NoError(t, streamBody(tp, "retried"))

			require.Equal(t, int64(3), posts.Load())
			require.Equal(t, tt.wantRuleEncodes, encodes(rule)-ruleBefore)
			require.Equal(t, tt.wantDefEncodes, encodes(def)-defBefore)
		})
	}
}

// TestCompressorTableOverridesCompressRequestBody checks that a body goes out
// with the table's encoding, not the deprecated flag's gzip.
func TestCompressorTableOverridesCompressRequestBody(t *testing.T) {
	t.Parallel()

	cluster := newTableCluster(0, "3.0.0")
	tp, err := New(Config{
		URLs:                []*url.URL{{Scheme: "https", Host: "lb"}},
		CompressorTable:     newTestTable(&fakeCompressor{encoding: encodingDefault}),
		CompressRequestBody: true,
		CompressMinBytes:    -1,
		DisableRetry:        true,
		NodeStatsInterval:   -1,
		Transport:           cluster,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tp.Close() })

	require.NoError(t, streamHeld(tp, cluster, "first"))
	require.Equal(t, []wireBody{{encoding: encodingDefault, body: "first"}}, cluster.wire())
}

// TestCompressorTableSeedFallback checks that a request served by the seed
// fallback carries the table's Compressor: Default before the versions are
// read, the rule's once they are.
func TestCompressorTableSeedFallback(t *testing.T) {
	t.Parallel()

	cluster := newTableCluster(0, "3.0.0")
	tp := newSeedFallbackTransport(t, newTestTable(&fakeCompressor{encoding: encodingDefault}), cluster)

	require.NoError(t, streamHeld(tp, cluster, "first"))
	waitResolved(t, tp, tp.table.rules[0].rc)
	require.NoError(t, streamBody(tp, "second"))

	require.Equal(t, []wireBody{{encoding: encodingDefault, body: "first"}, {encoding: encodingGzip, body: "second"}}, cluster.wire())
	require.True(t, tp.discoveryNeeded.Load(), "the requests did not go through the seed fallback")
}

// TestReadBody checks that readBody returns the whole body whatever size it is
// given, and the reader's error.
func TestReadBody(t *testing.T) {
	t.Parallel()

	const body = "0123456789"

	tests := []struct {
		name    string
		size    int64
		r       io.Reader
		want    string
		wantErr error
	}{
		{name: "exact size", size: 10, r: strings.NewReader(body), want: body},
		{name: "size too large", size: 64, r: strings.NewReader(body), want: body},
		{name: "size too small", size: 4, r: strings.NewReader(body), want: body},
		{name: "no size", size: 0, r: strings.NewReader(body), want: body},
		{name: "unknown size", size: -1, r: strings.NewReader(body), want: body},
		{name: "empty body", size: 10, r: strings.NewReader(""), want: ""},
		{name: "read error", size: 10, r: io.MultiReader(strings.NewReader("01"), iotestErrReader{}), wantErr: errFakeWrite},
		{name: "read error past size", size: 2, r: io.MultiReader(strings.NewReader("0123"), iotestErrReader{}), wantErr: errFakeWrite},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := readBody(tt.r, tt.size)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, string(got))
		})
	}
}

// iotestErrReader fails every read with errFakeWrite.
type iotestErrReader struct{}

func (iotestErrReader) Read([]byte) (int, error) { return 0, errFakeWrite }

// TestCompressorFor checks which version source resolves a Connection: a seed
// or rewritten address reads the whole cluster with GET /_nodes, and a node
// found by discovery uses its own version without any read.
func TestCompressorFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		seed         bool
		rewritten    bool
		connVersion  string
		nodeVersions []string // what GET /_nodes lists
		wantRule     bool
		wantProbes   int64
	}{
		{name: "seed uses the cluster result", seed: true, connVersion: "2.19.6", nodeVersions: []string{"3.1.0"}, wantRule: true, wantProbes: 1},
		{name: "seed with an older node behind it", seed: true, connVersion: "3.1.0", nodeVersions: []string{"3.1.0", "2.19.6"}, wantProbes: 1},
		{
			name: "rewritten uses the cluster result", rewritten: true, connVersion: "2.19.6", nodeVersions: []string{"3.1.0"},
			wantRule: true, wantProbes: 1,
		},
		{
			name: "rewritten with an older node behind it", rewritten: true, connVersion: "3.1.0", nodeVersions: []string{"3.1.0", "2.19.6"},
			wantProbes: 1,
		},
		{name: "discovered 3.1.0", connVersion: "3.1.0", nodeVersions: []string{"2.19.6"}, wantRule: true},
		{name: "discovered 2.19.6", connVersion: "2.19.6", nodeVersions: []string{"3.1.0"}},
		{name: "discovered without a version", nodeVersions: []string{"3.1.0"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cluster := newTableCluster(0, tt.nodeVersions...)
			tp := newTableTransport(t, newTestTable(&fakeCompressor{encoding: encodingDefault}), cluster, 0)
			conn := &Connection{URL: &url.URL{Scheme: "https", Host: "lb"}, seed: tt.seed, Rewritten: tt.rewritten}
			conn.storeVersion(tt.connVersion)

			want := tp.table.defaultCompressor
			if tt.wantRule {
				want = tp.table.rules[0].rc
			}

			if tt.wantProbes > 0 {
				// The first call starts the read, held until it has returned, and gets Default.
				release := cluster.holdNodes()
				require.Same(t, tp.table.defaultCompressor, tp.compressorFor(conn))
				release()
				waitResolved(t, tp, want)
			}
			require.Same(t, want, tp.compressorFor(conn))
			require.Equal(t, tt.wantProbes, cluster.probes.Load())
		})
	}
}

// TestCompressorForVersionChange checks that a discovered node resolves again
// when its version changes, with no read of the cluster.
func TestCompressorForVersionChange(t *testing.T) {
	t.Parallel()

	cluster := newTableCluster(0, "2.19.6")
	tp := newTableTransport(t, newTestTable(&fakeCompressor{encoding: encodingDefault}), cluster, 0)
	conn := &Connection{URL: &url.URL{Scheme: "https", Host: "lb"}}

	conn.storeVersion("2.19.6")
	require.Same(t, tp.table.defaultCompressor, tp.compressorFor(conn))
	conn.storeVersion("3.1.0")
	require.Same(t, tp.table.rules[0].rc, tp.compressorFor(conn))
	conn.storeVersion("2.19.6")
	require.Same(t, tp.table.defaultCompressor, tp.compressorFor(conn))
	require.Zero(t, cluster.probes.Load())
}

// TestCompressedBodyApplyDiscovered checks that an attempt to a discovered node
// running 3.1.0 is encoded with the rule's encoding on the first attempt, with no
// read of the cluster.
func TestCompressedBodyApplyDiscovered(t *testing.T) {
	t.Parallel()

	cluster := newTableCluster(0, "2.19.6")
	tp := newTableTransport(t, newTestTable(&fakeCompressor{encoding: encodingDefault}), cluster, 0)
	conn := &Connection{URL: &url.URL{Scheme: "https", Host: "lb"}}
	conn.storeVersion("3.1.0")

	req, err := http.NewRequest(http.MethodPost, "/abc", strings.NewReader("body"))
	require.NoError(t, err)
	cb := &compressedBody{tr: tp, size: 4, raw: func() io.ReadCloser { return io.NopCloser(strings.NewReader("body")) }}
	require.NoError(t, cb.apply(req, conn))

	require.Equal(t, encodingGzip, req.Header.Get(headerContentEncoding))
	require.Zero(t, cluster.probes.Load())
}

// TestProbedAtIsRelativeToTableStart checks that probedAt counts from the table's
// creation, not from the wall clock, so a clock step cannot make a probe look
// recent or ancient.
func TestProbedAtIsRelativeToTableStart(t *testing.T) {
	t.Parallel()

	cluster := newTableCluster(0, "3.1.0")
	tp := newTableTransport(t, newTestTable(None()), cluster, 0)
	require.Zero(t, tp.table.probedAt.Load())

	require.NoError(t, streamBody(tp, "first"))
	waitResolved(t, tp, tp.table.rules[0].rc)

	require.Positive(t, tp.table.probedAt.Load())
	require.Less(t, tp.table.probedAt.Load(), int64(time.Hour))
}

// TestProbeOlderResultDoesNotReplaceNewer checks that a superseded probe's
// result is dropped when it finally answers, leaving the newer probe's result
// in place.
func TestProbeOlderResultDoesNotReplaceNewer(t *testing.T) {
	t.Parallel()

	cluster := newTableCluster(0, "3.1.0")
	tp := newTableTransport(t, newTestTable(None()), cluster, 0)
	conn := &Connection{URL: &url.URL{Scheme: "https", Host: "lb"}, seed: true}

	// Probe A reads a cluster of 3.1.0 nodes and is held in flight.
	release := cluster.holdNodes()
	tp.probeClusterVersions(conn)
	require.Eventually(t, func() bool {
		cluster.mu.Lock()
		defer cluster.mu.Unlock()
		return cluster.probes.Load() == 1 && cluster.mu.hold == nil
	}, 5*time.Second, time.Millisecond)

	// The interval lapses and probe B finds an older node.
	cluster.setVersions("3.1.0", "2.19.6")
	tp.table.probedAt.Add(-int64(versionProbeInterval))
	tp.probeClusterVersions(conn)
	waitResolved(t, tp, tp.table.defaultCompressor)

	release()
	require.Never(t, func() bool { return tp.table.current() != tp.table.defaultCompressor }, time.Second, 50*time.Millisecond)
	require.Same(t, tp.table.defaultCompressor, tp.table.result.Load().rc)
}
