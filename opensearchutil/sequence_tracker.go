// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchutil

import "sync"

// SequenceTracker reports, for each of a set of ordered streams, how far the
// stream can safely be advanced when its records finish in any order.
//
// A [BulkIndexer] completes records out of order: batches for different shards
// flush at different times. A stream cursor, such as a Kafka consumer group
// offset, may only move forward past a record once every record before it is
// done. The tracker holds that line. It remembers which sequence numbers have
// completed, and reports the lowest one that has not: every sequence below the
// reported value is done, and the reported value itself is not. For a stream
// whose positions are offsets that is exactly the offset to commit.
//
// This differs from marking each record as it completes in a client library
// that keeps a per-partition maximum. Such a mark moves to the highest record
// completed, so completing out of order commits past a gap and loses the
// records in it. The tracker only ever reports the end of a contiguous prefix.
//
// There is deliberately no failed state. A failed record must not advance the
// line, and a record that is retried keeps its original sequence number, so not
// calling [SequenceTracker.Complete] for it is the correct handling: the line
// stalls below it until the retry lands. The cost of a stall is memory: every
// later completion of that stream is held until the missing one arrives.
//
// The streams are identified by a key of any comparable type, such as a topic
// and partition. The zero value is ready to use, and a SequenceTracker is safe
// for concurrent use; it must not be copied after first use.
type SequenceTracker[K comparable] struct {
	mu      sync.RWMutex
	streams map[K]*sequenceStream
}

// sequenceStream is the state of one stream: next is the lowest sequence not
// yet complete, and done holds the completed sequences above it.
type sequenceStream struct {
	mu   sync.Mutex
	next int64
	done map[int64]struct{}
}

// Begin starts tracking the stream k, whose first sequence to track is next:
// every sequence below next counts as already complete, so the position
// reported for k starts at next. For a Kafka partition, next is the offset the
// consumer starts reading from.
//
// Beginning a stream that is already tracked discards what the tracker knew of
// it and starts over at next, as happens when a partition is assigned again.
func (t *SequenceTracker[K]) Begin(k K, next int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.streams == nil {
		t.streams = make(map[K]*sequenceStream)
	}
	t.streams[k] = &sequenceStream{next: next}
}

// Complete records that the record with sequence seq on stream k is done. It
// may be called in any order and any number of times for a sequence. A
// sequence below the position already reported, and any sequence on a stream
// that is not tracked, is ignored.
func (t *SequenceTracker[K]) Complete(k K, seq int64) {
	t.mu.RLock()
	s := t.streams[k]
	t.mu.RUnlock()

	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch {
	case seq < s.next:
		// Already counted.
	case seq > s.next:
		if s.done == nil {
			s.done = make(map[int64]struct{})
		}
		s.done[seq] = struct{}{}
	default:
		// The lowest missing sequence arrived: move the line past it and past
		// every contiguous completion that was waiting on it.
		s.next++
		for {
			if _, ok := s.done[s.next]; !ok {
				break
			}
			delete(s.done, s.next)
			s.next++
		}
	}
}

// Safe reports the position stream k can safely be advanced to: every sequence
// below it is complete, and it is never above the lowest sequence that is not.
// The bool is false when k is not tracked.
func (t *SequenceTracker[K]) Safe(k K) (int64, bool) {
	t.mu.RLock()
	s := t.streams[k]
	t.mu.RUnlock()

	if s == nil {
		return 0, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.next, true
}

// SafeAll reports the safe position of every tracked stream, as [SequenceTracker.Safe]
// does for one. The map is a snapshot the caller owns.
func (t *SequenceTracker[K]) SafeAll() map[K]int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	safe := make(map[K]int64, len(t.streams))
	for k, s := range t.streams {
		s.mu.Lock()
		safe[k] = s.next
		s.mu.Unlock()
	}

	return safe
}

// Forget stops tracking the stream k and returns its final safe position. The
// bool is false when k was not tracked. Completions that arrive for k afterwards
// are ignored.
//
// A consumer that gives up a stream, such as a partition revoked in a rebalance,
// must do three things, in this order:
//
//  1. Flush the [BulkIndexer], so every record it was handed for the stream has
//     completed or failed. Until then the position is not final: records still
//     buffered have not completed, and the position stops below them.
//  2. Call Forget and take the position it returns. Doing this before the flush
//     takes the position too early, and the completions the flush produces are
//     then dropped.
//  3. Commit that position, and only that position, to the stream.
//
// A position taken from anywhere else, such as the highest record handed to the
// indexer, can pass records that were never written.
//
// What makes the sequence safe is that the flush comes first and that the
// position is read after it. Once Flush has returned and no more records are
// being admitted for the stream, nothing further completes, so [SequenceTracker.Safe]
// and Forget return the same position. Reading it with Safe, committing it, and
// only then calling Forget is therefore equally safe, and is the better order if
// you want to keep tracking the stream when the commit fails. Forget returns the
// position so that reading it and ending the tracking are one step.
func (t *SequenceTracker[K]) Forget(k K) (int64, bool) {
	t.mu.Lock()
	s := t.streams[k]
	delete(t.streams, k)
	t.mu.Unlock()

	if s == nil {
		return 0, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.next, true
}
