// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package body

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func newTestPooled(t *testing.T, s string) *Pooled {
	t.Helper()

	p, err := NewPooled(func(b *bytes.Buffer) error {
		b.WriteString(s)

		return nil
	})
	require.NoError(t, err)

	return p
}

// TestPooledReleasesOnTheLastReference checks that the buffer goes back to the
// pool only when the owner, the body and every retry reader have all let go, in
// any order.
func TestPooledReleasesOnTheLastReference(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		order []string
	}{
		{name: "the owner last", order: []string{"close", "reader", "release"}},
		{name: "the body last", order: []string{"release", "reader", "close"}},
		{name: "a retry reader last", order: []string{"release", "close", "reader"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := newTestPooled(t, `{"a":1}`)
			retry, ok := p.NewReader().(io.ReadCloser)
			require.True(t, ok)
			for i, step := range tt.order {
				require.NotNil(t, p.buf, "the buffer is still held before step %d", i)
				switch step {
				case "close":
					require.NoError(t, p.Close())
					require.NoError(t, p.Close(), "a second Close drops nothing")
				case "release":
					Release(p)
					Release(p)
				case "reader":
					got, err := io.ReadAll(retry)
					require.NoError(t, err)
					require.JSONEq(t, `{"a":1}`, string(got))
					require.NoError(t, retry.Close())
					require.NoError(t, retry.Close())
				}
			}
			require.Nil(t, p.buf, "the buffer is returned after the last reference")
		})
	}
}

// TestPooledReadsAsBuffered checks that the body reads as the bytes it was given,
// from the start for each retry reader whatever the body itself has read.
func TestPooledReadsAsBuffered(t *testing.T) {
	t.Parallel()

	p := newTestPooled(t, "abcdef")
	require.Equal(t, 6, p.Len())
	head := make([]byte, 2)
	_, err := io.ReadFull(p, head)
	require.NoError(t, err)
	require.Equal(t, "ab", string(head))
	require.Equal(t, 4, p.Len())
	require.Equal(t, "cdef", string(p.Bytes()), "Bytes is the unread part")

	all, err := io.ReadAll(p.NewReader())
	require.NoError(t, err)
	require.Equal(t, "abcdef", string(all), "a retry reader starts at the beginning")

	got, err := ReadAll(p)
	require.NoError(t, err)
	require.Equal(t, "cdef", string(got), "ReadAll takes a contiguous body's unread bytes")
}

// TestPooledWriteFailure checks that a failed encode returns no body.
func TestPooledWriteFailure(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	p, err := NewPooled(func(*bytes.Buffer) error { return boom })
	require.ErrorIs(t, err, boom)
	require.Nil(t, p)
}

// TestPooledConcurrentReleases releases the references from many goroutines at
// once, for the race detector.
func TestPooledConcurrentReleases(t *testing.T) {
	t.Parallel()

	for range 100 {
		p := newTestPooled(t, "x")
		readers := make([]io.ReadCloser, 8)
		for i := range readers {
			readers[i], _ = p.NewReader().(io.ReadCloser)
		}
		var wg sync.WaitGroup
		for _, r := range readers {
			wg.Go(func() {
				_, _ = io.ReadAll(r)
				_ = r.Close()
			})
		}
		wg.Go(func() { _ = p.Close() })
		wg.Go(func() { Release(p) })
		wg.Wait()
		require.Nil(t, p.buf)
	}
}

// TestReleaseLeavesOtherReaders checks that Release does nothing to a reader that
// is not a Pooled body.
func TestReleaseLeavesOtherReaders(t *testing.T) {
	t.Parallel()

	Release(bytes.NewReader([]byte("x")))
	Release(nil)
}
