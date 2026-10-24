// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package body

import (
	"bytes"
	"io"
	"sync"
	"sync/atomic"
)

// maxPooledBytes is the largest buffer returned to the pool. A larger one,
// from an unusually large body, is left to the garbage collector, so that the
// pool does not keep it for every small body after it.
const maxPooledBytes = 1 << 20

// pooledBuffers holds the buffers of Pooled bodies between requests.
var pooledBuffers = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// Pooled is a request body held in a pooled buffer. It is a [ContiguousReader],
// so the request builder and the transport send and replay it without copying
// it.
//
// The buffer is shared by everything that reads it: the body itself, each reader
// NewReader returns for a retry, and the owner that built the request, until
// the request has returned. Each holds a reference, and the buffer goes back to
// the pool when the last is released. net/http may close a request body after
// RoundTrip returns, so the request returning does not mean the buffer is free.
// A reference that is never released, such as the owner's of a request built
// but not sent through [Release], leaves the buffer to the garbage collector
// rather than to the pool, which is safe.
type Pooled struct {
	buf  *bytes.Buffer
	r    bytes.Reader
	refs atomic.Int32
	// closed and released make the body's Close and its owner's Release each
	// drop their reference once.
	closed, released atomic.Bool
}

// NewPooled returns a body holding what write writes into a pooled buffer. The
// caller holds the owner's reference, and passes the body to [Release] once the
// request it was sent in has returned.
func NewPooled(write func(*bytes.Buffer) error) (*Pooled, error) {
	buf, _ := pooledBuffers.Get().(*bytes.Buffer)
	buf.Reset()
	if err := write(buf); err != nil {
		putBuffer(buf)

		return nil, err
	}

	p := &Pooled{buf: buf}
	p.r.Reset(buf.Bytes())
	// One reference for the owner and one for the body, which the transport
	// closes when it is done with it.
	p.refs.Store(2)

	return p, nil
}

// Read implements io.Reader.
func (p *Pooled) Read(b []byte) (int, error) { return p.r.Read(b) }

// Len implements [BufferedReader]: the number of unread bytes.
func (p *Pooled) Len() int { return p.r.Len() }

// Bytes implements [ContiguousReader]: the unread bytes, valid only while a
// reference is held.
func (p *Pooled) Bytes() []byte { return p.buf.Bytes()[p.buf.Len()-p.r.Len():] }

// NewReader implements [BufferedReader]: an independent reader over the whole
// body, holding its own reference until it is closed.
func (p *Pooled) NewReader() io.Reader {
	p.refs.Add(1)
	r := &pooledReader{p: p}
	r.r.Reset(p.buf.Bytes())

	return r
}

// Close releases the body's own reference. It implements io.Closer, so the
// transport closes the body when it is done with it.
//
//nolint:unparam // io.Closer's signature; there is nothing to fail.
func (p *Pooled) Close() error {
	if p.closed.CompareAndSwap(false, true) {
		p.release()
	}

	return nil
}

// release drops one reference, and returns the buffer to the pool when it was
// the last.
func (p *Pooled) release() {
	if p.refs.Add(-1) != 0 {
		return
	}
	putBuffer(p.buf)
	p.buf = nil
}

func putBuffer(buf *bytes.Buffer) {
	if buf.Cap() <= maxPooledBytes {
		pooledBuffers.Put(buf)
	}
}

// Release drops the owner's reference to r when r is a [Pooled] body, once the
// request it was sent in has returned. Any other reader is left alone.
func Release(r io.Reader) {
	if p, ok := r.(*Pooled); ok && p.released.CompareAndSwap(false, true) {
		p.release()
	}
}

// pooledReader is a reader NewReader returned for a retry.
type pooledReader struct {
	p      *Pooled
	r      bytes.Reader
	closed atomic.Bool
}

// Read implements io.Reader.
func (r *pooledReader) Read(b []byte) (int, error) { return r.r.Read(b) }

// Close releases the reader's reference once.
//
//nolint:unparam // io.Closer's signature; there is nothing to fail.
func (r *pooledReader) Close() error {
	if r.closed.CompareAndSwap(false, true) {
		r.p.release()
	}

	return nil
}
