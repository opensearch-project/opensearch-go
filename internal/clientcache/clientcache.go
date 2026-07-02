// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package clientcache is a process-wide, refcounted, idle-TTL cache for
// implicitly-constructed default clients. It is transport-agnostic: it caches
// over a caller-supplied constructor and liveness probe, so it can live below
// both opensearch and opensearchapi without importing either.
package clientcache

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// HashKey identifies a cached entry by a hash of its resolved config.
type HashKey int64

// ClusterFunc wraps the transport io.Closer. The embedded Closer may be nil (a
// custom transport without Close), so every close site nil-checks it.
type ClusterFunc struct{ io.Closer }

// Constructed is what a cache entry carries: the client value handed to
// callers, its transport, and a liveness probe returning a monotonic request
// count. A nil Liveness makes an entry idle as soon as its refcount hits zero.
type Constructed[T any] struct {
	Value    T
	Closer   ClusterFunc
	Liveness func() int64
}

// ConstructFunc constructs a Constructed on a cache miss.
type ConstructFunc[T any] func() (Constructed[T], error)

type entry[T any] struct {
	val       Constructed[T]
	refCount  atomic.Int32 // >=0: live reference count; <0: claimed for eviction
	lastCount int64
	closed    atomic.Bool
}

// incIfLive increments the refcount unless the entry is claimed for eviction
// (refCount < 0), returning false so the caller reconstructs. Acquire half of
// the CAS-claim protocol: this and the sweep's CompareAndSwap(0, -1) arbitrate
// on one atomic word, so exactly one of evict/reacquire wins.
func (e *entry[T]) incIfLive() bool {
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

// Cache maps a config hash to a shared client entry. Reads go through the
// lock-free sync.Map; stores/deletes and the eviction sweep hold mu, which also
// guards the keys mirror the sweep iterates.
type Cache[T any] struct {
	cache  sync.Map // HashKey -> *entry[T]
	ttl    time.Duration
	cancel context.CancelFunc // set under mu when the worker is running

	mu struct {
		sync.Mutex
		keys    map[HashKey]struct{}
		running bool
	}
}

// New returns a cache with the given idle TTL: <0 disables caching (every
// GetOrCreate builds a fresh client and its release closes immediately), 0
// never evicts (entries live until process exit), >0 evicts entries idle for a
// full TTL window.
func New[T any](ttl time.Duration) *Cache[T] {
	c := &Cache[T]{ttl: ttl}
	c.mu.keys = make(map[HashKey]struct{})
	return c
}

// Len reports the number of cached entries.
func (c *Cache[T]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.mu.keys)
}

// GetOrCreate returns the entry for key, constructing it on a miss. The returned
// release decrements the entry's refcount exactly once; further calls are
// no-ops. When the cache is disabled (ttl < 0) nothing is stored and release
// closes the built transport.
func (c *Cache[T]) GetOrCreate(key HashKey, construct ConstructFunc[T]) (T, func() error, error) {
	var zero T
	if c.ttl < 0 {
		built, err := construct()
		if err != nil {
			return zero, nil, err
		}
		return built.Value, disabledRelease(built.Closer), nil
	}

	// Lock-free hit path.
	if v, ok := c.cache.Load(key); ok {
		e := v.(*entry[T])
		if e.incIfLive() {
			return e.val.Value, releaseFn(e), nil
		}
		// Claimed for eviction; fall through to the locked slow path, which
		// blocks until the sweep releases mu and removes the entry.
	}

	// Construct outside the lock (may do network setup).
	built, err := construct()
	if err != nil {
		return zero, nil, err
	}

	c.mu.Lock()
	// A concurrent goroutine may have inserted the same key while we
	// constructed. Under mu a present entry is always reacquirable (the sweep
	// cannot hold a half-evicted one).
	if v, ok := c.cache.Load(key); ok {
		e := v.(*entry[T])
		if e.incIfLive() {
			c.mu.Unlock()
			if built.Closer.Closer != nil {
				_ = built.Closer.Close() // discard the redundant build
			}
			return e.val.Value, releaseFn(e), nil
		}
	}
	e := &entry[T]{val: built}
	e.refCount.Store(1)
	if built.Liveness != nil {
		e.lastCount = built.Liveness()
	}
	c.cache.Store(key, e)
	c.mu.keys[key] = struct{}{}
	c.ensureWorkerLocked()
	c.mu.Unlock()
	return e.val.Value, releaseFn(e), nil
}

// releaseFn returns an idempotent refcount decrementer for e. The worker, not
// release, is the sole closer of a cached transport.
func releaseFn[T any](e *entry[T]) func() error {
	var once sync.Once
	return func() error {
		once.Do(func() { e.refCount.Add(-1) })
		return nil
	}
}

// disabledRelease returns an idempotent release that closes the built transport.
// A disabled cache stores nothing, so release owns teardown.
func disabledRelease(closer ClusterFunc) func() error {
	var once sync.Once
	return func() error {
		var err error
		once.Do(func() {
			if closer.Closer != nil {
				err = closer.Close()
			}
		})
		return err
	}
}

// ensureWorkerLocked starts the eviction worker if it is not already running.
// Caller must hold mu. A non-positive ttl means "never evict": no worker.
func (c *Cache[T]) ensureWorkerLocked() {
	if c.mu.running || c.ttl <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.mu.running = true
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
	for key := range c.mu.keys {
		v, ok := c.cache.Load(key)
		if !ok {
			delete(c.mu.keys, key) // reconcile a stray key
			continue
		}
		e := v.(*entry[T])
		if e.refCount.Load() != 0 {
			if e.val.Liveness != nil {
				e.lastCount = e.val.Liveness()
			}
			continue
		}
		var cur int64
		if e.val.Liveness != nil {
			cur = e.val.Liveness()
		}
		if e.val.Liveness == nil || cur == e.lastCount {
			// Idle for a full window: claim, close, evict. The claim CAS fails
			// if a concurrent hit reacquired the entry (0 -> 1), in which case
			// it is kept.
			if e.refCount.CompareAndSwap(0, -1) {
				if e.closed.CompareAndSwap(false, true) && e.val.Closer.Closer != nil {
					_ = e.val.Closer.Close()
				}
				c.cache.Delete(key)
				delete(c.mu.keys, key)
			}
			continue
		}
		e.lastCount = cur
	}
	// Stop the worker once empty. A stopping worker may briefly overlap a
	// replacement spawned by a concurrent insert and call cancel() twice; both
	// are safe (CancelFunc is idempotent, and this fires only on an empty
	// keyset, so no live entry goes unserviced).
	if len(c.mu.keys) == 0 {
		c.mu.running = false
		c.cancel()
	}
}
