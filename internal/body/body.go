// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package body defines how the client passes a request or response body that is
// already held in memory, so that no layer copies it only to read it again.
//
// A body that is held in memory says so by implementing [BufferedReader]. The
// request builder then gives the request a Content-Length and a GetBody without
// copying the body, the transport replays it on retry without copying it, and
// [Execute] parses a response from the bytes the transport already read.
//
// A body that is not held in memory, because the caller is streaming it, does
// not implement the interface and is left alone: layers that need to replay it
// buffer it themselves, once, and a caller who must not have it buffered turns
// retry off.
package body

import (
	"bytes"
	"io"
)

// BufferedReader is a body held in memory. Reading it consumes it, as reading any
// reader does, but its content is kept, so it can be sent again: NewReader returns
// a reader over the whole content, from the start, whatever has been read from the
// body itself, and as many of them as are wanted. That is what a retry needs, since
// the first attempt has by then read the body to its end. The content must stay
// intact until the last reader has finished.
type BufferedReader interface {
	io.Reader
	// Len returns the number of unread bytes. Before anything has been read it is
	// the length of the content.
	Len() int
	// NewReader returns an independent reader over the whole content, from its
	// start.
	NewReader() io.Reader
}

// ContiguousReader is a [BufferedReader] whose unread bytes are one slice, which
// a consumer that needs a []byte, such as a JSON decoder, can take without
// copying.
type ContiguousReader interface {
	BufferedReader
	// Bytes returns the unread bytes without consuming them. The slice aliases
	// the body and must not be modified.
	Bytes() []byte
}

// ReadAll returns everything r has left. It takes the bytes of a
// [ContiguousReader] without copying or consuming them, and reads any other
// reader to its end.
func ReadAll(r io.Reader) ([]byte, error) {
	if c, ok := r.(ContiguousReader); ok {
		return c.Bytes(), nil
	}

	return io.ReadAll(r)
}

// Bytes is a body backed by one slice. It reads, closes, and replays, and is
// what the transport hands back for a buffered response.
type Bytes struct {
	data []byte
	off  int
}

var _ interface {
	io.ReadCloser
	ContiguousReader
} = (*Bytes)(nil)

// New returns a body over data, which it does not copy.
func New(data []byte) *Bytes {
	return &Bytes{data: data}
}

// Read implements [io.Reader].
func (b *Bytes) Read(p []byte) (int, error) {
	if b.off >= len(b.data) {
		if len(p) == 0 {
			return 0, nil
		}

		return 0, io.EOF
	}

	n := copy(p, b.data[b.off:])
	b.off += n

	return n, nil
}

// Close implements [io.Closer]; there is nothing to release.
func (b *Bytes) Close() error { return nil }

// Len returns the number of unread bytes.
func (b *Bytes) Len() int { return len(b.data) - b.off }

// Bytes returns the unread bytes without consuming them.
func (b *Bytes) Bytes() []byte { return b.data[b.off:] }

// NewReader returns an independent reader over the whole content, whatever has
// been read.
func (b *Bytes) NewReader() io.Reader { return bytes.NewReader(b.data) }
