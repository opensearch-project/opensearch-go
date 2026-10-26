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
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

// newReadBodyTransport returns a Transport with the free list New gives one.
func newReadBodyTransport() *Transport {
	return &Transport{readBufs: make(chan *bytes.Buffer, readBufsKept)}
}

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
		{name: "an empty body of unknown length", contentLength: -1, reader: func() io.Reader {
			return strings.NewReader("")
		}, want: ""},
		{name: "a body read a byte at a time, of unknown length", contentLength: -1, reader: func() io.Reader {
			return iotest.OneByteReader(strings.NewReader(body))
		}, want: body},
		{name: "a read of unknown length that fails", contentLength: -1, reader: func() io.Reader {
			return io.MultiReader(strings.NewReader(body[:100]), iotest.ErrReader(boom))
		}, want: body[:100], wantErr: boom},
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
			got, err := newReadBodyTransport().readResponseBody(&http.Response{ContentLength: tt.contentLength, Body: io.NopCloser(r)})
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.want, string(got))
			require.NotNil(t, got, "a nil body reads as none to Response.RawBody")
		})
	}
}

// TestReadResponseBodyOwnsWhatItReturns checks that bodies of unknown length,
// read through the buffers of readBufs, from several goroutines at once, are not
// overwritten by the bodies read after them.
func TestReadResponseBodyOwnsWhatItReturns(t *testing.T) {
	t.Parallel()

	tr := newReadBodyTransport()
	read := func(s string) ([]byte, error) {
		return tr.readResponseBody(&http.Response{ContentLength: -1, Body: io.NopCloser(strings.NewReader(s))})
	}
	const readers = 2 * readBufsKept
	const letters = "abcdefghijklmnopqrstuvwxyz"
	got := make([][]byte, readers)
	var g errgroup.Group
	for i := range readers {
		g.Go(func() error {
			var err error
			if got[i], err = read(strings.Repeat(letters[i:i+1], 1000+i)); err != nil {
				return err
			}
			for range 10 {
				if _, err := read(strings.Repeat("z", 2000)); err != nil {
					return err
				}
			}

			return nil
		})
	}
	require.NoError(t, g.Wait())
	for i := range readers {
		require.Equal(t, strings.Repeat(letters[i:i+1], 1000+i), string(got[i]))
	}
}

// TestReadResponseBodyAllocatesOnce checks that a body is read into one buffer
// of its length, from the length its response gives or, without one, through a
// buffer of readBufs, where io.ReadAll would grow one by copying.
//
//nolint:paralleltest // testing.AllocsPerRun panics in a parallel test.
func TestReadResponseBodyAllocatesOnce(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 64<<10)
	for _, contentLength := range []int64{int64(len(body)), -1} {
		t.Run(fmt.Sprintf("length %d", contentLength), func(t *testing.T) {
			tr := newReadBodyTransport()
			r := bytes.NewReader(body)
			res := &http.Response{ContentLength: contentLength, Body: io.NopCloser(r)}

			var (
				got []byte
				err error
			)
			allocs := testing.AllocsPerRun(20, func() {
				r.Reset(body)
				got, err = tr.readResponseBody(res)
			})
			require.NoError(t, err)
			require.Len(t, got, len(body))
			if raceEnabled {
				t.Skip("the race detector allocates on its own")
			}
			require.LessOrEqual(t, allocs, 1.0)
		})
	}
}
