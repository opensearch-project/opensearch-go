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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/opensearch-project/opensearch-go/v5/internal/ttlcache"
)

// Timing budget for eviction tests. A CI runner under CPU-share starvation can
// have its scheduler bumped to ~1s granularity, so sub-second SLAs flake. We
// pick a sweep TTL comfortably above that floor and poll for whole multiples of
// it, trading a few seconds of wall time for determinism.
const (
	sweepTTL     = 500 * time.Millisecond // eviction window; one worker tick
	pollInterval = sweepTTL / 5           // Eventually/Never sampling cadence
	// evictWait spans many sweep windows so a starved runner still gets several
	// real ticks before we give up on an expected eviction.
	evictWait = 20 * sweepTTL // 10s
	// holdWindow spans several sweep windows to prove a kept entry survives
	// repeated ticks, not just one.
	holdWindow = 4 * sweepTTL // 2s
	// concurrentGoroutines drives the reacquire-vs-evict race; large enough to
	// interleave many hits across the post-construct window.
	concurrentGoroutines = 300
	// raceTTL and raceConstruct drive the reacquire-vs-evict race: a sweep window
	// far below the construct time means the entry becomes idle-eligible while it
	// is still being built, so goroutines hit the post-construct path as the
	// refcount churns 0<->non-zero. raceConstruct > raceTTL is what widens that
	// window; the exact values only need that ordering, not the CI-jitter floor
	// the eviction tests need (those goroutines are already blocked in construct).
	raceTTL       = time.Millisecond
	raceConstruct = 3 * raceTTL
	// hitIterations accesses re-issued one accessInterval apart; each must be a
	// cache hit, so construct runs exactly once across the whole run.
	hitIterations  = 20
	accessInterval = sweepTTL // one access per sweep window keeps liveness advancing
)

type stubCloser struct{ closed atomic.Int32 }

func (s *stubCloser) Close() error { s.closed.Add(1); return nil }

// cacheable is a test ttlcache.Cacheable[io.Closer]. build makes the Value; if
// nil, it returns closer/live. notCacheable makes Key report ErrNotCacheable;
// keyErr makes Key report an arbitrary error.
type cacheable struct {
	key          ttlcache.Key
	notCacheable bool
	keyErr       error
	closer       io.Closer
	live         func() int64
	build        func(context.Context) (ttlcache.Value[io.Closer], error)
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
	if c.build != nil {
		return c.build(ctx)
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

	v1, rel1, err := c.GetOrCreate(t.Context(), cacheable{key: 42, build: construct})
	require.NoError(t, err)
	v2, rel2, err := c.GetOrCreate(t.Context(), cacheable{key: 42, build: construct})
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

	va, _, _ := c.GetOrCreate(t.Context(), newCacheable(1, a, live))
	vb, _, _ := c.GetOrCreate(t.Context(), newCacheable(2, b, live))

	require.Same(t, a, va)
	require.Same(t, b, vb)
	require.Equal(t, 2, c.Len())
}

func TestDisabledCache_NeverStores_ReleaseCloses(t *testing.T) {
	c := ttlcache.New[io.Closer](-1) // negative ttl disables caching
	closer := &stubCloser{}
	live := func() int64 { return 0 }

	v, rel, err := c.GetOrCreate(t.Context(), newCacheable(1, closer, live))
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

	v, rel, err := c.GetOrCreate(t.Context(), item)
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
			c := ttlcache.New[io.Closer](sweepTTL)
			closer := &stubCloser{}

			_, rel, err := c.GetOrCreate(t.Context(), newCacheable(1, closer, tt.live))
			require.NoError(t, err)
			require.NoError(t, rel()) // refcount now 0

			require.Eventually(t, func() bool {
				return c.Len() == 0 && closer.closed.Load() == 1
			}, evictWait, pollInterval, "idle refcount-0 entry must be evicted+closed")
		})
	}
}

// TestConcurrentReacquireVsEviction guards the CAS-claim invariant: a slow
// constructor forces many goroutines through the post-construct hit path while
// the entry churns between refcount 0 and non-zero, and a handed-out value must
// never already be closed.
func TestConcurrentReacquireVsEviction(t *testing.T) {
	c := ttlcache.New[io.Closer](raceTTL)
	live := func() int64 { return 1 } // constant => always idle-eligible at ref 0

	//nolint:unparam // signature matches Cacheable.New; this test's build always succeeds
	construct := func(context.Context) (ttlcache.Value[io.Closer], error) {
		closer := &stubCloser{}
		time.Sleep(raceConstruct) // widen the post-construct hit window
		return ttlcache.Value[io.Closer]{
			Obj:      closer,
			Closer:   ttlcache.ClusterFunc{Closer: closer},
			Liveness: live,
		}, nil
	}

	var g errgroup.Group
	for range concurrentGoroutines {
		g.Go(func() error {
			v, rel, err := c.GetOrCreate(t.Context(), cacheable{key: 7, build: construct})
			if err != nil {
				return err
			}
			defer func() { _ = rel() }()
			if sc, ok := v.(*stubCloser); ok && sc.closed.Load() != 0 {
				return errClosedHandout
			}
			return nil
		})
	}
	require.NoError(t, g.Wait())
}

var errClosedHandout = closedHandoutError{}

type closedHandoutError struct{}

func (closedHandoutError) Error() string { return "GetOrCreate returned a closed transport" }

// TestWorker_SustainedHitsAcrossWindows re-accesses one key hitIterations times,
// one sweep window apart, and asserts every access is a cache hit (construct
// runs exactly once). This is the CPU-starvation-robust replacement for a
// short-SLA eviction race: it spans many real worker ticks, so a runner whose
// scheduler is bumped to coarse granularity still exercises multiple sweeps
// without flaking. A reference is held throughout, so the entry is never
// eviction-eligible and the hit count is deterministic.
func TestWorker_SustainedHitsAcrossWindows(t *testing.T) {
	c := ttlcache.New[io.Closer](sweepTTL)
	closer := &stubCloser{}
	live := func() int64 { return 0 }

	var builds atomic.Int32
	construct := func(context.Context) (ttlcache.Value[io.Closer], error) {
		builds.Add(1)
		return ttlcache.Value[io.Closer]{
			Obj:      closer,
			Closer:   ttlcache.ClusterFunc{Closer: closer},
			Liveness: live,
		}, nil
	}

	releases := make([]func() error, 0, hitIterations)
	for range hitIterations {
		v, rel, err := c.GetOrCreate(t.Context(), cacheable{key: 1, build: construct})
		require.NoError(t, err)
		require.Same(t, closer, v)
		releases = append(releases, rel)
		require.Equal(t, 1, c.Len(), "entry must stay cached across every window")
		time.Sleep(accessInterval)
	}
	require.Equal(t, int32(1), builds.Load(), "all %d accesses must be cache hits (one build)", hitIterations)
	require.Equal(t, int32(0), closer.closed.Load(), "sustained-hit entry must never be closed")

	for _, rel := range releases {
		require.NoError(t, rel())
	}
}

func TestWorker_KeepsBusyEntry(t *testing.T) {
	c := ttlcache.New[io.Closer](sweepTTL)
	closer := &stubCloser{}
	var counter atomic.Int64
	live := counter.Load

	_, rel, err := c.GetOrCreate(t.Context(), newCacheable(1, closer, live))
	require.NoError(t, err)
	require.NoError(t, rel()) // refcount 0 but counter keeps advancing

	stop := make(chan struct{})
	go func() {
		tk := time.NewTicker(pollInterval) // advance liveness well within each sweep window
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
	time.Sleep(holdWindow)
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
	c := ttlcache.New[io.Closer](sweepTTL)
	closer := &stubCloser{}
	live := func() int64 { return 0 } // idle-eligible: only the held ref keeps it

	_, rel, err := c.GetOrCreate(t.Context(), newCacheable(1, closer, live))
	require.NoError(t, err) // refcount 1, deliberately not released

	// Several sweep windows pass with the ref outstanding.
	time.Sleep(holdWindow)
	require.Equal(t, 1, c.Len(), "referenced entry must not be evicted")
	require.Equal(t, int32(0), closer.closed.Load(), "referenced entry must never be closed")

	// Releasing the last ref makes it evictable; the worker then closes it.
	require.NoError(t, rel())
	require.Eventually(t, func() bool {
		return c.Len() == 0 && closer.closed.Load() == 1
	}, evictWait, pollInterval, "entry must evict+close once its last ref is released")
}

func TestWorker_StopsWhenEmpty(t *testing.T) {
	c := ttlcache.New[io.Closer](sweepTTL)
	closer := &stubCloser{}
	live := func() int64 { return 0 }
	_, rel, _ := c.GetOrCreate(t.Context(), newCacheable(1, closer, live))
	require.NoError(t, rel())
	require.Eventually(t, func() bool { return c.Len() == 0 }, evictWait, pollInterval)
	// Re-insert must respawn the worker and evict again.
	closer2 := &stubCloser{}
	_, rel2, _ := c.GetOrCreate(t.Context(), newCacheable(2, closer2, live))
	require.NoError(t, rel2())
	require.Eventually(t, func() bool {
		return c.Len() == 0 && closer2.closed.Load() == 1
	}, evictWait, pollInterval, "worker must respawn after emptying")
}

func TestNeverEvict_TTLZero_NoWorker(t *testing.T) {
	c := ttlcache.New[io.Closer](0) // 0 => never evict, no worker spawns
	closer := &stubCloser{}
	live := func() int64 { return 0 } // idle-eligible, but nothing should ever evict it

	_, rel, err := c.GetOrCreate(t.Context(), newCacheable(1, closer, live))
	require.NoError(t, err)
	require.Equal(t, 1, c.Len())
	require.NoError(t, rel()) // refcount 0: with a worker this would eventually evict

	// No worker exists to evict; the entry must persist and never be closed.
	require.Never(t, func() bool {
		return c.Len() == 0 || closer.closed.Load() != 0
	}, holdWindow, pollInterval, "ttl=0 must never evict or close")
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
	var g errgroup.Group
	for range concurrentGoroutines {
		g.Go(func() error {
			_, rel, err := c.GetOrCreate(t.Context(), cacheable{key: 99, build: construct})
			if err != nil {
				return err
			}
			return rel()
		})
	}
	require.NoError(t, g.Wait())
	require.LessOrEqual(t, c.Len(), 1)
	// The surviving cached transport must never be closed while refcounts churn
	// above zero. Redundant builds lost to the construct race are closed by
	// GetOrCreate per spec, and deliberately not asserted here.
	v, rel, err := c.GetOrCreate(t.Context(), cacheable{key: 99, build: construct})
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
		build: func(context.Context) (ttlcache.Value[io.Closer], error) {
			newCalled = true
			return ttlcache.Value[io.Closer]{}, nil
		},
	}

	v, rel, err := c.GetOrCreate(t.Context(), item)
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
			v, rel, err := c.GetOrCreate(t.Context(), cacheable{key: 1, build: failing})
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
		_, rel, err := c.GetOrCreate(t.Context(), cacheable{key: 1, build: nilConstruct})
		require.NoError(t, err)
		require.NotNil(t, rel)
		require.NotPanics(t, func() { _ = rel() }, "nil closer release must not panic")
	})

	t.Run("idle eviction", func(t *testing.T) {
		c := ttlcache.New[io.Closer](sweepTTL)
		_, rel, err := c.GetOrCreate(t.Context(), cacheable{key: 1, build: nilConstruct})
		require.NoError(t, err)
		require.NoError(t, rel())
		require.Eventually(t, func() bool { return c.Len() == 0 }, evictWait, pollInterval,
			"idle entry with nil closer must evict without panic")
	})
}
