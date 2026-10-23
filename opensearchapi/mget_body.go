// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"bytes"
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
)

// mgetMaxPooledBody is the largest buffer returned to mgetBodyPool. A larger one
// is left to the garbage collector, so that one large mget does not keep a large
// buffer alive.
const mgetMaxPooledBody = 1 << 20

// mgetBodyBuffer is a buffer and the encoder that writes into it, pooled together.
type mgetBodyBuffer struct {
	buf bytes.Buffer
	enc *json.Encoder
}

//nolint:gochecknoglobals // A pool is process-wide by design.
var mgetBodyPool = sync.Pool{New: func() any {
	b := &mgetBodyBuffer{}
	b.enc = json.NewEncoder(&b.buf)

	return b
}}

// mgetBody is the body of one sub-request, encoded into a pooled buffer. It is a
// [bufbody.BufferedReader], so the request builder and the transport replay it
// on a retry without copying it.
//
// The buffer is shared by everything that reads it: the body itself, each
// reader NewReader returns for a retry, and the driver, until the request
// returns. Each holds a reference, and the buffer goes back to the pool when the
// last one is released. net/http may close a request body after RoundTrip
// returns, so the request returning does not mean the buffer is free. A reader
// that is never closed keeps its reference, and the buffer is then left to the
// garbage collector rather than reused.
type mgetBody struct {
	b    *mgetBodyBuffer
	r    bytes.Reader
	refs atomic.Int32
	// closed makes the body's own Close release its reference once.
	closed atomic.Bool
}

// encodeMGetBody encodes the operations at slots as an mget body, {"docs":[...]},
// with encoding/json as the generated request would. The caller holds one
// reference, and releases it with release when the request has returned.
func encodeMGetBody(ops []MGetOperation, slots []int) (*mgetBody, error) {
	pb, _ := mgetBodyPool.Get().(*mgetBodyBuffer)
	pb.buf.Reset()

	// Encode each document on its own, straight from the caller's request. Copying
	// them into a scratch slice to encode in one call is faster on its own, but
	// the slice is lost whenever the pool is cleared, and on the whole mget it
	// allocates more for no gain in time.
	// Encode follows each document with a newline, which is whitespace between
	// array elements.
	pb.buf.WriteString(`{"docs":[`)
	var err error
	for i, pos := range slots {
		if i > 0 {
			pb.buf.WriteByte(',')
		}
		if err = pb.enc.Encode(&ops[pos]); err != nil {
			break
		}
	}
	pb.buf.WriteString(`]}`)
	if err != nil {
		mgetBodyPool.Put(pb)

		return nil, err
	}

	body := &mgetBody{b: pb}
	body.r.Reset(pb.buf.Bytes())
	// One reference for the caller and one for the body, which the transport
	// closes when it is done with it.
	body.refs.Store(2)

	return body, nil
}

// Read implements io.Reader.
func (body *mgetBody) Read(p []byte) (int, error) { return body.r.Read(p) }

// Len implements bufbody.BufferedReader: the number of unread bytes.
func (body *mgetBody) Len() int { return body.r.Len() }

// Bytes returns the whole encoded body without consuming it. The slice is valid
// only while a reference is held.
func (body *mgetBody) Bytes() []byte { return body.b.buf.Bytes() }

// NewReader implements bufbody.BufferedReader: an independent reader over the
// whole body, holding its own reference until it is closed.
func (body *mgetBody) NewReader() io.Reader {
	body.refs.Add(1)
	r := &mgetBodyReader{body: body}
	r.r.Reset(body.b.buf.Bytes())

	return r
}

// Close releases the body's own reference. It implements io.Closer, so the
// transport closes the body when it is done with it.
//
//nolint:unparam // io.Closer's signature; there is nothing to fail.
func (body *mgetBody) Close() error {
	if body.closed.CompareAndSwap(false, true) {
		body.release()
	}

	return nil
}

// release drops one reference, and returns the buffer to the pool when it was
// the last.
func (body *mgetBody) release() {
	if body.refs.Add(-1) != 0 {
		return
	}
	if body.b.buf.Cap() <= mgetMaxPooledBody {
		mgetBodyPool.Put(body.b)
	}
	body.b = nil
}

// mgetBodyReader is a reader NewReader returned for a retry.
type mgetBodyReader struct {
	body   *mgetBody
	r      bytes.Reader
	closed atomic.Bool
}

// Read implements io.Reader.
func (r *mgetBodyReader) Read(p []byte) (int, error) { return r.r.Read(p) }

// Close releases the reader's reference once.
//
//nolint:unparam // io.Closer's signature; there is nothing to fail.
func (r *mgetBodyReader) Close() error {
	if r.closed.CompareAndSwap(false, true) {
		r.body.release()
	}

	return nil
}
