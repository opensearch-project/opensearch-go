// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchutil

import "sync"

// FrontierTracker reports the frontier of each of a set of ordered streams whose
// records finish in any order: the lowest sequence number that is not yet
// complete. Every sequence below a stream's frontier is done.
//
// A [BulkIndexer] completes records out of order: batches for different shards
// flush at different times. A stream cursor, such as a Kafka consumer group
// offset, may only move forward past a record once every record before it is
// done. The tracker remembers which sequence numbers have completed, and
// reports the lowest one that has not, the frontier: every sequence below it is
// done, and the frontier itself is not. For a stream whose positions are offsets,
// the frontier is exactly the offset to commit.
//
// This differs from marking each record as it completes in a client library
// that keeps a per-partition maximum. Such a mark moves to the highest record
// completed, so completing out of order commits past a gap and loses the
// records in it. The frontier is the end of the contiguous run of completed
// records, so it never passes a gap.
//
// There is deliberately no failed state. A failed record must not advance the
// frontier, and a record that is retried keeps its original sequence number, so
// not calling [FrontierTracker.Complete] for it is the correct handling: the
// frontier stalls below it until the retry lands. The cost of a stall is memory: every
// later completion of that stream is held until the missing one arrives.
//
// The streams are identified by a key of any comparable type, such as a topic
// and partition. The zero value is ready to use, and a FrontierTracker is safe
// for concurrent use; it must not be copied after first use.
type FrontierTracker[K comparable] struct {
	mu struct {
		sync.RWMutex
		streams map[K]*frontierStream
	}
}

// frontierStream is the state of one stream: next is its frontier, the lowest
// sequence not yet complete, and done holds the completed sequences above it.
type frontierStream struct {
	mu struct {
		sync.Mutex
		next int64
		done map[int64]struct{}
	}
}

// Begin starts tracking the stream k, whose first sequence to track is next:
// every sequence below next counts as already complete, so the frontier of k
// starts at next. For a Kafka partition, next is the offset the
// consumer starts reading from.
//
// Beginning a stream that is already tracked discards what the tracker knew of
// it and starts over at next, as happens when a partition is assigned again.
func (t *FrontierTracker[K]) Begin(k K, next int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.mu.streams == nil {
		t.mu.streams = make(map[K]*frontierStream)
	}
	s := &frontierStream{}
	s.mu.next = next
	t.mu.streams[k] = s
}

// Complete records that the record with sequence seq on stream k is done. It
// may be called in any order and any number of times for a sequence. A
// sequence below the frontier, and any sequence on a stream that is not
// tracked, is ignored.
func (t *FrontierTracker[K]) Complete(k K, seq int64) {
	t.mu.RLock()
	s := t.mu.streams[k]
	t.mu.RUnlock()

	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch {
	case seq < s.mu.next:
		// Already counted.
	case seq > s.mu.next:
		if s.mu.done == nil {
			s.mu.done = make(map[int64]struct{})
		}
		s.mu.done[seq] = struct{}{}
	default:
		// The frontier arrived: move the frontier past it and past every
		// contiguous completion that was waiting on it.
		s.mu.next++
		for {
			if _, ok := s.mu.done[s.mu.next]; !ok {
				break
			}
			delete(s.mu.done, s.mu.next)
			s.mu.next++
		}
	}
}

// Frontier reports the frontier of stream k: the lowest sequence that is not
// complete, below which every sequence is. The bool is false when k is not
// tracked.
func (t *FrontierTracker[K]) Frontier(k K) (int64, bool) {
	t.mu.RLock()
	s := t.mu.streams[k]
	t.mu.RUnlock()

	if s == nil {
		return 0, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.mu.next, true
}

// Frontiers reports the frontier of every tracked stream, as
// [FrontierTracker.Frontier] does for one. The map is a snapshot the caller owns.
func (t *FrontierTracker[K]) Frontiers() map[K]int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	frontiers := make(map[K]int64, len(t.mu.streams))
	for k, s := range t.mu.streams {
		s.mu.Lock()
		frontiers[k] = s.mu.next
		s.mu.Unlock()
	}

	return frontiers
}

// Forget stops tracking the stream k and returns its final frontier. The bool
// is false when k was not tracked. Completions that arrive for k afterwards
// are ignored.
//
// A consumer that gives up a stream, such as a partition revoked in a rebalance,
// must do three things, in this order:
//
//  1. Flush the [BulkIndexer], so every record it was handed for the stream has
//     completed or failed. Until then the frontier is not final: records still
//     buffered have not completed, and the frontier stops below them.
//  2. Call Forget and take the frontier it returns. Doing this before the flush
//     takes the frontier too early, and the completions the flush produces are
//     then dropped.
//  3. Commit that frontier, and only that frontier, to the stream.
//
// A position taken from anywhere else, such as the highest record handed to the
// indexer, can pass records that were never written.
//
// What makes this order correct is that the flush comes first and the frontier
// is read after it. Once Flush has returned and no more records are being
// admitted for the stream, nothing further completes, so
// [FrontierTracker.Frontier] and Forget return the same value. Reading it with
// Frontier, committing it, and only then calling Forget is therefore just as
// correct, and is the better order if you want to keep tracking the stream when
// the commit fails. Forget returns the frontier so that reading it and ending
// the tracking are one step.
func (t *FrontierTracker[K]) Forget(k K) (int64, bool) {
	t.mu.Lock()
	s := t.mu.streams[k]
	delete(t.mu.streams, k)
	t.mu.Unlock()

	if s == nil {
		return 0, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.mu.next, true
}
