// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/mod/semver"
)

// Compressor encodes request bodies before they are sent. Set one as
// Config.Compressor: [GZip] returns one backed by the standard library, and a
// type that implements this interface plugs in another codec, such as zstd,
// without the client depending on it.
//
// A Compressor is shared by every request of a Transport, so ContentEncoding
// must return the same value on every call and NewEncoder must be safe for
// concurrent use. New returns an error for a Compressor whose ContentEncoding
// is not a valid HTTP token (RFC 9110, section 5.6.2) or whose
// NewEncoder(io.Discard) or Close fails. NewEncoder must return a non-nil
// Encoder whenever its error is nil.
type Compressor interface {
	// ContentEncoding returns the Content-Encoding value for bodies the
	// Compressor encodes, such as "gzip".
	ContentEncoding() string

	// NewEncoder returns an Encoder that writes the encoded stream to w.
	NewEncoder(w io.Writer) (Encoder, error)
}

// Encoder encodes one request body at a time and is used by one goroutine at a
// time. *gzip.Writer, *flate.Writer and *zlib.Writer satisfy it.
//
// The transport writes a body to the Encoder, then calls Close, which must
// flush all remaining output to the writer. Before reusing a closed Encoder it
// calls Reset with the next destination. If Write or Close returns an error,
// the transport discards the Encoder: it is never Reset or reused. If writing
// the body fails, the transport calls Close once and ignores its result.
type Encoder interface {
	io.WriteCloser

	// Reset redirects a closed Encoder to write a new stream to w.
	Reset(w io.Writer)
}

// CompressorTable chooses a [Compressor] by the OpenSearch version of the
// cluster, for an encoding only some versions decode. Set one as
// Config.CompressorTable.
//
// Each rule applies from its MinVersion up. A version resolves to the rule with
// the highest MinVersion at or below it, or to Default when none is, when the
// version is empty, or when it is not a semantic version. Pre-releases order as
// semver orders them, so 3.0.0-SNAPSHOT is below 3.0.0.
//
// Only the node that receives a request decodes its Content-Encoding, so a
// connection to a node found by discovery uses that node's version, which
// discovery and its health checks keep current. A node whose version is not
// known yet resolves to Default.
//
// A seed URL, or an address an AddressResolverFunc rewrote, may front several
// nodes, so it uses a rule's Compressor only while every node's version
// resolves to that same rule; otherwise it uses Default. The transport reads
// every node's version with GET /_nodes at most once a minute while requests
// are being sent. GET /_nodes lists every node whichever node answers it, so
// this holds behind a load balancer. A read counts only until the next one
// starts. Until a read answers, and when one fails, for example with a 403 for
// a user without the cluster:monitor/nodes/info permission, the cluster
// resolves to Default. The transport logs a debug record each time the outcome
// changes.
type CompressorTable struct {
	// Rules are the version rules, in any order. New returns an error when it
	// is empty, or when two rules have the same MinVersion.
	Rules []CompressorRule

	// Default is the Compressor for a cluster no single rule covers. nil, like
	// [None], sends bodies unmodified.
	Default Compressor
}

// CompressorRule maps the server versions from MinVersion up to a [Compressor].
type CompressorRule struct {
	// MinVersion is the lowest server version the rule covers, such as "3.0.0",
	// with or without a leading "v". New returns an error for a value that is
	// not a semantic version.
	MinVersion string

	// Compressor encodes bodies for the versions the rule covers. nil, like
	// [None], sends them unmodified.
	Compressor Compressor
}

// noneCompressor leaves bodies unmodified. The transport sends bodies as they
// are for it and never calls NewEncoder.
type noneCompressor struct{}

// ContentEncoding returns the empty string, since None leaves bodies unencoded.
func (noneCompressor) ContentEncoding() string { return "" }

// NewEncoder always returns an error, since None has no Encoder.
func (noneCompressor) NewEncoder(io.Writer) (Encoder, error) {
	return nil, errors.New("opensearchtransport: None does not encode")
}

// None returns a Compressor that sends request bodies unmodified. Unlike a nil
// Config.Compressor, it takes precedence over the deprecated
// Config.CompressRequestBody, so it disables compression outright.
func None() Compressor { return noneCompressor{} }

// GZip returns a Compressor that gzips request bodies at the given level, one
// of the compress/gzip constants ([gzip.DefaultCompression],
// [gzip.NoCompression], [gzip.BestSpeed], [gzip.BestCompression],
// [gzip.HuffmanOnly]) or a value between them. It returns an error for a level
// outside that range.
//
// [gzip.DefaultCompression], which the standard library runs as level 6, is
// the usual choice. It is the level the deprecated Config.CompressRequestBody
// uses.
func GZip(level int) (Compressor, error) {
	// Validate once so NewEncoder can ignore the error.
	if _, err := gzip.NewWriterLevel(io.Discard, level); err != nil {
		return nil, fmt.Errorf("opensearchtransport: %w", err)
	}
	return gzipCompressor{level: level}, nil
}

// encodingGzip is the Content-Encoding value of a gzipped body.
const encodingGzip = "gzip"

type gzipCompressor struct {
	level int
}

// ContentEncoding returns "gzip".
func (gzipCompressor) ContentEncoding() string { return encodingGzip }

// NewEncoder returns a gzip writer at the compressor's level that writes to w.
func (c gzipCompressor) NewEncoder(w io.Writer) (Encoder, error) {
	zw, err := gzip.NewWriterLevel(w, c.level)
	if err != nil {
		// Return an untyped nil: a nil *gzip.Writer in an Encoder is not nil.
		return nil, err
	}
	return zw, nil
}

// requestCompressor applies a Compressor to request bodies and owns the pool of
// its closed Encoders. It is safe for concurrent use.
type requestCompressor struct {
	// encoding is the Content-Encoding value sent with compressed bodies, as
	// newRequestCompressor validated it, or empty for [None]: stream checks it
	// to skip compression, so compress is never called on an empty encoding.
	encoding string
	c        Compressor

	// pool holds closed Encoders of c. It has no New func: a miss falls back to
	// c.NewEncoder.
	pool sync.Pool

	// ratio is the last body's compressed size over its raw size, in parts per
	// thousand, or 0 before one; compress sizes its buffer from it.
	ratio atomic.Int64
}

// defaultCompressMinBytes is [Config.CompressMinBytes] when it is 0. The
// measurements behind it are in compress/README.md, "Minimum body size".
const defaultCompressMinBytes = 4096

// newRequestCompressor validates c against the [Compressor] contract and
// returns the compressor the transport applies, so a broken Compressor fails
// when the transport is built rather than on its first request. [None] skips
// validation; see requestCompressor.encoding.
func newRequestCompressor(c Compressor) (*requestCompressor, error) {
	if _, ok := c.(noneCompressor); ok {
		return &requestCompressor{}, nil
	}

	encoding := c.ContentEncoding()
	if !validToken(encoding) {
		return nil, fmt.Errorf("opensearchtransport: Compressor content encoding %q is not a valid HTTP token", encoding)
	}

	// The probe encoder is discarded rather than pooled, so the pool only ever
	// holds Encoders that served a request.
	enc, err := c.NewEncoder(io.Discard)
	if err != nil {
		return nil, fmt.Errorf("opensearchtransport: Compressor NewEncoder: %w", err)
	}
	if enc == nil {
		return nil, errors.New("opensearchtransport: Compressor NewEncoder returned a nil Encoder")
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("opensearchtransport: Compressor Encoder Close: %w", err)
	}

	return &requestCompressor{encoding: encoding, c: c}, nil
}

// newTableCompressor is newRequestCompressor with a nil c meaning [None].
func newTableCompressor(c Compressor) (*requestCompressor, error) {
	if c == nil {
		c = None()
	}
	return newRequestCompressor(c)
}

// compressorTable is a validated [CompressorTable] and the state of the cluster
// reads that resolve it. It is safe for concurrent use.
type compressorTable struct {
	// rules are sorted by minVersion, highest first.
	rules             []tableRule
	defaultCompressor *requestCompressor

	// start is the base of probedAt. It carries a monotonic clock reading, so
	// a wall-clock step cannot make a probe look recent or old.
	start time.Time

	// probedAt is when the latest probe (probeClusterVersions) started, as one
	// more than the nanoseconds since start, so 0 means none has started. result
	// is what the latest probe to answer found, nil until one answers.
	probedAt atomic.Int64
	result   atomic.Pointer[probeResult]
}

// tableRule is a [CompressorRule] with its MinVersion canonical, as semver
// reads it, and its Compressor validated.
type tableRule struct {
	minVersion string
	rc         *requestCompressor
}

// probeResult is what one probeClusterVersions found.
type probeResult struct {
	rc        *requestCompressor // the Compressor every node resolved to, or the Default
	startedAt int64              // the probe's compressorTable.probedAt
}

// newCompressorTable validates t, so a broken table fails when the transport is
// built rather than on its first request.
func newCompressorTable(t *CompressorTable) (*compressorTable, error) {
	if len(t.Rules) == 0 {
		return nil, errors.New("opensearchtransport: CompressorTable has no Rules")
	}

	tbl := &compressorTable{rules: make([]tableRule, 0, len(t.Rules)), start: time.Now()}
	for i, r := range t.Rules {
		v := withV(r.MinVersion)
		if !semver.IsValid(v) {
			return nil, fmt.Errorf("opensearchtransport: CompressorTable Rules[%d]: MinVersion %q is not a semantic version", i, r.MinVersion)
		}
		v = semver.Canonical(v)
		if slices.ContainsFunc(tbl.rules, func(o tableRule) bool { return o.minVersion == v }) {
			return nil, fmt.Errorf("opensearchtransport: CompressorTable Rules[%d]: duplicate MinVersion %q", i, r.MinVersion)
		}
		rc, err := newTableCompressor(r.Compressor)
		if err != nil {
			return nil, fmt.Errorf("opensearchtransport: CompressorTable Rules[%d]: %w", i, err)
		}
		tbl.rules = append(tbl.rules, tableRule{minVersion: v, rc: rc})
	}
	slices.SortFunc(tbl.rules, func(a, b tableRule) int { return semver.Compare(b.minVersion, a.minVersion) })

	def, err := newTableCompressor(t.Default)
	if err != nil {
		return nil, fmt.Errorf("opensearchtransport: CompressorTable Default: %w", err)
	}
	tbl.defaultCompressor = def
	return tbl, nil
}

// withV returns v with a leading "v", which semver requires and the server's
// versions leave out.
func withV(v string) string {
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

// lookup returns the Compressor of the highest rule at or below version, or the
// Default when there is none or version is not semver.
func (t *compressorTable) lookup(version string) *requestCompressor {
	v := withV(version)
	if !semver.IsValid(v) {
		return t.defaultCompressor
	}
	for _, r := range t.rules {
		if semver.Compare(v, r.minVersion) >= 0 {
			return r.rc
		}
	}
	return t.defaultCompressor
}

// current returns the Compressor for the cluster: what the latest probe found, but
// only once it has answered. Once a newer probe starts, the older result no
// longer counts.
func (t *compressorTable) current() *requestCompressor {
	if r := t.result.Load(); r != nil && r.startedAt == t.probedAt.Load() {
		return r.rc
	}
	return t.defaultCompressor
}

// validToken reports whether s is an HTTP token (RFC 9110, section 5.6.2): one
// or more ASCII letters, digits, or the punctuation tchar allows.
func validToken(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		b := s[i]
		switch {
		case 'a' <= b && b <= 'z', 'A' <= b && b <= 'Z', '0' <= b && b <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", b) >= 0:
		default:
			return false
		}
	}
	return true
}

// compress encodes body into a buffer the caller owns: the buffer is never
// pooled, because the request body and GetBody readers keep reading it after
// stream returns. size is the body's length when known, or 0; with it, the
// buffer starts at the size the recent compression ratio predicts, so it does
// not regrow from empty for every request.
func (rqc *requestCompressor) compress(body io.Reader, size int64) (*bytes.Buffer, error) {
	buf := new(bytes.Buffer)
	if size > 0 {
		if permille := rqc.ratio.Load(); permille > 0 {
			// The prediction plus an eighth, so a body that compresses worse than
			// the last ones is less likely to regrow the buffer.
			want := size * permille / 1000
			buf.Grow(int(want + want/8 + compressOverhead))
		}
	}

	enc, err := rqc.encoder(buf)
	if err != nil {
		return nil, err
	}

	if _, err := io.Copy(enc, body); err != nil {
		_ = enc.Close() // the Encoder is discarded; Close releases what it holds
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("closing encoder: %w", err)
	}

	if size > 0 {
		rqc.ratio.Store(int64(buf.Len()) * 1000 / size)
	}
	// Only an Encoder that closed cleanly goes back to the pool.
	rqc.pool.Put(enc)
	return buf, nil
}

// compressOverhead is headroom, not a measured size, for an encoding's fixed
// per-body cost, its headers and checksums, which a ratio taken from large
// bodies leaves out for small ones.
const compressOverhead = 512

// encoder returns a pooled Encoder reset to write to dst, or a new one.
func (rqc *requestCompressor) encoder(dst io.Writer) (Encoder, error) {
	if enc, ok := rqc.pool.Get().(Encoder); ok {
		enc.Reset(dst)
		return enc, nil
	}
	return rqc.c.NewEncoder(dst)
}

// readBody reads all of r into a slice sized for size bytes when size is
// positive, so a body of the expected length costs one allocation of its size
// rather than the regrowth io.ReadAll makes. A body that turns out longer or
// shorter than size is still read whole.
func readBody(r io.Reader, size int64) ([]byte, error) {
	var buf bytes.Buffer
	if size > 0 {
		// ReadFrom grows the buffer when under MinRead bytes are free, which the
		// read that finds EOF would otherwise be.
		buf.Grow(int(size) + bytes.MinRead)
	}
	if _, err := buf.ReadFrom(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// compressedBody is the body of a request sent with a [CompressorTable]. It keeps
// the body unencoded, encodes it on the first attempt whose Compressor has an
// encoding, and encodes it again only when a later attempt resolves to a
// different Compressor. Only the goroutine running the request uses it.
type compressedBody struct {
	tr *Transport
	// raw returns a reader over the unencoded body from its start, and size is
	// the body's length. A body already in memory is read in place, and keepAlive
	// keeps it open until the request returns; any other body is read from a
	// copy, and keepAlive is nil.
	raw       func() io.ReadCloser
	size      int64
	keepAlive io.ReadCloser

	// buf is raw encoded by encodedBy's Compressor, and nil until an attempt needs an encoding.
	buf       *bytes.Buffer
	encodedBy *requestCompressor
}

// apply sets req's Body, GetBody, ContentLength and Content-Encoding for an
// attempt to conn.
func (cb *compressedBody) apply(req *http.Request, conn *Connection) error {
	use := cb.tr.compressorFor(conn)

	// The body being replaced was never sent. A reader over a body in memory
	// holds a reference to it, so it is closed, except keepAlive, which stays
	// open until the request returns.
	if req.Body != nil && req.Body != cb.keepAlive {
		_ = req.Body.Close()
	}

	if use.encoding == "" {
		req.Header.Del(headerContentEncoding)
		req.GetBody = func() (io.ReadCloser, error) { return cb.raw(), nil }
		req.Body = cb.raw()
		req.ContentLength = cb.size
		return nil
	}

	if cb.encodedBy != use {
		r := cb.raw()
		b, err := use.compress(r, cb.size)
		_ = r.Close()
		if err != nil {
			return fmt.Errorf("failed to compress request body: %w", err)
		}
		cb.buf, cb.encodedBy = b, use
	}
	body := cb.buf.Bytes()
	req.Header.Set(headerContentEncoding, use.encoding)

	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	//nolint:errcheck // error is always nil
	req.Body, _ = req.GetBody()
	req.ContentLength = int64(len(body))
	return nil
}

// compressorFor returns the Compressor for an attempt to conn: the cluster-wide
// result for a seed or rewritten connection (starting a probe when one is due),
// otherwise the one for conn's own version. See [CompressorTable] for why.
func (tr *Transport) compressorFor(conn *Connection) *requestCompressor {
	if conn.seed || conn.Rewritten {
		tr.probeClusterVersions(conn)
		return tr.table.current()
	}
	return tr.table.lookup(conn.loadVersion())
}

// versionProbeInterval is the least time between two probeClusterVersions
// requests: short enough that a downgrade or an older node joining stops the
// rule's encoding soon after, long enough that the probe costs one small GET a
// minute.
const versionProbeInterval = time.Minute

// probeClusterVersions reads every node's version through conn in the
// background and stores the outcome, unless a probe started within
// versionProbeInterval.
func (tr *Transport) probeClusterVersions(conn *Connection) {
	tbl := tr.table
	now := int64(time.Since(tbl.start)) + 1
	last := tbl.probedAt.Load()
	if (last != 0 && now-last < int64(versionProbeInterval)) || !tbl.probedAt.CompareAndSwap(last, now) {
		return
	}

	go func() {
		rc, detail := tr.clusterVersionCompressor(conn)
		// A newer probe supersedes this one, and its result must not be
		// overwritten by this older one, so the write is conditional on both
		// probedAt and the result it replaces.
		next := &probeResult{rc: rc, startedAt: now}
		for {
			prev := tbl.result.Load()
			if tbl.probedAt.Load() != now {
				return
			}
			if !tbl.result.CompareAndSwap(prev, next) {
				continue
			}
			if prev == nil || prev.rc != rc {
				Debug().Str("conn", conn.URLString).Str("encoding", rc.encoding).Str("detail", detail).
					Msg("CompressorTable Compressor changed")
			}
			return
		}
	}()
}

// nodesVersionsPath asks for each node's version and nothing else.
const nodesVersionsPath = "/_nodes?filter_path=nodes.*.version"

// clusterVersionCompressor reads every node's version with GET /_nodes through conn
// and returns the Compressor they all resolve to, or the table's Default when the
// read fails or the nodes resolve to different Compressors. detail says why, for
// the log.
func (tr *Transport) clusterVersionCompressor(conn *Connection) (*requestCompressor, string) {
	tbl := tr.table
	ctx := tr.ctx
	if tr.healthCheckTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, tr.healthCheckTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, nodesVersionsPath, nil)
	if err != nil {
		return tbl.defaultCompressor, err.Error()
	}
	if err := tr.prepareInternalRequest(conn.URL, req, tr.healthCheckRequestModifier); err != nil {
		return tbl.defaultCompressor, err.Error()
	}
	res, err := tr.transport.RoundTrip(req)
	if err != nil {
		return tbl.defaultCompressor, err.Error()
	}
	// The probe shares conn's connection with real traffic. Drain what is left
	// before closing, so a non-200 response or bytes json.Decoder leaves unread
	// do not stop the connection from being reused.
	defer func() {
		//nolint:errcheck // best-effort drain before close on a cleanup path
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}()
	if res.StatusCode != http.StatusOK {
		return tbl.defaultCompressor, fmt.Sprintf("GET %q: status %d", nodesVersionsPath, res.StatusCode)
	}

	var info struct {
		Nodes map[string]struct {
			Version string `json:"version"`
		} `json:"nodes"`
	}
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		return tbl.defaultCompressor, err.Error()
	}
	if len(info.Nodes) == 0 {
		return tbl.defaultCompressor, "no nodes listed"
	}
	var rc *requestCompressor
	for id, n := range info.Nodes {
		h := tbl.lookup(n.Version)
		if rc != nil && h != rc {
			return tbl.defaultCompressor, fmt.Sprintf(
				"node %q runs version %q, which resolves to a different Compressor than another node's", id, n.Version)
		}
		rc = h
	}
	return rc, fmt.Sprintf("all %d nodes resolve to the same Compressor", len(info.Nodes))
}

// resolveCompressor returns the Compressor the transport applies. A nil c
// resolves to GZip at the default level when legacyGzip is set and to [None]
// otherwise; a non-nil c wins over the legacy flag.
func resolveCompressor(c Compressor, legacyGzip bool) Compressor {
	if c != nil {
		return c
	}
	if legacyGzip {
		return gzipCompressor{level: gzip.DefaultCompression}
	}
	return None()
}
