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
	"github.com/klauspost/compress/zstd"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// DefaultLevel selects a codec's own default compression level, which is level
// 5 for [Gzip] and [Deflate] and klauspost's default zstd level for [ZstdTable].
// Pass it where a constructor takes a level and there is no reason to choose
// one.
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
	encodingZstd    = "zstd"
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

// maxZstdLevel is the highest compression level [ZstdTable] accepts, zstd's own
// maximum.
const maxZstdLevel = 22

// zstdMinServerVersion is the first OpenSearch release that supports zstd
// request bodies.
const zstdMinServerVersion = "v3.0.0"

// zstdTableGzipLevel is the gzip level of the [ZstdTable] Default, the standard
// library's default.
const zstdTableGzipLevel = 6

// ZstdTable returns an [opensearchtransport.CompressorTable] that sends request
// bodies in the zstd format at the given level, [DefaultLevel] or 1 to 22, to
// OpenSearch 3.0.0 and later, where zstd is supported, and gzip at level 6
// otherwise. Set it as opensearch.Config.CompressorTable or
// opensearchtransport.Config.CompressorTable; see
// [opensearchtransport.CompressorTable] for how server versions are read.
//
// klauspost/compress groups the zstd levels:
//
//   - 1 to 2: fastest
//   - 3 to 5: default, which [DefaultLevel] selects
//   - 6 to 9: better compression
//   - 10 to 22: best compression
//
// See https://pkg.go.dev/github.com/klauspost/compress/zstd#EncoderLevelFromZstd
// for details. It returns an error for a level outside that range.
func ZstdTable(level int) (*opensearchtransport.CompressorTable, error) {
	encLevel := zstd.SpeedDefault
	switch {
	case level == DefaultLevel:
	case level >= 1 && level <= maxZstdLevel:
		encLevel = zstd.EncoderLevelFromZstd(level)
	default:
		return nil, fmt.Errorf("compress: invalid compression level: %d", level)
	}
	return &opensearchtransport.CompressorTable{
		Rules: []opensearchtransport.CompressorRule{
			{MinVersion: zstdMinServerVersion, Compressor: zstdCompressor{level: encLevel}},
		},
		Default: leveledCompressor{encoding: encodingGzip, level: zstdTableGzipLevel, newWriter: newGzipWriter},
	}, nil
}

type zstdCompressor struct {
	level zstd.EncoderLevel
}

// ContentEncoding implements [opensearchtransport.Compressor].
func (zstdCompressor) ContentEncoding() string { return encodingZstd }

// zstdWindowSize bounds how far back a [ZstdTable] encoder matches, and so the
// history each encoder holds: about 1 MiB in lower-memory mode, rather than the
// 8 to 16 MiB that klauspost's default 4 or 8 MiB window needs. Measured on 5 MB
// bodies of synthetic logs from 8 concurrent senders at level 1, it cut peak
// heap from 147 to 54 MB, with a compression ratio of 8.96 against 8.94.
const zstdWindowSize = 1 << 20

// NewEncoder implements [opensearchtransport.Compressor].
func (c zstdCompressor) NewEncoder(w io.Writer) (opensearchtransport.Encoder, error) {
	return asEncoder(zstd.NewWriter(w,
		zstd.WithEncoderLevel(c.level),
		// Concurrency 1 for the same reason as Snappy's.
		zstd.WithEncoderConcurrency(1),
		zstd.WithWindowSize(zstdWindowSize),
		// History of one window plus one block, rather than two windows.
		zstd.WithLowerEncoderMem(true),
	))
}
