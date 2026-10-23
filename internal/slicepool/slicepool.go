// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package slicepool pools slices of pointers and the elements they point to, so
// a hot path that fills one per call allocates neither the slice nor its
// elements.
package slicepool

import "sync"

// poolCap is the capacity of a slice a [Pool] makes, enough for a shard's
// primary and a few replicas.
const poolCap = 4

// Append extends *dst by one element and returns it. When the backing array
// already holds an element in that slot, from an earlier use of the slice, that
// element is returned as it is, contents and all, so the caller must set every
// field it reads.
func Append[T any](dst *[]*T) *T {
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

// Get returns an empty slice whose backing array may hold elements for
// [Append] to reuse.
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
