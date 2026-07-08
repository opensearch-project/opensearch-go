// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ttlcache

import "fmt"

// panicOnInvariantViolation makes should-never-happen conditions fatal instead
// of self-healing. It is false in a shipped binary and set true by an init in
// straykey_internal_test.go, so `go test` fails loudly on a defect while a
// production process reconciles and stays up.
var panicOnInvariantViolation bool

// onStrayKey handles a mapKeys entry with no backing sync.Map value: a
// should-never-happen lockstep violation, since both are only ever mutated
// together under mu. Under `go test` it panics so the defect surfaces loudly;
// in a production binary it emits a debug diagnostic through the installed
// logger (if any) and lets sweep reconcile, rather than crashing a live process
// over a self-healing inconsistency. Caller holds mu.
func (c *Cache[T]) onStrayKey(key Key) {
	if panicOnInvariantViolation {
		panic(fmt.Sprintf("ttlcache: stray key %d in mapKeys with no cache entry", key))
	}
	if c.logf != nil {
		c.logf("ttlcache: stray key %d in mapKeys with no cache entry; reconciling", key)
	}
}
