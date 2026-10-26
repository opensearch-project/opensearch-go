// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"
)

func TestReadResponseBody(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("x", 3000)
	boom := errors.New("boom")

	tests := []struct {
		name          string
		contentLength int64
		reader        func() io.Reader
		want          string
		wantErr       error
	}{
		{name: "the length the response gives", contentLength: 3000, want: body},
		{name: "no length", contentLength: -1, want: body},
		{name: "a zero length with a body", contentLength: 0, want: body},
		{name: "a length shorter than the body", contentLength: 10, want: body},
		{name: "a length longer than the body", contentLength: 5000, want: body},
		{name: "a length past the bound", contentLength: maxPresizedBody + 1, want: body},
		{name: "a body read a byte at a time", contentLength: 3000, reader: func() io.Reader {
			return iotest.OneByteReader(strings.NewReader(body))
		}, want: body},
		{name: "a read that fails", contentLength: 3000, reader: func() io.Reader {
			return io.MultiReader(strings.NewReader(body[:100]), iotest.ErrReader(boom))
		}, want: body[:100], wantErr: boom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := io.Reader(strings.NewReader(body))
			if tt.reader != nil {
				r = tt.reader()
			}
			got, err := readResponseBody(&http.Response{ContentLength: tt.contentLength, Body: io.NopCloser(r)})
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.want, string(got))
		})
	}
}

// TestReadResponseBodyAllocatesOnce checks that a body of the length its response
// gives is read into one buffer, where io.ReadAll would grow one by copying.
//
//nolint:paralleltest // testing.AllocsPerRun panics in a parallel test.
func TestReadResponseBodyAllocatesOnce(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 64<<10)
	r := bytes.NewReader(body)
	res := &http.Response{ContentLength: int64(len(body)), Body: io.NopCloser(r)}

	var (
		got []byte
		err error
	)
	allocs := testing.AllocsPerRun(20, func() {
		r.Reset(body)
		got, err = readResponseBody(res)
	})
	require.NoError(t, err)
	require.Len(t, got, len(body))
	if raceEnabled {
		t.Skip("the race detector allocates on its own")
	}
	require.LessOrEqual(t, allocs, 1.0)
}
