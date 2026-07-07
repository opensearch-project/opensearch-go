// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build ttlcache_dev

package ttlcache

import "fmt"

// onStrayKey panics on a mapKeys entry with no backing sync.Map value. This
// build tag (-tags ttlcache_dev) turns the should-never-happen lockstep
// violation into a hard failure so tests and development surface the bug
// instead of silently self-healing. Production builds log-and-reconcile
// instead; see the default build. Caller holds mu.
func (c *Cache[T]) onStrayKey(key Key) {
	panic(fmt.Sprintf("ttlcache: stray key %d in mapKeys with no cache entry", key))
}
