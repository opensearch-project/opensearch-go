// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package ttlcache provides a process-wide, refcounted, idle-TTL cache of
// constructed-on-demand objects. It is transport-agnostic: it caches over a
// caller-supplied constructor and liveness probe, so it can live below both
// opensearch and opensearchapi without importing either.
package ttlcache

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// ErrNotCacheable is what a Cacheable's Key returns when the item cannot be
// cached (e.g. an un-hashable config). GetOrCreate then builds it fresh via New
// and never stores it, so its release closes the built value.
var ErrNotCacheable = errors.New("ttlcache: item is not cacheable")

// Key identifies a cached entry by a hash of the item's identity.
type Key int64

// ClusterFunc wraps a value's io.Closer. The embedded Closer may be nil (a
// value with nothing to close), so every close site nil-checks it.
type ClusterFunc struct{ io.Closer }

// Value is both what a Cacheable's New returns and the cache node itself: the
// object handed to callers, its closer, a liveness probe, plus the refcount
// bookkeeping the sweep uses to evict idle entries. refCount is >=0 for a live
// reference count and <0 once claimed for eviction.
type Value[T any] struct {
	Obj      T
	Closer   ClusterFunc
	Liveness func() int64

	refCount atomic.Int32 // live refs; >=0 in use, <0 once claimed for eviction (the closed marker)
	// lastLiveness is Liveness() at the previous sweep; unchanged == idle.
	// Guarded by Cache.mu: written under mu in GetOrCreate (before publish) and in sweep.
	lastLiveness int64
}

// Cacheable is an item the cache can key and build on demand. Key reports the
// item's cache key, or ErrNotCacheable when the item cannot be cached (the cache
// then builds it fresh via New and never stores it). New constructs the value on
// a miss and receives the context passed to GetOrCreate, so construction can
// honor cancellation and deadlines.
type Cacheable[T any] interface {
	Key() (Key, error)
	New(context.Context) (Value[T], error)
}

// incIfLive increments the refcount unless the entry is claimed for eviction
// (refCount < 0), returning false so the caller reconstructs. Acquire half of
// the CAS-claim protocol: this and the sweep's CompareAndSwap(0, -1) arbitrate
// on one atomic word, so exactly one of evict/reacquire wins.
func (e *Value[T]) incIfLive() bool {
	for {
		n := e.refCount.Load()
		if n < 0 {
			return false
		}
		if e.refCount.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

// Cache maps a key to a shared cached entry. Reads go through the
// lock-free sync.Map; stores/deletes and the eviction sweep hold mu, which also
// guards the mapKeys mirror the sweep iterates.
type Cache[T any] struct {
	cache sync.Map // Key -> *Value[T]
	ttl   time.Duration
	logf  func(string, ...any) // diagnostic sink for should-never-happen conditions; nil = silent

	mu struct {
		sync.Mutex
		// mapKeys and cancel are guarded by the embedded Mutex. mapKeys mirrors
		// the sync.Map keyset (kept in lockstep with every Store/Delete under mu)
		// so the sweep can iterate without racing writers; cancel stops the
		// eviction worker and is assigned only with mu held.
		mapKeys map[Key]struct{}
		cancel  context.CancelFunc // non-nil while the eviction worker runs; cleared when it stops
	}
}

// New returns a cache with the given idle TTL: <0 disables caching (every
// GetOrCreate builds a fresh value and its release closes immediately), 0
// never evicts (entries live until process exit), >0 evicts entries idle for a
// full TTL window. Options tune diagnostics; see WithLogger.
func New[T any](ttl time.Duration, opts ...Option[T]) *Cache[T] {
	c := &Cache[T]{ttl: ttl}
	c.mu.mapKeys = make(map[Key]struct{})
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Option configures a Cache at construction.
type Option[T any] func(*Cache[T])

// WithLogger installs a diagnostic sink for should-never-happen conditions
// (currently the stray-key reconcile in sweep). It is only consulted off the
// hot path, so a nil or absent logger leaves the cache silent.
func WithLogger[T any](logf func(string, ...any)) Option[T] {
	return func(c *Cache[T]) { c.logf = logf }
}

// Len reports the number of cached entries.
func (c *Cache[T]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.mu.mapKeys)
}

// GetOrCreate returns the cached value for item, constructing it on a miss via
// item.New(ctx). The returned release decrements the entry's refcount exactly
// once; further calls are no-ops. When the cache is disabled (ttl < 0) or item
// reports ErrNotCacheable, nothing is stored and release closes the built
// value.
func (c *Cache[T]) GetOrCreate(ctx context.Context, item Cacheable[T]) (T, func() error, error) {
	var zero T
	key, err := item.Key()
	if c.ttl < 0 || errors.Is(err, ErrNotCacheable) {
		built, berr := item.New(ctx)
		if berr != nil {
			return zero, nil, berr
		}
		return built.Obj, disabledRelease(built.Closer), nil
	}
	if err != nil {
		return zero, nil, err
	}

	// Lock-free hit path.
	if v, ok := c.cache.Load(key); ok {
		e := v.(*Value[T])
		if e.incIfLive() {
			return e.Obj, releaseFn(e), nil
		}
		// Claimed for eviction; fall through to the locked slow path, which
		// blocks until the sweep releases mu and removes the entry.
	}

	// Construct outside the lock (may do network setup).
	built, err := item.New(ctx)
	if err != nil {
		return zero, nil, err
	}

	c.mu.Lock()
	// A concurrent goroutine may have inserted the same key while we
	// constructed. Under mu a present entry is always reacquirable (the sweep
	// cannot hold a half-evicted one).
	if v, ok := c.cache.Load(key); ok {
		e := v.(*Value[T])
		if e.incIfLive() {
			c.mu.Unlock()
			if built.Closer.Closer != nil {
				_ = built.Closer.Close() // discard the redundant build
			}
			return e.Obj, releaseFn(e), nil
		}
	}
	e := &built
	e.refCount.Store(1)
	if built.Liveness != nil {
		e.lastLiveness = built.Liveness()
	}
	c.cache.Store(key, e)
	c.mu.mapKeys[key] = struct{}{}
	c.ensureWorkerLocked()
	c.mu.Unlock()
	return e.Obj, releaseFn(e), nil
}

// releaseFn returns an idempotent refcount decrementer for e. The worker, not
// release, is the sole closer of a cached value.
func releaseFn[T any](e *Value[T]) func() error {
	return sync.OnceValue(func() error {
		e.refCount.Add(-1)
		return nil
	})
}

// disabledRelease returns an idempotent release that closes the built value.
// A disabled cache stores nothing, so release owns teardown.
func disabledRelease(closer ClusterFunc) func() error {
	return sync.OnceValue(func() error {
		if closer.Closer != nil {
			return closer.Close()
		}
		return nil
	})
}

// ensureWorkerLocked starts the eviction worker if it is not already running.
// Caller must hold mu. A non-positive ttl means "never evict": no worker.
func (c *Cache[T]) ensureWorkerLocked() {
	if c.mu.cancel != nil || c.ttl <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.cancel = cancel
	go c.worker(ctx)
}

func (c *Cache[T]) worker(ctx context.Context) {
	ticker := time.NewTicker(c.ttl)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.sweep()
		}
	}
}

// sweep evicts idle refcount-0 entries. It skips the tick when GetOrCreate holds
// mu (eviction is best-effort). It stops the worker when the keyset empties.
func (c *Cache[T]) sweep() {
	if !c.mu.TryLock() {
		return
	}
	defer c.mu.Unlock()
	for key := range c.mu.mapKeys {
		v, ok := c.cache.Load(key)
		if !ok {
			// A key in mapKeys with no sync.Map entry is a lockstep-invariant
			// violation that cannot occur by construction (both are mutated
			// only together under mu). Treat it as a bug: reconcile so prod
			// stays healthy, but surface it loudly in development.
			c.onStrayKey(key)
			delete(c.mu.mapKeys, key)
			continue
		}
		e := v.(*Value[T])
		if e.refCount.Load() != 0 {
			if e.Liveness != nil {
				e.lastLiveness = e.Liveness()
			}
			continue
		}
		var cur int64
		if e.Liveness != nil {
			cur = e.Liveness()
		}
		if e.Liveness == nil || cur == e.lastLiveness {
			// Idle for a full window: claim, close, evict. The claim CAS fails
			// if a concurrent hit reacquired the entry (0 -> 1), in which case
			// it is kept. Winning the claim (refCount -1) is a once-per-entry
			// gate, so it is also the sole close.
			if e.refCount.CompareAndSwap(0, -1) {
				if e.Closer.Closer != nil {
					_ = e.Closer.Close()
				}
				c.cache.Delete(key)
				delete(c.mu.mapKeys, key)
			}
			continue
		}
		e.lastLiveness = cur
	}
	// Stop the worker once empty. A worker that already cancelled itself can
	// still sweep once more (a tick buffered in ticker.C races ctx.Done), so
	// the cancel may already be cleared; skip when nil. This fires only on an
	// empty keyset, so no live entry goes unserviced.
	if len(c.mu.mapKeys) == 0 {
		if cancel := c.mu.cancel; cancel != nil {
			c.mu.cancel = nil
			cancel()
		}
	}
}
