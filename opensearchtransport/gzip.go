// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"sync"
)

// Compressor encodes request bodies before they are sent. Obtain one from
// [None] or [GZip] and set it as Config.Compressor. The method set is sealed so
// further encodings can be added without a breaking change.
type Compressor interface {
	// contentEncoding is the Content-Encoding value for compressed bodies.
	contentEncoding() string
	compress(io.ReadCloser) (*bytes.Buffer, error)
	collectBuffer(*bytes.Buffer)
}

// noneCompressor leaves bodies unmodified. The transport treats it as "no
// compressor" and never calls compress.
type noneCompressor struct{}

func (noneCompressor) contentEncoding() string { return "" }

func (noneCompressor) compress(io.ReadCloser) (*bytes.Buffer, error) {
	return nil, fmt.Errorf("opensearchtransport: noneCompressor does not compress")
}

func (noneCompressor) collectBuffer(*bytes.Buffer) {}

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
	c, err := newGzipCompressor(level)
	if err != nil {
		return nil, err
	}
	return c, nil
}

type gzipCompressor struct {
	gzipWriterPool *sync.Pool
	bufferPool     *sync.Pool
}

func (*gzipCompressor) contentEncoding() string { return "gzip" }

// newGzipCompressor returns a new gzipCompressor that uses a sync.Pool to reuse gzip.Writers.
func newGzipCompressor(level int) (*gzipCompressor, error) {
	// Validate once so the pool's New can ignore the error.
	if _, err := gzip.NewWriterLevel(io.Discard, level); err != nil {
		return nil, fmt.Errorf("opensearchtransport: %w", err)
	}

	gzipWriterPool := sync.Pool{
		New: func() any {
			w, _ := gzip.NewWriterLevel(io.Discard, level) //nolint:errcheck // level validated above
			return w
		},
	}

	bufferPool := sync.Pool{
		New: func() any {
			return new(bytes.Buffer)
		},
	}

	return &gzipCompressor{
		gzipWriterPool: &gzipWriterPool,
		bufferPool:     &bufferPool,
	}, nil
}

func (pg *gzipCompressor) compress(rc io.ReadCloser) (*bytes.Buffer, error) {
	writer := pg.gzipWriterPool.Get().(*gzip.Writer)
	defer pg.gzipWriterPool.Put(writer)

	buf := pg.bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	writer.Reset(buf)

	if _, err := io.Copy(writer, rc); err != nil {
		return buf, fmt.Errorf("failed to compress request body: %w", err)
	}
	if err := writer.Close(); err != nil {
		return buf, fmt.Errorf("failed to compress request body (during close): %w", err)
	}
	return buf, nil
}

func (pg *gzipCompressor) collectBuffer(buf *bytes.Buffer) {
	if buf == nil {
		return
	}
	pg.bufferPool.Put(buf)
}

// resolveCompressor returns the compressor stream applies, or nil when request
// compression is off. A non-nil c wins over the legacy flag.
func resolveCompressor(c Compressor, legacyGzip bool) Compressor {
	if c == nil && legacyGzip {
		gz, _ := newGzipCompressor(gzip.DefaultCompression) //nolint:errcheck // the default level is always valid
		return gz
	}
	if _, ok := c.(noneCompressor); ok {
		return nil
	}
	return c
}
