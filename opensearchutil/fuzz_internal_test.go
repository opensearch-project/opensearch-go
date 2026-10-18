// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/envvars"
)

// The fuzz targets in this file run their seed corpus as ordinary tests. To look
// for more, run one for a while, for example:
//
//	go test ./opensearchutil -run '^$' -fuzz '^FuzzSequenceTracker$' -fuzztime 30s
//
// A failing input is saved under testdata/fuzz and then runs with every `go test`.

// FuzzSequenceTracker drives a SequenceTracker with an arbitrary program of
// Begin, Complete and Forget calls over a few streams and checks it against a
// model of what the safe position must be after each one.
func FuzzSequenceTracker(f *testing.F) {
	f.Add([]byte{0, 0, 1, 5, 1, 2, 1, 0, 3, 0})
	f.Add([]byte{0, 0x40, 1, 0x41, 1, 0x40, 1, 0x42, 3, 0x40, 1, 0x40})
	f.Add([]byte{1, 3, 0, 3, 1, 3, 1, 3, 1, 4})

	type model struct {
		next int64
		done map[int64]bool
	}

	f.Fuzz(func(t *testing.T, program []byte) {
		var tracker SequenceTracker[uint8]
		models := map[uint8]*model{}

		for i := 0; i+1 < len(program); i += 2 {
			op, arg := program[i]%3, program[i+1]
			stream, seq := arg>>6, int64(arg&0x3f)
			m := models[stream]

			switch op {
			case 0:
				tracker.Begin(stream, seq)
				models[stream] = &model{next: seq, done: map[int64]bool{}}
			case 1:
				tracker.Complete(stream, seq)
				if m == nil || seq < m.next {
					break
				}
				m.done[seq] = true
				for m.done[m.next] {
					delete(m.done, m.next)
					m.next++
				}
			case 2:
				got, ok := tracker.Forget(stream)
				require.Equal(t, m != nil, ok)
				if m != nil {
					require.Equal(t, m.next, got, "Forget returns the position Safe reported")
				}
				delete(models, stream)
			}

			for s := range uint8(4) {
				got, ok := tracker.Safe(s)
				m := models[s]
				require.Equal(t, m != nil, ok, "stream %d tracked", s)
				if m != nil {
					require.Equal(t, m.next, got, "stream %d position", s)
				}
			}
		}
	})
}

// FuzzChunkBuffer drives a chunkBuffer, whose chunks are only 16 bytes so that
// almost every write crosses one, with an arbitrary program of writes, reads,
// truncations and resets, and checks what it reads against a plain byte slice.
func FuzzChunkBuffer(f *testing.F) {
	f.Add([]byte{0, 20, 1, 5, 0, 3, 1, 30})
	f.Add([]byte{0, 39, 2, 10, 0, 17, 1, 39, 3, 0, 0, 5, 1, 5})
	f.Add([]byte{0, 16, 0, 16, 1, 16, 2, 0, 0, 1})

	f.Fuzz(func(t *testing.T, program []byte) {
		buf := newChunkBuffer(newChunkPool(16))
		defer buf.Reset()

		var data []byte // everything written and not truncated
		read := 0       // how much of data has been read
		next := byte(0) // the next byte to write, so every byte of data is distinguishable

		for i := 0; i+1 < len(program); i += 2 {
			op, arg := program[i]%4, int(program[i+1])

			switch op {
			case 0: // Write.
				p := make([]byte, arg%40)
				for j := range p {
					p[j] = next
					next++
				}
				n, err := buf.Write(p)
				require.NoError(t, err)
				require.Equal(t, len(p), n)
				data = append(data, p...)
			case 1: // Read.
				p := make([]byte, arg%40)
				n, err := buf.Read(p)
				want := min(len(p), len(data)-read)
				require.Equal(t, want, n)
				require.Equal(t, data[read:read+want], p[:n])
				read += n
				if want == 0 && len(p) > 0 {
					require.ErrorIs(t, err, io.EOF)
				}
			case 2: // Truncate to some length not beyond what was written.
				n := arg % (len(data) + 1)
				buf.Truncate(n)
				data = data[:n]
				read = min(read, n)
			case 3: // Reset.
				buf.Reset()
				data, read = nil, 0
			}

			require.Equal(t, len(data)-read, buf.Len())
		}

		// What is left to read is exactly the unread tail.
		rest := make([]byte, len(data))
		n, _ := buf.Read(rest)
		require.Equal(t, data[read:], rest[:n])
	})
}

// FuzzItemMeta checks the action line the indexer writes for an item: it is one
// line of valid JSON that carries the item's fields, and, for a plain item, the
// indexer's estimate of its size is not below its real size.
func FuzzItemMeta(f *testing.F) {
	f.Add("index", "events", "evt-1", "tenant-7", true)
	f.Add("create", "", "", "", false)
	f.Add("delete", "a\"b", "c\nd", "e\\f", true)
	f.Add("update", "idx", "<&>", "é", true)

	f.Fuzz(func(t *testing.T, action, index, id, routing string, hasRouting bool) {
		item := BulkIndexerItem{Action: action, Index: index, DocumentID: id}
		if hasRouting {
			item.Routing = &routing
		}

		bi := &bulkIndexer{
			metaPoolMaxBytes: defaultMetaBufferPoolMaxBytes,
			metaPool:         sync.Pool{New: newMetaEncoder},
		}
		w := &worker{bi: bi, buf: newChunkBuffer(newChunkPool(defaultChunkSize))}
		defer w.buf.Reset()

		require.NoError(t, w.writeMeta(item))
		line := w.buf.String()

		require.Equal(t, referenceMeta(t, item), line, "the pooled encoder writes what a fresh encoder and a map did")
		require.Equal(t, 1, strings.Count(line, "\n"), "one line")
		require.Equal(t, byte('\n'), line[len(line)-1], "terminated by a newline")

		var meta map[string]map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &meta), "valid JSON: %q", line)

		if !utf8.ValidString(action + index + id + routing) {
			return // The encoder substitutes U+FFFD for invalid UTF-8.
		}

		require.Len(t, meta, 1)
		fields, ok := meta[action]
		require.True(t, ok, "keyed by the action: %q", line)

		check := func(key, want string, set bool) {
			t.Helper()

			got, present := fields[key]
			require.Equal(t, set, present, "%s present", key)
			if set {
				require.Equal(t, want, got, key)
			}
		}
		check("_index", index, index != "")
		check("_id", id, id != "")
		check("routing", routing, hasRouting)

		// The estimate is for memory accounting. Escaping makes the real line longer
		// than the raw lengths the estimate adds up, so compare only plain strings.
		if plain(action, index, id, routing) {
			require.GreaterOrEqual(t, estimateItemBytes(item), int64(len(line)), "the estimate must not undercount: %q", line)
		}
	})
}

// plain reports whether none of the strings needs escaping in JSON.
func plain(ss ...string) bool {
	for _, s := range ss {
		for i := range len(s) {
			if c := s[i]; c < 0x20 || c >= 0x7f || c == '"' || c == '\\' {
				return false
			}
		}
	}

	return true
}

// FuzzSizer feeds a sizer an arbitrary run of flushes and checks that its target
// never leaves the bounds it was given, is never NaN, and does not move at all
// when it is fixed, whichever kind of sizer it is.
func FuzzSizer(f *testing.F) {
	f.Add(uint8(0), []byte{1, 200, 10, 0, 2, 100, 20, 0, 3, 50, 40, 1})
	f.Add(uint8(1), []byte{255, 255, 255, 255, 0, 0, 0, 0})
	f.Add(uint8(2), []byte{9, 9, 9, 8, 1, 1, 1, 2, 5, 5, 5, 4})
	f.Add(uint8(4), []byte{7, 7, 7, 16, 7, 7, 7, 0})

	f.Fuzz(func(t *testing.T, kind uint8, program []byte) {
		cfg := BulkSizing{Fixed: kind&4 != 0, AIMD: kind&1 != 0}
		resolved := cfg.resolve(testCeiling)
		s := newSizer(resolved)
		start := s.bytes()

		for i := 0; i+3 < len(program); i += 4 {
			flags := program[i+3]
			s.observe(flushSample{
				bytes:      int64(program[i]) << 14,
				latency:    time.Duration(program[i+1]) * time.Millisecond,
				distance:   time.Duration(program[i+2]) * time.Millisecond,
				served:     flags&8 == 0,
				rejected:   flags&1 != 0,
				failed:     flags&2 != 0,
				overloaded: flags&4 != 0,
			})

			require.False(t, math.IsNaN(s.target) || math.IsInf(s.target, 0), "target %v", s.target)
			require.GreaterOrEqual(t, s.target, resolved.min)
			require.LessOrEqual(t, s.target, resolved.max)
			require.False(t, math.IsNaN(s.baseRate) || math.IsInf(s.baseRate, 0), "baseRate %v", s.baseRate)
			if resolved.fixed {
				require.Equal(t, start, s.bytes(), "a fixed sizer ignores every flush")
			}
		}
	})
}

// FuzzBulkSizingEnv sets the sizing environment variables to arbitrary text and
// checks what resolve makes of them: the gains stay finite and not negative, and
// the sizes stay ordered.
func FuzzBulkSizingEnv(f *testing.F) {
	f.Add("fixed", "on", "1", "0.1", "0.25")
	f.Add("adaptive", "off", "0", "0", "0")
	f.Add("", "", "", "", "")
	f.Add("FIXED ", "maybe", "-1", "NaN", "Inf")
	f.Add("x", "1", "1e400", "0x10", "  7  ")

	f.Fuzz(func(t *testing.T, mode, aimd, kp, ki, kd string) {
		t.Setenv(envvars.BulkMode, mode)
		t.Setenv(envvars.BulkAIMD, aimd)
		t.Setenv(envvars.BulkPIDKp, kp)
		t.Setenv(envvars.BulkPIDKi, ki)
		t.Setenv(envvars.BulkPIDKd, kd)

		r := BulkSizing{}.resolve(testCeiling)
		for name, gain := range map[string]float64{"kp": r.kp, "ki": r.ki, "kd": r.kd} {
			require.False(t, math.IsNaN(gain) || math.IsInf(gain, 0) || gain < 0, "%s = %v", name, gain)
		}
		require.LessOrEqual(t, r.min, r.initial)
		require.LessOrEqual(t, r.initial, r.max)
	})
}

// FuzzClassifyFailure checks that every status and error classifies as one of the
// four kinds, and that the classification of a status does not depend on the error.
func FuzzClassifyFailure(f *testing.F) {
	for _, status := range []int{0, 200, 400, 409, 429, 500, 502, 503, 504, 599, 600, -1} {
		f.Add(status, true)
	}

	f.Fuzz(func(t *testing.T, status int, hasErr bool) {
		var err error
		if hasErr {
			err = errors.New("failed")
		}

		kind := classifyBatchFailure(status, err)
		require.GreaterOrEqual(t, int(kind), 0)
		require.Less(t, int(kind), numFailureKinds)

		if status == http.StatusTooManyRequests {
			require.Equal(t, FailureRejected, kind)
		}
		if status == http.StatusServiceUnavailable {
			require.Equal(t, FailureUnavailable, kind)
		}
		require.Equal(t, kind, classifyBatchFailure(status, errors.New("another")), "the status decides, not the error")
	})
}

// FuzzDefaultRoutingNumShards checks the derivation of the server's
// routing_num_shards for any number of shards: zero for a count that is not
// positive or too large for the answer to fit, otherwise a multiple of the count,
// which is what a shard map needs.
func FuzzDefaultRoutingNumShards(f *testing.F) {
	for _, n := range []int{-1, 0, 1, 2, 5, 32, 1024, 1025, 1 << 20, math.MaxInt32, math.MaxInt64} {
		f.Add(n)
	}

	f.Fuzz(func(t *testing.T, n int) {
		got := DefaultRoutingNumShards(n)
		if n <= 0 {
			require.Zero(t, got)

			return
		}

		if got == 0 {
			// Zero also means the answer would not fit in an int, which can only be
			// so for a count above half of the largest int.
			require.Greater(t, n, math.MaxInt>>1, "no routing_num_shards for %d shards", n)

			return
		}

		require.Positive(t, got, "routing_num_shards for %d shards overflowed", n)
		require.Zero(t, got%n, "routing_num_shards %d is not a multiple of %d", got, n)
		require.True(t, validShardMap(DefaultShardMap(n)))
	})
}
