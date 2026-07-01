// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package clientcache_test

import (
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

func newConstruct(closer io.Closer, live func() int64) func() (clientcache.Constructed, error) {
	return func() (clientcache.Constructed, error) {
		return clientcache.Constructed{Value: closer, Closer: closer, Liveness: live}, nil
	}
}

func TestGetOrCreate_SharesValueAndRefcounts(t *testing.T) {
	c := clientcache.New(time.Hour, false)
	closer := &stubCloser{}
	live := func() int64 { return 0 }

	calls := 0
	construct := func() (clientcache.Constructed, error) {
		calls++
		return clientcache.Constructed{Value: closer, Closer: closer, Liveness: live}, nil
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
	c := clientcache.New(time.Hour, false)
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
	c := clientcache.New(0, true)
	closer := &stubCloser{}
	live := func() int64 { return 0 }

	v, rel, err := c.GetOrCreate(1, newConstruct(closer, live))
	require.NoError(t, err)
	require.Same(t, closer, v)
	require.Equal(t, 0, c.Len(), "disabled cache stores nothing")
	require.NoError(t, rel())
	require.Equal(t, int32(1), closer.closed.Load(), "disabled release closes immediately")
}

func TestWorker_EvictsIdleRefZeroEntry(t *testing.T) {
	c := clientcache.New(20*time.Millisecond, false)
	closer := &stubCloser{}
	live := func() int64 { return 7 } // never advances => idle

	_, rel, err := c.GetOrCreate(1, newConstruct(closer, live))
	require.NoError(t, err)
	require.NoError(t, rel()) // refcount now 0

	require.Eventually(t, func() bool {
		return c.Len() == 0 && closer.closed.Load() == 1
	}, 2*time.Second, 10*time.Millisecond, "idle refcount-0 entry must be evicted+closed")
}

// TestConcurrentReacquireVsEviction stresses concurrent construct/hit/release
// against an idle worker on a tiny TTL. A slow constructor forces many
// goroutines through the post-construct hit path while the entry churns between
// refcount 0 and non-zero; a handed-out value must never be already closed.
// Guards the invariant that both hit paths increment the refcount under the
// mutex before unlocking, so a sweep cannot evict a to-be-reacquired entry.
func TestConcurrentReacquireVsEviction(t *testing.T) {
	c := clientcache.New(time.Millisecond, false)
	live := func() int64 { return 1 } // constant => always idle-eligible at ref 0

	//nolint:unparam // signature is fixed by GetOrCreate's construct parameter
	construct := func() (clientcache.Constructed, error) {
		closer := &stubCloser{}
		time.Sleep(3 * time.Millisecond) // widen the post-construct hit window
		return clientcache.Constructed{
			Value:    closer,
			Closer:   closer,
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
	c := clientcache.New(20*time.Millisecond, false)
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
}

func TestWorker_StopsWhenEmpty(t *testing.T) {
	c := clientcache.New(20*time.Millisecond, false)
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

func TestConcurrentGetRelease(t *testing.T) {
	c := clientcache.New(time.Hour, false)
	closer := &stubCloser{}
	live := func() int64 { return 0 }
	errs := make([]error, 50)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() {
			_, rel, err := c.GetOrCreate(99, newConstruct(closer, live))
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
	require.Equal(t, int32(0), closer.closed.Load(), "refcount>0 transitions must never close mid-flight")
}
