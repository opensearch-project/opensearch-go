// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !ttlcache_dev

package ttlcache

// onStrayKey handles a mapKeys entry with no backing sync.Map value: a
// should-never-happen lockstep violation. In production builds it emits a debug
// diagnostic through the installed logger (if any) and lets sweep reconcile,
// rather than crashing a live process over a self-healing inconsistency. Build
// with -tags ttlcache_dev to panic instead. Caller holds mu.
func (c *Cache[T]) onStrayKey(key Key) {
	if c.logf != nil {
		c.logf("ttlcache: stray key %d in mapKeys with no cache entry; reconciling", key)
	}
}
