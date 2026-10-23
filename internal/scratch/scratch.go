// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package scratch holds reusable slices of pointers, so a hot path that fills
// one per call allocates neither the slice nor its elements.
package scratch

import "sync"

// poolCap is the capacity of a slice a [Pool] makes, enough for a shard's
// primary and a few replicas.
const poolCap = 4

// Next extends *dst by one element and returns it, reusing the pointer already
// in the backing array when there is one.
func Next[T any](dst *[]*T) *T {
	n := len(*dst)
	if n < cap(*dst) {
		*dst = (*dst)[:n+1]
		if (*dst)[n] == nil {
			(*dst)[n] = new(T)
		}

		return (*dst)[n]
	}

	t := new(T)
	*dst = append(*dst, t)

	return t
}

// Pool is a [sync.Pool] of *[]*T. The zero Pool is ready to use.
type Pool[T any] struct {
	p sync.Pool
}

// Get returns an empty slice whose backing array may hold elements to reuse
// through [Next].
func (p *Pool[T]) Get() *[]*T {
	if s, ok := p.p.Get().(*[]*T); ok {
		return s
	}
	s := make([]*T, 0, poolCap)

	return &s
}

// Put empties s, keeping its elements for reuse, and returns it to the pool.
func (p *Pool[T]) Put(s *[]*T) {
	*s = (*s)[:0]
	p.p.Put(s)
}
