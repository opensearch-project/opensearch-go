// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package clientcache_test

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/clientcache"
)

type stubCloser struct{ closed atomic.Int32 }

func (s *stubCloser) Close() error { s.closed.Add(1); return nil }

func newConstruct(closer io.Closer, live func() int64) clientcache.ConstructFunc[io.Closer] {
	return func() (clientcache.Constructed[io.Closer], error) {
		return clientcache.Constructed[io.Closer]{
			Value:    closer,
			Closer:   clientcache.ClusterFunc{Closer: closer},
			Liveness: live,
		}, nil
	}
}

func TestGetOrCreate_SharesValueAndRefcounts(t *testing.T) {
	c := clientcache.New[io.Closer](time.Hour)
	closer := &stubCloser{}
	live := func() int64 { return 0 }

	calls := 0
	construct := func() (clientcache.Constructed[io.Closer], error) {
		calls++
		return clientcache.Constructed[io.Closer]{
			Value:    closer,
			Closer:   clientcache.ClusterFunc{Closer: closer},
			Liveness: live,
		}, nil
	}

	v1, rel1, err := c.GetOrCreate(42, construct)
	require.NoError(t, err)
	v2, rel2, err := c.GetOrCreate(42, construct)
	require.NoError(t, err)

	require.Same(t, closer, v1)
	require.Same(t, closer, v2)
	require.Equal(t, 1, calls, "second hit must reuse, not reconstruct")
	require.Equal(t, 1, c.Len())

	require.NoError(t, rel1())
	require.NoError(t, rel2())
	require.NoError(t, rel1(), "double release is a no-op")
	require.Equal(t, int32(0), closer.closed.Load(), "release must not close while cached")
}

func TestGetOrCreate_DistinctKeys(t *testing.T) {
	c := clientcache.New[io.Closer](time.Hour)
	a := &stubCloser{}
	b := &stubCloser{}
	live := func() int64 { return 0 }

	va, _, _ := c.GetOrCreate(1, newConstruct(a, live))
	vb, _, _ := c.GetOrCreate(2, newConstruct(b, live))

	require.Same(t, a, va)
	require.Same(t, b, vb)
	require.Equal(t, 2, c.Len())
}

func TestDisabledCache_NeverStores_ReleaseCloses(t *testing.T) {
	c := clientcache.New[io.Closer](-1) // negative ttl disables caching
	closer := &stubCloser{}
	live := func() int64 { return 0 }

	v, rel, err := c.GetOrCreate(1, newConstruct(closer, live))
	require.NoError(t, err)
	require.Same(t, closer, v)
	require.Equal(t, 0, c.Len(), "disabled cache stores nothing")
	require.NoError(t, rel())
	require.Equal(t, int32(1), closer.closed.Load(), "disabled release closes immediately")
	require.NoError(t, rel(), "disabled double release is a no-op")
	require.Equal(t, int32(1), closer.closed.Load(), "disabled release closes exactly once")
}

func TestWorker_EvictsIdleRefZeroEntry(t *testing.T) {
	tests := []struct {
		name string
		live func() int64
	}{
		{"constant liveness", func() int64 { return 7 }}, // never advances => idle
		{"nil liveness", nil},                            // idle the moment refcount hits 0
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := clientcache.New[io.Closer](20 * time.Millisecond)
			closer := &stubCloser{}

			_, rel, err := c.GetOrCreate(1, newConstruct(closer, tt.live))
			require.NoError(t, err)
			require.NoError(t, rel()) // refcount now 0

			require.Eventually(t, func() bool {
				return c.Len() == 0 && closer.closed.Load() == 1
			}, 2*time.Second, 10*time.Millisecond, "idle refcount-0 entry must be evicted+closed")
		})
	}
}

// TestConcurrentReacquireVsEviction guards the CAS-claim invariant: a slow
// constructor forces many goroutines through the post-construct hit path while
// the entry churns between refcount 0 and non-zero, and a handed-out value must
// never already be closed.
func TestConcurrentReacquireVsEviction(t *testing.T) {
	c := clientcache.New[io.Closer](time.Millisecond)
	live := func() int64 { return 1 } // constant => always idle-eligible at ref 0

	construct := func() (clientcache.Constructed[io.Closer], error) {
		closer := &stubCloser{}
		time.Sleep(3 * time.Millisecond) // widen the post-construct hit window
		return clientcache.Constructed[io.Closer]{
			Value:    closer,
			Closer:   clientcache.ClusterFunc{Closer: closer},
			Liveness: live,
		}, nil
	}

	errs := make([]error, 300)
	var wg sync.WaitGroup
	for i := range 300 {
		wg.Go(func() {
			v, rel, err := c.GetOrCreate(7, construct)
			if err != nil {
				errs[i] = err
				return
			}
			if sc, ok := v.(*stubCloser); ok && sc.closed.Load() != 0 {
				errs[i] = errClosedHandout
			}
			_ = rel()
		})
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
}

var errClosedHandout = closedHandoutError{}

type closedHandoutError struct{}

func (closedHandoutError) Error() string { return "GetOrCreate returned a closed transport" }

func TestWorker_KeepsBusyEntry(t *testing.T) {
	c := clientcache.New[io.Closer](20 * time.Millisecond)
	closer := &stubCloser{}
	var counter atomic.Int64
	live := counter.Load

	_, rel, err := c.GetOrCreate(1, newConstruct(closer, live))
	require.NoError(t, err)
	require.NoError(t, rel()) // refcount 0 but counter keeps advancing

	stop := make(chan struct{})
	go func() {
		tk := time.NewTicker(5 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
				counter.Add(1)
			}
		}
	}()
	time.Sleep(200 * time.Millisecond)
	busy := c.Len() == 1
	close(stop)
	require.True(t, busy, "entry with advancing liveness must not be evicted")
	require.Equal(t, int32(0), closer.closed.Load(), "busy entry must never be closed")
}

func TestWorker_StopsWhenEmpty(t *testing.T) {
	c := clientcache.New[io.Closer](20 * time.Millisecond)
	closer := &stubCloser{}
	live := func() int64 { return 0 }
	_, rel, _ := c.GetOrCreate(1, newConstruct(closer, live))
	require.NoError(t, rel())
	require.Eventually(t, func() bool { return c.Len() == 0 }, 2*time.Second, 10*time.Millisecond)
	// Re-insert must respawn the worker and evict again.
	closer2 := &stubCloser{}
	_, rel2, _ := c.GetOrCreate(2, newConstruct(closer2, live))
	require.NoError(t, rel2())
	require.Eventually(t, func() bool {
		return c.Len() == 0 && closer2.closed.Load() == 1
	}, 2*time.Second, 10*time.Millisecond, "worker must respawn after emptying")
}

func TestNeverEvict_TTLZero_NoWorker(t *testing.T) {
	c := clientcache.New[io.Closer](0) // 0 => never evict, no worker spawns
	closer := &stubCloser{}
	live := func() int64 { return 0 } // idle-eligible, but nothing should ever evict it

	_, rel, err := c.GetOrCreate(1, newConstruct(closer, live))
	require.NoError(t, err)
	require.Equal(t, 1, c.Len())
	require.NoError(t, rel()) // refcount 0: with a worker this would eventually evict

	// No worker exists to evict; the entry must persist and never be closed.
	require.Never(t, func() bool {
		return c.Len() == 0 || closer.closed.Load() != 0
	}, 200*time.Millisecond, 20*time.Millisecond, "ttl=0 must never evict or close")
	require.Equal(t, 1, c.Len())
	require.Equal(t, int32(0), closer.closed.Load())
}

func TestConcurrentGetRelease(t *testing.T) {
	c := clientcache.New[io.Closer](time.Hour)
	live := func() int64 { return 0 }
	construct := func() (clientcache.Constructed[io.Closer], error) {
		closer := &stubCloser{}
		return clientcache.Constructed[io.Closer]{
			Value:    closer,
			Closer:   clientcache.ClusterFunc{Closer: closer},
			Liveness: live,
		}, nil
	}
	errs := make([]error, 50)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() {
			_, rel, err := c.GetOrCreate(99, construct)
			if err != nil {
				errs[i] = err
				return
			}
			errs[i] = rel()
		})
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	require.LessOrEqual(t, c.Len(), 1)
	// The surviving cached transport must never be closed while refcounts churn
	// above zero. Redundant builds lost to the construct race are closed by
	// GetOrCreate per spec, and deliberately not asserted here.
	v, rel, err := c.GetOrCreate(99, construct)
	require.NoError(t, err)
	require.Equal(t, int32(0), v.(*stubCloser).closed.Load(), "live cached transport must not be closed mid-flight")
	require.NoError(t, rel())
}

func TestGetOrCreate_ConstructError(t *testing.T) {
	wantErr := errors.New("construct failed")
	failing := func() (clientcache.Constructed[io.Closer], error) {
		return clientcache.Constructed[io.Closer]{}, wantErr
	}
	tests := []struct {
		name string
		ttl  time.Duration
	}{
		{"disabled ttl", -1},
		{"caching ttl", time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := clientcache.New[io.Closer](tt.ttl)
			v, rel, err := c.GetOrCreate(1, failing)
			require.ErrorIs(t, err, wantErr)
			require.Nil(t, v, "no value on construct error")
			require.Nil(t, rel, "no release func on construct error")
			require.Equal(t, 0, c.Len(), "construct error stores nothing")
		})
	}
}

// TestNilCloser_SafeNoop covers a transport without Close: a nil io.Closer
// wrapped as ClusterFunc{Closer: nil}. Both the disabled release and the sweep's
// evict must skip Close without panicking.
func TestNilCloser_SafeNoop(t *testing.T) {
	nilConstruct := func() (clientcache.Constructed[io.Closer], error) {
		return clientcache.Constructed[io.Closer]{
			Value:    nil,
			Closer:   clientcache.ClusterFunc{Closer: nil},
			Liveness: func() int64 { return 0 },
		}, nil
	}

	t.Run("disabled release", func(t *testing.T) {
		c := clientcache.New[io.Closer](-1)
		_, rel, err := c.GetOrCreate(1, nilConstruct)
		require.NoError(t, err)
		require.NotNil(t, rel)
		require.NotPanics(t, func() { _ = rel() }, "nil closer release must not panic")
	})

	t.Run("idle eviction", func(t *testing.T) {
		c := clientcache.New[io.Closer](20 * time.Millisecond)
		_, rel, err := c.GetOrCreate(1, nilConstruct)
		require.NoError(t, err)
		require.NoError(t, rel())
		require.Eventually(t, func() bool { return c.Len() == 0 }, 2*time.Second, 10*time.Millisecond,
			"idle entry with nil closer must evict without panic")
	})
}
