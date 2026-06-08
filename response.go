// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package opensearch

import (
	"fmt"
	"io"
	"net/http"
)

const httpStatusCodeThreshold = 299

// Response represents the API response.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
	rawBody    []byte

	// render caches String's rendered bytes behind a pointer so value copies
	// (such as the one fmt makes) share one cache and String can use a value
	// receiver while staying non-consuming. Set by NewResponse and Client.Do.
	render *renderCache
}

// renderCache holds the bytes String renders, filled at most once. It is
// referenced through a pointer so a value copy of Response observes a cache
// populated by any other copy. Concurrent String calls on copies sharing one
// renderCache are not safe.
type renderCache struct {
	buf    []byte
	err    error
	loaded bool
}

// RawBody returns the buffered response bytes for inspection or
// comparison testing.
//
// The returned slice aliases memory owned by Response and may be backed
// by pooled storage in future implementations. Callers must NOT mutate
// the slice and must NOT retain it past the lifetime of the request
// handling: copy with bytes.Clone if either is needed, or use
// HijackBody to transfer ownership to the caller.
//
// Populated for both success and error responses buffered by Client.Do
// (the default buffered mode). Returns nil when:
//
//   - The response was streamed without buffering (DisableResponseBuffering).
//   - The Response was constructed directly (e.g. via NewResponse) rather
//     than returned by Client.Do.
func (r *Response) RawBody() []byte {
	return r.rawBody
}

// HijackBody returns the buffered response bytes and transfers
// ownership to the caller, clearing r.rawBody so the Response no
// longer references the buffer. After Hijack the caller may mutate
// or retain the slice indefinitely; subsequent RawBody calls return
// nil. Use this when the buffer outlives the Response (logging,
// background processing, test fixtures).
func (r *Response) HijackBody() []byte {
	body := r.rawBody
	r.rawBody = nil
	return body
}

// NewResponse creates a Response with the given status, body, and headers.
func NewResponse(statusCode int, body io.ReadCloser, header http.Header) *Response {
	return &Response{
		StatusCode: statusCode,
		Body:       body,
		Header:     header,
		render:     &renderCache{},
	}
}

// String returns the response status and body as a string.
//
// String uses a value receiver, so both Response and *Response satisfy
// fmt.Stringer. For any Response returned by Client.Do (success or error, in
// the default buffered mode) it renders from the buffered rawBody and never
// touches Body, so logging a response does not drain it. For a Response
// holding only an unbuffered Body -- a streamed response
// (DisableResponseBuffering) or a hand-built Response -- String reads Body
// once to render it; repeat calls stay consistent via an internal cache, but
// a value receiver cannot restore the caller's Body field, so that single-use
// stream is consumed.
func (r Response) String() string {
	body, rerr, ok := r.renderedBody()
	if !ok {
		return r.Status()
	}
	if rerr != nil {
		return fmt.Sprintf("%s <error reading response body: %v>", r.Status(), rerr)
	}
	return fmt.Sprintf("%s %s", r.Status(), body)
}

// renderedBody returns the bytes String should render, any read error, and
// whether a body is present.
//
// Reading is idempotent for any Response built by NewResponse or Client.Do:
// both provision r.render, so the first read caches its result and every later
// call returns it without touching Body. A bare Response{} struct literal has a
// nil r.render and cannot get one here -- a value receiver only sees a copy, so
// it cannot initialize a pointer field that outlives the call. For such a
// literal the rawBody/cache fast-paths are skipped and Body is read (and
// consumed) on every call. Nothing in this module builds a Response that way;
// the SDK always routes through NewResponse or Do.
func (r Response) renderedBody() ([]byte, error, bool) {
	// Prefer bytes already buffered by Client.Do: rendering from rawBody (or
	// a prior cached render) never touches Body, so it is fully value-safe.
	if r.rawBody != nil {
		return r.rawBody, nil, true
	}
	if r.render != nil && r.render.loaded {
		return r.render.buf, r.render.err, true
	}
	if r.Body == nil {
		return nil, nil, false
	}

	// Body has not been buffered yet. Read it and cache the result behind the
	// shared pointer (when one exists) so repeat String calls, including fmt's
	// value copies, return the same bytes without re-reading. The read drains
	// Body; a value receiver cannot hand a restored reader back to the caller,
	// which is why responses that must stay readable carry rawBody.
	read, err := io.ReadAll(r.Body)
	if r.render != nil {
		r.render.buf, r.render.err, r.render.loaded = read, err, true
	}
	return read, err, true
}

// Status retuens the response status as string.
func (r Response) Status() string {
	status := http.StatusText(r.StatusCode)
	if status == "" {
		status = "<nil>"
	}
	return fmt.Sprintf("[%d %s]", r.StatusCode, status)
}

// IsError returns true when the response status indicates failure.
func (r *Response) IsError() bool {
	return r.StatusCode > httpStatusCodeThreshold
}
