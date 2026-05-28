// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPooledFloats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		do   func(t *testing.T)
	}{
		{
			name: "zero value Slice is nil",
			do: func(t *testing.T) {
				t.Helper()
				var b pooledFloats
				require.Nil(t, b.Slice())
			},
		},
		{
			name: "zero value Len is zero",
			do: func(t *testing.T) {
				t.Helper()
				var b pooledFloats
				require.Equal(t, 0, b.Len())
			},
		},
		{
			name: "zero value Release is a no-op",
			do: func(t *testing.T) {
				t.Helper()
				var b pooledFloats
				require.NotPanics(t, func() { b.Release() })
			},
		},
		{
			name: "acquire grows when n exceeds capacity",
			do: func(t *testing.T) {
				t.Helper()
				b := acquireFloats(128)
				defer b.Release()
				require.Equal(t, 128, b.Len())
				require.GreaterOrEqual(t, cap(b.Slice()), 128)
			},
		},
		{
			name: "slice is writable and readable",
			do: func(t *testing.T) {
				t.Helper()
				b := acquireFloats(3)
				defer b.Release()
				s := b.Slice()
				s[0], s[1], s[2] = 1.0, 2.0, 3.0
				require.Equal(t, []float64{1.0, 2.0, 3.0}, b.Slice())
			},
		},
		{
			name: "release then reacquire produces clean length-n buffer",
			do: func(t *testing.T) {
				t.Helper()
				// sync.Pool reuse is best-effort, but length and writability
				// must hold regardless of whether the backing array is reused.
				b1 := acquireFloats(8)
				s1 := b1.Slice()
				for i := range s1 {
					s1[i] = float64(i + 1)
				}
				b1.Release()

				b2 := acquireFloats(4)
				defer b2.Release()
				require.Equal(t, 4, b2.Len())
				require.Len(t, b2.Slice(), 4)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.do(t)
		})
	}
}

func TestPooledFloatsAcquireLength(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		n    int
	}{
		{name: "zero", n: 0},
		{name: "small", n: 4},
		{name: "default capacity", n: 16},
		{name: "grow beyond default", n: 64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := acquireFloats(tt.n)
			defer b.Release()
			require.Equal(t, tt.n, b.Len())
			require.Len(t, b.Slice(), tt.n)
		})
	}
}

func TestPooledConns(t *testing.T) {
	t.Parallel()

	u, err := url.Parse("http://example.invalid:9200")
	require.NoError(t, err)
	conn := &Connection{URL: u}

	tests := []struct {
		name string
		do   func(t *testing.T)
	}{
		{
			name: "zero value Slice is nil",
			do: func(t *testing.T) {
				t.Helper()
				var b pooledConns
				require.Nil(t, b.Slice())
			},
		},
		{
			name: "zero value Len is zero",
			do: func(t *testing.T) {
				t.Helper()
				var b pooledConns
				require.Equal(t, 0, b.Len())
			},
		},
		{
			name: "zero value Release is a no-op",
			do: func(t *testing.T) {
				t.Helper()
				var b pooledConns
				require.NotPanics(t, func() { b.Release() })
			},
		},
		{
			name: "slice is writable and readable",
			do: func(t *testing.T) {
				t.Helper()
				b := acquireConns(2)
				defer b.Release()
				s := b.Slice()
				s[0] = conn
				s[1] = conn
				require.Same(t, conn, b.Slice()[0])
				require.Same(t, conn, b.Slice()[1])
			},
		},
		{
			name: "clearConns covers full capacity",
			do: func(t *testing.T) {
				t.Helper()
				// Verify the shared helper used by Release and putConnSlice
				// clears all cap slots, not just the length-n window. This
				// prevents *Connection retention across pool cycles.
				//
				// Tested directly (not via Release) so the test never reads
				// the backing array after returning the buffer to the pool,
				// which would race with parallel re-acquisition.
				s := make([]*Connection, 0, 4)
				s = append(s, conn, conn)
				backing := s[:cap(s)]
				for i := range backing {
					backing[i] = conn
				}
				clearConns(&s)
				require.Empty(t, s)
				for i := range backing {
					require.Nilf(t, backing[i], "index %d not cleared by clearConns", i)
				}
			},
		},
		{
			name: "release then reacquire produces clean length-n buffer",
			do: func(t *testing.T) {
				t.Helper()
				b1 := acquireConns(8)
				b1.Release()
				b2 := acquireConns(3)
				defer b2.Release()
				require.Equal(t, 3, b2.Len())
				for i, c := range b2.Slice() {
					require.Nilf(t, c, "index %d not nil after reacquire", i)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.do(t)
		})
	}
}

func TestPooledConnsAcquireLength(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		n    int
	}{
		{name: "zero", n: 0},
		{name: "small", n: 2},
		{name: "default capacity", n: 8},
		{name: "grow beyond default", n: 32},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := acquireConns(tt.n)
			defer b.Release()
			require.Equal(t, tt.n, b.Len())
			require.Len(t, b.Slice(), tt.n)
		})
	}
}
