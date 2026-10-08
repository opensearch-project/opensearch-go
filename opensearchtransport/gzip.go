// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
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
// NewEncoder(io.Discard) or Close fails.
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

// noneCompressor leaves bodies unmodified. The transport treats it as "no
// compressor" and never calls NewEncoder.
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
	// newRequestCompressor validated it.
	encoding string
	c        Compressor

	// pool holds closed Encoders of c. It has no New func: a miss falls back to
	// c.NewEncoder.
	pool sync.Pool
}

// newRequestCompressor validates c and returns the compressor the transport
// applies. It rejects a ContentEncoding that is not an HTTP token, and a
// Compressor whose NewEncoder or Close fails, so a broken Compressor fails when
// the transport is built rather than on its first request.
func newRequestCompressor(c Compressor) (*requestCompressor, error) {
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
// stream returns.
func (rc *requestCompressor) compress(body io.Reader) (*bytes.Buffer, error) {
	buf := new(bytes.Buffer)

	enc, err := rc.encoder(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to compress request body: %w", err)
	}

	if _, err := io.Copy(enc, body); err != nil {
		_ = enc.Close() // the Encoder is discarded; Close releases what it holds
		return nil, fmt.Errorf("failed to compress request body: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("failed to compress request body (during close): %w", err)
	}

	// Only an Encoder that closed cleanly goes back to the pool.
	rc.pool.Put(enc)
	return buf, nil
}

// encoder returns a pooled Encoder reset to write to dst, or a new one.
func (rc *requestCompressor) encoder(dst io.Writer) (Encoder, error) {
	if enc, ok := rc.pool.Get().(Encoder); ok {
		enc.Reset(dst)
		return enc, nil
	}
	return rc.c.NewEncoder(dst)
}

// resolveCompressor returns the compressor stream applies, or nil when request
// compression is off. A non-nil c wins over the legacy flag.
func resolveCompressor(c Compressor, legacyGzip bool) Compressor {
	if c == nil && legacyGzip {
		return gzipCompressor{level: gzip.DefaultCompression}
	}
	if _, ok := c.(noneCompressor); ok {
		return nil
	}
	return c
}
