// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ttlcache_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/ttlcache"
)

type stubCloser struct{ closed atomic.Int32 }

func (s *stubCloser) Close() error { s.closed.Add(1); return nil }

// cacheable is a test ttlcache.Cacheable[io.Closer]. new builds the Value; if
// nil, it returns closer/live. notCacheable makes Key report ErrNotCacheable;
// keyErr makes Key report an arbitrary error.
type cacheable struct {
	key          ttlcache.Key
	notCacheable bool
	keyErr       error
	closer       io.Closer
	live         func() int64
	new          func(context.Context) (ttlcache.Value[io.Closer], error)
}

func (c cacheable) Key() (ttlcache.Key, error) {
	if c.keyErr != nil {
		return 0, c.keyErr
	}
	if c.notCacheable {
		return 0, ttlcache.ErrNotCacheable
	}
	return c.key, nil
}

func (c cacheable) New(ctx context.Context) (ttlcache.Value[io.Closer], error) {
	if c.new != nil {
		return c.new(ctx)
	}
	return ttlcache.Value[io.Closer]{
		Obj:      c.closer,
		Closer:   ttlcache.ClusterFunc{Closer: c.closer},
		Liveness: c.live,
	}, nil
}

func newCacheable(key ttlcache.Key, closer io.Closer, live func() int64) cacheable {
	return cacheable{key: key, closer: closer, live: live}
}

func TestGetOrCreate_SharesValueAndRefcounts(t *testing.T) {
	c := ttlcache.New[io.Closer](time.Hour)
	closer := &stubCloser{}
	live := func() int64 { return 0 }

	calls := 0
	construct := func(context.Context) (ttlcache.Value[io.Closer], error) {
		calls++
		return ttlcache.Value[io.Closer]{
			Obj:      closer,
			Closer:   ttlcache.ClusterFunc{Closer: closer},
			Liveness: live,
		}, nil
	}

	v1, rel1, err := c.GetOrCreate(context.Background(), cacheable{key: 42, new: construct})
	require.NoError(t, err)
	v2, rel2, err := c.GetOrCreate(context.Background(), cacheable{key: 42, new: construct})
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
	c := ttlcache.New[io.Closer](time.Hour)
	a := &stubCloser{}
	b := &stubCloser{}
	live := func() int64 { return 0 }

	va, _, _ := c.GetOrCreate(context.Background(), newCacheable(1, a, live))
	vb, _, _ := c.GetOrCreate(context.Background(), newCacheable(2, b, live))

	require.Same(t, a, va)
	require.Same(t, b, vb)
	require.Equal(t, 2, c.Len())
}

func TestDisabledCache_NeverStores_ReleaseCloses(t *testing.T) {
	c := ttlcache.New[io.Closer](-1) // negative ttl disables caching
	closer := &stubCloser{}
	live := func() int64 { return 0 }

	v, rel, err := c.GetOrCreate(context.Background(), newCacheable(1, closer, live))
	require.NoError(t, err)
	require.Same(t, closer, v)
	require.Equal(t, 0, c.Len(), "disabled cache stores nothing")
	require.NoError(t, rel())
	require.Equal(t, int32(1), closer.closed.Load(), "disabled release closes immediately")
	require.NoError(t, rel(), "disabled double release is a no-op")
	require.Equal(t, int32(1), closer.closed.Load(), "disabled release closes exactly once")
}

// TestNotCacheable_NeverStores_ReleaseCloses covers a Cacheable whose Key
// reports ErrNotCacheable on an otherwise caching cache: it must build fresh,
// store nothing, and let release own teardown -- the same contract as a disabled
// cache, but selected per-item rather than by ttl.
func TestNotCacheable_NeverStores_ReleaseCloses(t *testing.T) {
	c := ttlcache.New[io.Closer](time.Hour) // caching cache
	closer := &stubCloser{}
	item := cacheable{notCacheable: true, closer: closer, live: func() int64 { return 0 }}

	v, rel, err := c.GetOrCreate(context.Background(), item)
	require.NoError(t, err)
	require.Same(t, closer, v)
	require.Equal(t, 0, c.Len(), "un-cacheable item stores nothing")
	require.NoError(t, rel())
	require.Equal(t, int32(1), closer.closed.Load(), "un-cacheable release closes immediately")
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
			c := ttlcache.New[io.Closer](20 * time.Millisecond)
			closer := &stubCloser{}

			_, rel, err := c.GetOrCreate(context.Background(), newCacheable(1, closer, tt.live))
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
	c := ttlcache.New[io.Closer](time.Millisecond)
	live := func() int64 { return 1 } // constant => always idle-eligible at ref 0

	//nolint:unparam // signature matches Cacheable.New; this test's build always succeeds
	construct := func(context.Context) (ttlcache.Value[io.Closer], error) {
		closer := &stubCloser{}
		time.Sleep(3 * time.Millisecond) // widen the post-construct hit window
		return ttlcache.Value[io.Closer]{
			Obj:      closer,
			Closer:   ttlcache.ClusterFunc{Closer: closer},
			Liveness: live,
		}, nil
	}

	errs := make([]error, 300)
	var wg sync.WaitGroup
	for i := range 300 {
		wg.Go(func() {
			v, rel, err := c.GetOrCreate(context.Background(), cacheable{key: 7, new: construct})
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
	c := ttlcache.New[io.Closer](20 * time.Millisecond)
	closer := &stubCloser{}
	var counter atomic.Int64
	live := counter.Load

	_, rel, err := c.GetOrCreate(context.Background(), newCacheable(1, closer, live))
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

// TestWorker_KeepsReferencedEntry covers the sweep's refCount != 0 arm: a held
// entry (never released) survives every sweep regardless of liveness, and is
// evictable only once its last ref is released. Distinct from
// TestWorker_KeepsBusyEntry, which holds refcount 0 and relies on advancing
// liveness; here liveness is idle-eligible, so survival comes purely from the
// outstanding reference.
func TestWorker_KeepsReferencedEntry(t *testing.T) {
	c := ttlcache.New[io.Closer](20 * time.Millisecond)
	closer := &stubCloser{}
	live := func() int64 { return 0 } // idle-eligible: only the held ref keeps it

	_, rel, err := c.GetOrCreate(context.Background(), newCacheable(1, closer, live))
	require.NoError(t, err) // refcount 1, deliberately not released

	// Several sweep windows pass with the ref outstanding.
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, 1, c.Len(), "referenced entry must not be evicted")
	require.Equal(t, int32(0), closer.closed.Load(), "referenced entry must never be closed")

	// Releasing the last ref makes it evictable; the worker then closes it.
	require.NoError(t, rel())
	require.Eventually(t, func() bool {
		return c.Len() == 0 && closer.closed.Load() == 1
	}, 2*time.Second, 10*time.Millisecond, "entry must evict+close once its last ref is released")
}

func TestWorker_StopsWhenEmpty(t *testing.T) {
	c := ttlcache.New[io.Closer](20 * time.Millisecond)
	closer := &stubCloser{}
	live := func() int64 { return 0 }
	_, rel, _ := c.GetOrCreate(context.Background(), newCacheable(1, closer, live))
	require.NoError(t, rel())
	require.Eventually(t, func() bool { return c.Len() == 0 }, 2*time.Second, 10*time.Millisecond)
	// Re-insert must respawn the worker and evict again.
	closer2 := &stubCloser{}
	_, rel2, _ := c.GetOrCreate(context.Background(), newCacheable(2, closer2, live))
	require.NoError(t, rel2())
	require.Eventually(t, func() bool {
		return c.Len() == 0 && closer2.closed.Load() == 1
	}, 2*time.Second, 10*time.Millisecond, "worker must respawn after emptying")
}

func TestNeverEvict_TTLZero_NoWorker(t *testing.T) {
	c := ttlcache.New[io.Closer](0) // 0 => never evict, no worker spawns
	closer := &stubCloser{}
	live := func() int64 { return 0 } // idle-eligible, but nothing should ever evict it

	_, rel, err := c.GetOrCreate(context.Background(), newCacheable(1, closer, live))
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
	c := ttlcache.New[io.Closer](time.Hour)
	live := func() int64 { return 0 }
	//nolint:unparam // signature matches Cacheable.New; this test's build always succeeds
	construct := func(context.Context) (ttlcache.Value[io.Closer], error) {
		closer := &stubCloser{}
		return ttlcache.Value[io.Closer]{
			Obj:      closer,
			Closer:   ttlcache.ClusterFunc{Closer: closer},
			Liveness: live,
		}, nil
	}
	errs := make([]error, 50)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() {
			_, rel, err := c.GetOrCreate(context.Background(), cacheable{key: 99, new: construct})
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
	v, rel, err := c.GetOrCreate(context.Background(), cacheable{key: 99, new: construct})
	require.NoError(t, err)
	require.Equal(t, int32(0), v.(*stubCloser).closed.Load(), "live cached transport must not be closed mid-flight")
	require.NoError(t, rel())
}

// TestKeyError_Propagates covers a Cacheable whose Key fails with an error
// other than ErrNotCacheable: GetOrCreate returns it and never calls New.
func TestKeyError_Propagates(t *testing.T) {
	c := ttlcache.New[io.Closer](time.Hour)
	wantErr := errors.New("key failed")
	newCalled := false
	item := cacheable{
		keyErr: wantErr,
		new: func(context.Context) (ttlcache.Value[io.Closer], error) {
			newCalled = true
			return ttlcache.Value[io.Closer]{}, nil
		},
	}

	v, rel, err := c.GetOrCreate(context.Background(), item)
	require.ErrorIs(t, err, wantErr)
	require.Nil(t, v)
	require.Nil(t, rel)
	require.False(t, newCalled, "New must not run when Key errors")
	require.Equal(t, 0, c.Len())
}

func TestGetOrCreate_ConstructError(t *testing.T) {
	wantErr := errors.New("construct failed")
	//nolint:unparam // signature matches Cacheable.New; this fixture always fails, so Value is always zero
	failing := func(context.Context) (ttlcache.Value[io.Closer], error) {
		return ttlcache.Value[io.Closer]{}, wantErr
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
			c := ttlcache.New[io.Closer](tt.ttl)
			v, rel, err := c.GetOrCreate(context.Background(), cacheable{key: 1, new: failing})
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
	nilConstruct := func(context.Context) (ttlcache.Value[io.Closer], error) {
		return ttlcache.Value[io.Closer]{
			Obj:      nil,
			Closer:   ttlcache.ClusterFunc{Closer: nil},
			Liveness: func() int64 { return 0 },
		}, nil
	}

	t.Run("disabled release", func(t *testing.T) {
		c := ttlcache.New[io.Closer](-1)
		_, rel, err := c.GetOrCreate(context.Background(), cacheable{key: 1, new: nilConstruct})
		require.NoError(t, err)
		require.NotNil(t, rel)
		require.NotPanics(t, func() { _ = rel() }, "nil closer release must not panic")
	})

	t.Run("idle eviction", func(t *testing.T) {
		c := ttlcache.New[io.Closer](20 * time.Millisecond)
		_, rel, err := c.GetOrCreate(context.Background(), cacheable{key: 1, new: nilConstruct})
		require.NoError(t, err)
		require.NoError(t, rel())
		require.Eventually(t, func() bool { return c.Len() == 0 }, 2*time.Second, 10*time.Millisecond,
			"idle entry with nil closer must evict without panic")
	})
}
