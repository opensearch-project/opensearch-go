// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"cmp"
	"iter"
	"maps"
	"slices"
)

// set is a generic map-backed set, used throughout osgen in place of bare
// map[T]struct{} literals so membership tests read as intent (s.has(k)).
//
// Being a map type, a set is a reference: passing one by value shares the
// underlying storage; use clone to get an independent copy (e.g. to derive a
// modified view of a package-global set without mutating it).
type set[T comparable] map[T]struct{}

// newSet returns a set containing keys.
func newSet[T comparable](keys ...T) set[T] {
	s := make(set[T], len(keys))
	for _, k := range keys {
		s[k] = struct{}{}
	}
	return s
}

// add inserts k.
func (s set[T]) add(k T) { s[k] = struct{}{} }

// has reports whether k is present. Safe to call on a nil set.
func (s set[T]) has(k T) bool {
	_, ok := s[k]
	return ok
}

// clone returns an independent copy, so a caller can derive a modified view of a
// shared/global set without mutating the original. Unlike maps.Clone, it never
// returns nil: cloning a nil set yields an empty, ready-to-mutate set, so callers
// can add to the result without a nil check.
func (s set[T]) clone() set[T] {
	if s == nil {
		return make(set[T])
	}
	return maps.Clone(s)
}

// sortedKeys returns a range-over-func iterator that yields a set's members in
// ascending order, so ranging a set is deterministic by construction (important
// for reproducible codegen). It is a free function rather than a method because
// ordering needs cmp.Ordered, a tighter constraint than set's comparable.
//
//	for k := range sortedKeys(s) { ... }
func sortedKeys[T cmp.Ordered](s set[T]) iter.Seq[T] {
	return slices.Values(slices.Sorted(maps.Keys(s)))
}
