// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import "sync"

// floatSlicePool pools []float64 buffers used for per-candidate scoring
// and extra-cost slices. sync.Pool clears entries every GC cycle, so
// oversized buffers from transient spikes don't persist indefinitely.
//
//nolint:gochecknoglobals // sync.Pool must be package-level
var floatSlicePool = sync.Pool{
	New: func() any {
		s := make([]float64, 0, 16)
		return &s
	},
}

// pooledFloats is a pooled []float64 buffer. The zero value represents an
// empty/nil result; calling Release on it is a no-op.
type pooledFloats struct{ p *[]float64 }

// acquireFloats returns a pooled []float64 with length n.
func acquireFloats(n int) pooledFloats {
	bp := floatSlicePool.Get().(*[]float64) //nolint:forcetypeassert // pool only stores *[]float64
	if cap(*bp) < n {
		*bp = make([]float64, n)
	} else {
		*bp = (*bp)[:n]
	}
	return pooledFloats{p: bp}
}

// Slice returns the underlying []float64.
func (b pooledFloats) Slice() []float64 {
	if b.p == nil {
		return nil
	}
	return *b.p
}

// Len returns the length of the pooled slice.
func (b pooledFloats) Len() int {
	if b.p == nil {
		return 0
	}
	return len(*b.p)
}

// Release returns the buffer to the pool. Safe to call on zero-value.
func (b pooledFloats) Release() {
	if b.p == nil {
		return
	}
	*b.p = (*b.p)[:0]
	floatSlicePool.Put(b.p)
}

// pooledConns is a pooled []*Connection buffer. The zero value represents an
// empty/nil result; calling Release on it is a no-op.
type pooledConns struct{ p *[]*Connection }

// acquireConns returns a pooled []*Connection with length n.
func acquireConns(n int) pooledConns {
	bp := connSlicePool.Get().(*[]*Connection) //nolint:forcetypeassert // pool only stores *[]*Connection
	if cap(*bp) < n {
		*bp = make([]*Connection, n)
	} else {
		*bp = (*bp)[:n]
	}
	return pooledConns{p: bp}
}

// Slice returns the underlying []*Connection.
func (b pooledConns) Slice() []*Connection {
	if b.p == nil {
		return nil
	}
	return *b.p
}

// Len returns the length of the pooled slice.
func (b pooledConns) Len() int {
	if b.p == nil {
		return 0
	}
	return len(*b.p)
}

// Release clears pointer references and returns the buffer to the pool.
// Safe to call on zero-value.
func (b pooledConns) Release() {
	if b.p == nil {
		return
	}
	s := *b.p
	clear(s[:cap(s)])
	*b.p = s[:0]
	connSlicePool.Put(b.p)
}
