// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport //nolint:testpackage // Benchmarks access unexported decayCounter/timeWeightedCounter.

import (
	"sync/atomic"
	"testing"
)

// --- decayCounter benchmarks (used by indexSlot.requestDecay) ---

func BenchmarkDecayCounter(b *testing.B) {
	b.Run("Add", func(b *testing.B) {
		var c decayCounter
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			c.add(0.999, 60.0)
		}
	})

	b.Run("Increment", func(b *testing.B) {
		var c decayCounter
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			c.increment(0.999)
		}
	})

	b.Run("Load", func(b *testing.B) {
		var c decayCounter
		c.store(1000.0)
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = c.load()
		}
	})

	b.Run("Contended_Add_4G", func(b *testing.B) {
		var c decayCounter
		b.SetParallelism(4)
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				c.add(0.999, 60.0)
			}
		})
	})

	b.Run("Contended_Add_16G", func(b *testing.B) {
		var c decayCounter
		b.SetParallelism(16)
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				c.add(0.999, 60.0)
			}
		})
	})
}

// --- timeWeightedCounter benchmarks (used by Connection.affinityCounter) ---

func BenchmarkTimeWeightedCounter(b *testing.B) {
	b.Run("Add", func(b *testing.B) {
		c := timeWeightedCounter{clock: realClock{}}
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			c.add(60.0)
		}
	})

	b.Run("Load", func(b *testing.B) {
		c := timeWeightedCounter{clock: realClock{}}
		c.store(1000.0)
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = c.load()
		}
	})

	b.Run("AddThenLoad", func(b *testing.B) {
		c := timeWeightedCounter{clock: realClock{}}
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if i%2 == 0 {
				c.add(60.0)
			} else {
				_ = c.load()
			}
		}
	})

	b.Run("Contended_Add_4G", func(b *testing.B) {
		c := timeWeightedCounter{clock: realClock{}}
		b.SetParallelism(4)
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				c.add(60.0)
			}
		})
	})

	b.Run("Contended_Add_16G", func(b *testing.B) {
		c := timeWeightedCounter{clock: realClock{}}
		b.SetParallelism(16)
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				c.add(60.0)
			}
		})
	})

	b.Run("Contended_ReadHeavy_4G", func(b *testing.B) {
		// Simulates the typical access pattern: many concurrent load() readers
		// with occasional add() writers. Uses atomic counter to give ~12.5% of
		// goroutine iterations to add() and the rest to load().
		c := timeWeightedCounter{clock: realClock{}}
		c.store(1000.0)
		var seq atomic.Int64
		b.SetParallelism(4)
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if seq.Add(1)%8 == 0 {
					c.add(60.0)
				} else {
					_ = c.load()
				}
			}
		})
	})
}
