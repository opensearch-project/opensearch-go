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
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// Constructed is the result of a cache-miss constructor: the opaque client
// value handed to callers, its transport as an io.Closer, and a liveness probe
// returning a monotonic request count used to detect idleness. A nil Liveness
// makes an entry idle as soon as its refcount reaches zero.
type Constructed struct {
	Value    any
	Closer   io.Closer
	Liveness func() int64
}

type entry struct {
	value     any
	closer    io.Closer
	liveness  func() int64
	refCount  atomic.Int32
	lastCount int64
	closed    atomic.Bool
}

// Cache maps a config hash to a shared client entry.
type Cache struct {
	mu       sync.Mutex
	entries  map[uint64]*entry
	ttl      time.Duration
	disabled bool
	running  bool
	stop     chan struct{}
}

// New returns a cache with the given idle TTL. When disabled, GetOrCreate never
// stores and its release closes immediately.
func New(ttl time.Duration, disabled bool) *Cache {
	return &Cache{
		entries:  make(map[uint64]*entry),
		ttl:      ttl,
		disabled: disabled,
	}
}

// Len reports the number of cached entries.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// GetOrCreate returns the entry for key, constructing it on a miss. The returned
// release decrements the entry's refcount exactly once; further calls are no-ops.
func (c *Cache) GetOrCreate(key uint64, construct func() (Constructed, error)) (any, func() error, error) {
	if c.disabled {
		built, err := construct()
		if err != nil {
			return nil, nil, err
		}
		var once sync.Once
		release := func() error {
			var rerr error
			once.Do(func() {
				if built.Closer != nil {
					rerr = built.Closer.Close()
				}
			})
			return rerr
		}
		return built.Value, release, nil
	}

	c.mu.Lock()
	if e, ok := c.entries[key]; ok {
		e.refCount.Add(1)
		c.mu.Unlock()
		return e.value, releaseFn(e), nil
	}
	c.mu.Unlock()

	// Construct outside the lock (may do network setup).
	built, err := construct()
	if err != nil {
		return nil, nil, err
	}

	c.mu.Lock()
	// Another goroutine may have inserted the same key while we constructed.
	// Increment the refcount before releasing the lock, so a decrement-to-zero
	// plus worker eviction cannot close a transport we are about to hand out.
	if e, ok := c.entries[key]; ok {
		e.refCount.Add(1)
		c.mu.Unlock()
		if built.Closer != nil {
			_ = built.Closer.Close() // discard the redundant build
		}
		return e.value, releaseFn(e), nil
	}
	e := &entry{value: built.Value, closer: built.Closer, liveness: built.Liveness}
	e.refCount.Store(1)
	if built.Liveness != nil {
		e.lastCount = built.Liveness()
	}
	c.entries[key] = e
	c.ensureWorkerLocked()
	c.mu.Unlock()
	return e.value, releaseFn(e), nil
}

// releaseFn returns an idempotent refcount decrementer for e. The worker, not
// release, is the sole closer of the transport.
func releaseFn(e *entry) func() error {
	var once sync.Once
	return func() error {
		once.Do(func() { e.refCount.Add(-1) })
		return nil
	}
}

// ensureWorkerLocked starts the eviction worker if it is not already running.
// Caller must hold c.mu. A non-positive ttl means "never evict": no worker.
func (c *Cache) ensureWorkerLocked() {
	if c.running || c.ttl <= 0 {
		return
	}
	c.running = true
	c.stop = make(chan struct{})
	ticker := time.NewTicker(c.ttl)
	go c.worker(ticker)
}

func (c *Cache) worker(ticker *time.Ticker) {
	defer ticker.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
			if c.sweep() {
				return // map emptied: stop the worker
			}
		}
	}
}

// sweep evicts idle refcount-0 entries. It returns true when the map is empty
// afterward, signaling the worker to stop.
func (c *Cache) sweep() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, e := range c.entries {
		if e.refCount.Load() != 0 {
			if e.liveness != nil {
				e.lastCount = e.liveness()
			}
			continue
		}
		var cur int64
		if e.liveness != nil {
			cur = e.liveness()
		}
		if e.liveness == nil || cur == e.lastCount {
			// Idle for a full window: close and evict.
			if e.closed.CompareAndSwap(false, true) && e.closer != nil {
				_ = e.closer.Close()
			}
			delete(c.entries, key)
			continue
		}
		e.lastCount = cur
	}
	if len(c.entries) == 0 {
		c.running = false
		close(c.stop)
		return true
	}
	return false
}
