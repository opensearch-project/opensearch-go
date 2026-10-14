// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package compress provides request-body compressors for the OpenSearch
// client, backed by [klauspost/compress].
//
// Each constructor returns an [opensearchtransport.Compressor] to set on the
// Compressor field of opensearch.Config. The module is separate from the
// client so that klauspost/compress stays out of the core dependency graph, and
// no klauspost type appears in its API.
//
// [klauspost/compress]: https://github.com/klauspost/compress
package compress

import (
	"fmt"
	"io"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/s2"
	"github.com/klauspost/compress/zlib"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// DefaultLevel selects a codec's own default compression level, which is level
// 5 for [Gzip] and [Deflate]. Pass it where a constructor takes a level and
// there is no reason to choose one.
const DefaultLevel = -1

// maxLevel is the highest compression level [Gzip] and [Deflate] accept, flate's
// BestCompression.
const maxLevel = 9

// checkLevel rejects a level outside [DefaultLevel] through [maxLevel]. The
// underlying codecs accept some levels outside that range for modes this
// package does not expose.
func checkLevel(level int) error {
	if level < DefaultLevel || level > maxLevel {
		return fmt.Errorf("compress: invalid compression level: %d", level)
	}
	return nil
}

// Content-Encoding tokens of the compressors in this package.
const (
	encodingGzip    = "gzip"
	encodingDeflate = "deflate"
	encodingSnappy  = "snappy"
)

// Gzip returns a Compressor that gzips request bodies at the given level:
// [DefaultLevel] or 0 through 9, where 0 stores the data uncompressed, 1 is the
// fastest and 9 compresses the most. It returns an error for a level outside
// that range.
func Gzip(level int) (opensearchtransport.Compressor, error) {
	if err := checkLevel(level); err != nil {
		return nil, err
	}
	return leveledCompressor{encoding: encodingGzip, level: level, newWriter: newGzipWriter}, nil
}

// Deflate returns a Compressor that sends request bodies in the zlib format
// that the "deflate" Content-Encoding denotes, at the given level:
// [DefaultLevel] or 0 through 9, as for [Gzip]. It returns an error for a level
// outside that range.
func Deflate(level int) (opensearchtransport.Compressor, error) {
	if err := checkLevel(level); err != nil {
		return nil, err
	}
	return leveledCompressor{encoding: encodingDeflate, level: level, newWriter: newZlibWriter}, nil
}

// newLevelWriterFunc builds a codec's writer over w at the given level.
type newLevelWriterFunc func(w io.Writer, level int) (opensearchtransport.Encoder, error)

func newGzipWriter(w io.Writer, level int) (opensearchtransport.Encoder, error) {
	return asEncoder(gzip.NewWriterLevel(w, level))
}

func newZlibWriter(w io.Writer, level int) (opensearchtransport.Encoder, error) {
	return asEncoder(zlib.NewWriterLevel(w, level))
}

// asEncoder converts a codec's concrete writer to an Encoder, returning an
// untyped nil on error: a nil *gzip.Writer held in an Encoder is not nil.
func asEncoder[W opensearchtransport.Encoder](zw W, err error) (opensearchtransport.Encoder, error) {
	if err != nil {
		return nil, err
	}
	return zw, nil
}

// leveledCompressor is the [opensearchtransport.Compressor] behind [Gzip] and
// [Deflate], which differ only in Content-Encoding token and writer.
type leveledCompressor struct {
	encoding  string
	level     int
	newWriter newLevelWriterFunc
}

// ContentEncoding implements [opensearchtransport.Compressor].
func (c leveledCompressor) ContentEncoding() string { return c.encoding }

// NewEncoder implements [opensearchtransport.Compressor].
func (c leveledCompressor) NewEncoder(w io.Writer) (opensearchtransport.Encoder, error) {
	return c.newWriter(w, c.level)
}

// Snappy returns a Compressor that sends request bodies in the snappy framing
// format. It has a single speed setting, so it takes no level.
func Snappy() opensearchtransport.Compressor { return snappyCompressor{} }

type snappyCompressor struct{}

// ContentEncoding implements [opensearchtransport.Compressor].
func (snappyCompressor) ContentEncoding() string { return encodingSnappy }

// NewEncoder implements [opensearchtransport.Compressor].
func (snappyCompressor) NewEncoder(w io.Writer) (opensearchtransport.Encoder, error) {
	// Concurrency 1 keeps the writer from starting goroutines on every Reset;
	// the transport already runs one Encoder per in-flight request.
	return s2.NewWriter(w, s2.WriterSnappyCompat(), s2.WriterConcurrency(1)), nil
}
