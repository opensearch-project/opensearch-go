// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package body_test

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/body"
)

func TestBytesReadsAndReplays(t *testing.T) {
	t.Parallel()

	const payload = "0123456789"

	tests := []struct {
		name string
		// consume reads this many bytes before the checks.
		consume   int
		wantLen   int
		wantBytes string
	}{
		{name: "unread", consume: 0, wantLen: 10, wantBytes: payload},
		{name: "partly read", consume: 4, wantLen: 6, wantBytes: "456789"},
		{name: "fully read", consume: 10, wantLen: 0, wantBytes: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b := body.New([]byte(payload))
			if tt.consume > 0 {
				_, err := io.ReadFull(b, make([]byte, tt.consume))
				require.NoError(t, err)
			}

			require.Equal(t, tt.wantLen, b.Len())
			require.Equal(t, tt.wantBytes, string(b.Bytes()))

			// A replay is of the whole content, however much has been read, because
			// a request that is sent again has had its body read to the end.
			for range 2 {
				got, err := io.ReadAll(b.NewReader())
				require.NoError(t, err)
				require.Equal(t, payload, string(got))
			}
			require.Equal(t, tt.wantLen, b.Len(), "replaying leaves the body where it was")
			require.NoError(t, b.Close())
		})
	}
}

func TestReadAll(t *testing.T) {
	t.Parallel()

	data := []byte("payload")

	tests := []struct {
		name string
		r    io.Reader
		// wantShared is set when the result must alias the source bytes, which
		// proves nothing was copied.
		wantShared bool
	}{
		{name: "a contiguous body is not copied", r: body.New(data), wantShared: true},
		{name: "any other reader is read", r: strings.NewReader("payload")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := body.ReadAll(tt.r)
			require.NoError(t, err)
			require.Equal(t, "payload", string(got))

			if tt.wantShared {
				require.Same(t, &data[0], &got[0])
			}
		})
	}

	t.Run("reading does not consume a contiguous body", func(t *testing.T) {
		t.Parallel()

		b := body.New(data)
		_, err := body.ReadAll(b)
		require.NoError(t, err)
		require.Equal(t, len(data), b.Len())
	})
}

type closableBody struct {
	*body.Bytes
	closed *bool
}

func (c closableBody) NewReader() io.Reader {
	return closableReader{Reader: c.Bytes.NewReader(), closed: c.closed}
}

type closableReader struct {
	io.Reader
	closed *bool
}

//
//nolint:unparam // Keeps the io.Closer signature.
func (c closableReader) Close() error { *c.closed = true; return nil }

// TestReplay: a replay of a body whose reader can be closed is that reader, so the
// transport's close reaches the owner; any other replay closes into nothing.
func TestReplay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		newBody     func(closed *bool) body.BufferedReader
		wantReached bool
	}{
		{"a closable reader", func(closed *bool) body.BufferedReader {
			return closableBody{Bytes: body.New([]byte("abc")), closed: closed}
		}, true},
		{"a reader that cannot be closed", func(*bool) body.BufferedReader { return body.New([]byte("abc")) }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var closed bool
			rc := body.Replay(tt.newBody(&closed))

			got, err := io.ReadAll(rc)
			require.NoError(t, err)
			require.Equal(t, "abc", string(got), "the whole content, from the start")

			require.NoError(t, rc.Close())
			require.Equal(t, tt.wantReached, closed)
		})
	}
}
