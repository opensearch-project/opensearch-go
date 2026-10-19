// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testChunkSize is small enough that every operation below crosses chunk
// boundaries.
const testChunkSize = 8

func newTestChunkBuffer() *chunkBuffer {
	return newChunkBuffer(newChunkPool(testChunkSize))
}

// TestChunkBufferMatchesBytesBuffer applies random writes, truncations and
// resets to a chunkBuffer and a bytes.Buffer and requires them to agree.
func TestChunkBufferMatchesBytesBuffer(t *testing.T) {
	t.Parallel()

	for seed := int64(1); seed <= 20; seed++ {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			t.Parallel()

			rng := rand.New(rand.NewSource(seed))
			got, want := newTestChunkBuffer(), new(bytes.Buffer)

			for step := range 300 {
				switch op := rng.Intn(10); op {
				case 0:
					p := randomBytes(rng, rng.Intn(40))
					_, _ = got.Write(p)
					_, _ = want.Write(p)
				case 1:
					s := string(randomBytes(rng, rng.Intn(40)))
					_, _ = got.WriteString(s)
					_, _ = want.WriteString(s)
				case 2:
					c := alphabet[rng.Intn(len(alphabet))]
					got.appendByte(c)
					require.NoError(t, want.WriteByte(c))
				case 3:
					// Multi-byte characters straddle chunk boundaries too.
					s := []string{"a", "é", "€", "🌍", "\n"}[rng.Intn(5)]
					_, _ = got.WriteString(s)
					_, _ = want.WriteString(s)
				case 4:
					src := string(randomBytes(rng, rng.Intn(100)))
					gotN, gotErr := got.ReadFrom(strings.NewReader(src))
					wantN, wantErr := want.ReadFrom(strings.NewReader(src))
					require.Equal(t, wantN, gotN)
					require.Equal(t, wantErr, gotErr)
				case 5:
					n := rng.Intn(want.Len() + 1)
					got.Truncate(n)
					want.Truncate(n)
				case 6:
					if rng.Intn(10) == 0 {
						got.Reset()
						want.Reset()
					}
				case 7:
					// A one-byte write straddles a chunk boundary when the
					// buffer sits at one.
					got.Truncate(want.Len())
				case 8, 9:
					// A slice lent to the buffer, as a document sent without a copy is.
					p := randomBytes(rng, rng.Intn(40))
					got.appendRef(p)
					_, _ = want.Write(p)
				}

				require.Equal(t, want.Len(), got.Len(), "step %d", step)
				require.Equal(t, want.String(), got.String(), "step %d", step)
			}
		})
	}
}

const alphabet = "abcdefghijklmnopqrstuvwxyz"

func randomBytes(rng *rand.Rand, n int) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = alphabet[rng.Intn(len(alphabet))]
	}

	return p
}

func TestChunkBufferReads(t *testing.T) {
	t.Parallel()

	content := strings.Repeat("0123456789", 7) // 70 bytes: eight chunks, the last partial.

	tests := []struct {
		name string
		read func(t *testing.T, b *chunkBuffer) string
	}{
		{
			name: "ReadAll",
			read: func(t *testing.T, b *chunkBuffer) string {
				t.Helper()
				got, err := io.ReadAll(b)
				require.NoError(t, err)
				return string(got)
			},
		},
		{
			name: "small reads",
			read: func(t *testing.T, b *chunkBuffer) string {
				t.Helper()
				var out strings.Builder
				p := make([]byte, 3)
				for {
					n, err := b.Read(p)
					out.Write(p[:n])
					if errors.Is(err, io.EOF) {
						return out.String()
					}
					require.NoError(t, err)
				}
			},
		},
		{
			name: "WriteTo",
			read: func(t *testing.T, b *chunkBuffer) string {
				t.Helper()
				var out bytes.Buffer
				n, err := b.WriteTo(&out)
				require.NoError(t, err)
				require.EqualValues(t, len(content), n)
				return out.String()
			},
		},
		{
			name: "io.Copy",
			read: func(t *testing.T, b *chunkBuffer) string {
				t.Helper()
				var out bytes.Buffer
				_, err := io.Copy(&out, b)
				require.NoError(t, err)
				return out.String()
			},
		},
		{
			name: "NewReader",
			read: func(t *testing.T, b *chunkBuffer) string {
				t.Helper()
				got, err := io.ReadAll(b.NewReader())
				require.NoError(t, err)
				return string(got)
			},
		},
	}

	fills := []struct {
		name string
		fill func(b *chunkBuffer)
	}{
		{"written", func(b *chunkBuffer) { _, _ = b.WriteString(content) }},
		{"written and lent", func(b *chunkBuffer) {
			_, _ = b.WriteString(content[:13])
			b.appendRef([]byte(content[13:42]))
			_, _ = b.WriteString(content[42:48])
			b.appendRef([]byte(content[48:]))
		}},
	}

	for _, fill := range fills {
		for _, tt := range tests {
			t.Run(fill.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				b := newTestChunkBuffer()
				fill.fill(b)
				require.Equal(t, len(content), b.Len())

				require.Equal(t, content, tt.read(t, b))
			})
		}
	}

	t.Run("a reader replays independently and leaves the buffer unread", func(t *testing.T) {
		t.Parallel()

		b := newTestChunkBuffer()
		_, _ = b.WriteString(content)

		for range 2 {
			got, err := io.ReadAll(b.NewReader())
			require.NoError(t, err)
			require.Equal(t, content, string(got))
		}
		require.Equal(t, len(content), b.Len())
		require.Equal(t, content, b.String())
	})

	t.Run("a replay is of the whole content after the buffer has been read", func(t *testing.T) {
		t.Parallel()

		b := newTestChunkBuffer()
		_, _ = b.WriteString(content)

		// A request sent again has had its body read to the end by the first attempt.
		got, err := io.ReadAll(b)
		require.NoError(t, err)
		require.Equal(t, content, string(got))
		require.Zero(t, b.Len())

		for range 2 {
			again, replayErr := io.ReadAll(b.NewReader())
			require.NoError(t, replayErr)
			require.Equal(t, content, string(again))
		}
	})

	t.Run("a chunk-aligned buffer reads to its end", func(t *testing.T) {
		t.Parallel()

		b := newTestChunkBuffer()
		aligned := strings.Repeat("x", testChunkSize*3)
		_, _ = b.WriteString(aligned)

		got, err := io.ReadAll(b)
		require.NoError(t, err)
		require.Equal(t, aligned, string(got))
		require.Zero(t, b.Len())
	})

	t.Run("an empty buffer is at EOF", func(t *testing.T) {
		t.Parallel()

		n, err := newTestChunkBuffer().Read(make([]byte, 4))
		require.Zero(t, n)
		require.ErrorIs(t, err, io.EOF)
	})
}

func TestChunkBufferTruncateRollsBack(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		content  string
		truncate int
	}{
		{name: "inside a chunk", content: strings.Repeat("a", 20), truncate: 18},
		{name: "on a chunk boundary", content: strings.Repeat("a", 20), truncate: testChunkSize * 2},
		{name: "just past a boundary", content: strings.Repeat("a", 20), truncate: testChunkSize + 1},
		{name: "to empty", content: strings.Repeat("a", 20), truncate: 0},
		{name: "to the same length", content: strings.Repeat("a", 20), truncate: 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b := newTestChunkBuffer()
			_, _ = b.WriteString(tt.content)
			b.Truncate(tt.truncate)

			require.Equal(t, tt.truncate, b.Len())
			require.Equal(t, tt.content[:tt.truncate], b.String())

			// Writing after a rollback continues from the cut.
			_, _ = b.WriteString("Z")
			require.Equal(t, tt.content[:tt.truncate]+"Z", b.String())
		})
	}

	t.Run("out of range panics", func(t *testing.T) {
		t.Parallel()

		b := newTestChunkBuffer()
		_, _ = b.WriteString("abc")
		require.Panics(t, func() { b.Truncate(4) })
		require.Panics(t, func() { b.Truncate(-1) })
	})
}

// TestChunkBufferLendsSlices: a slice lent to the buffer is read where it is, in
// order among the bytes written around it, is never written to, and is let go of
// without being handed to the pool.
func TestChunkBufferLendsSlices(t *testing.T) {
	t.Parallel()

	pool := newChunkPool(testChunkSize)
	b := newChunkBuffer(pool)

	lent := []byte("0123456789ABCDEF0123") // Longer than a chunk.
	snapshot := bytes.Clone(lent)

	_, _ = b.WriteString("head|")
	b.appendRef(lent)
	_, _ = b.WriteString("|tail")
	b.appendRef(nil)

	require.Equal(t, "head|"+string(snapshot)+"|tail", b.String())
	require.Equal(t, len("head|")+len(lent)+len("|tail"), b.Len())

	// Writing after a lent slice goes to a new chunk and not into the slice.
	require.Equal(t, snapshot, lent)
	require.Equal(t, uint64(2), pool.served.Load(), "two owned segments: the head and the tail")

	all, err := io.ReadAll(b.NewReader())
	require.NoError(t, err)
	require.Equal(t, "head|"+string(snapshot)+"|tail", string(all))

	// Rolling back through the lent slice releases it, and only the chunks go back.
	b.Truncate(len("head|") + 5)
	require.Equal(t, "head|01234", b.String())
	require.Equal(t, snapshot, lent, "truncation does not write to a lent slice")

	b.Reset()
	require.Zero(t, b.Len())
	require.Equal(t, snapshot, lent)
}

// limitedWriter accepts at most limit bytes in total. Past that it writes what
// room is left and fails with err, or, when err is nil, reports the short write
// without an error as a misbehaving io.Writer may.
type limitedWriter struct {
	buf   bytes.Buffer
	limit int
	err   error
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	room := max(w.limit-w.buf.Len(), 0)
	if len(p) <= room {
		return w.buf.Write(p)
	}

	n, _ := w.buf.Write(p[:room])

	return n, w.err
}

// TestChunkBufferWriteToFromPosition: WriteTo writes the bytes not read yet, and
// leaves the buffer read to its end, so what a partial Read consumed is not
// written twice.
func TestChunkBufferWriteToFromPosition(t *testing.T) {
	t.Parallel()

	content := strings.Repeat("0123456789", 3) // Four chunks, the last partial.

	tests := []struct {
		name string
		// consume is how many bytes are read before WriteTo.
		consume int
	}{
		{name: "nothing read", consume: 0},
		{name: "inside the first chunk", consume: 3},
		{name: "on a chunk boundary", consume: testChunkSize * 2},
		{name: "inside a later chunk", consume: testChunkSize*2 + 5},
		{name: "all but one byte", consume: len(content) - 1},
		{name: "everything", consume: len(content)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b := newTestChunkBuffer()
			b.appendRef([]byte(content[:5]))
			_, _ = b.WriteString(content[5:])
			_, err := io.ReadFull(b, make([]byte, tt.consume))
			require.NoError(t, err)

			var out bytes.Buffer
			n, err := b.WriteTo(&out)
			require.NoError(t, err)
			require.EqualValues(t, len(content)-tt.consume, n)
			require.Equal(t, content[tt.consume:], out.String())
			require.Zero(t, b.Len(), "written bytes are consumed")

			// A second write has nothing left to send.
			n, err = b.WriteTo(&out)
			require.NoError(t, err)
			require.Zero(t, n)
			require.Equal(t, content[tt.consume:], out.String())
		})
	}
}

// TestChunkBufferWriteToWriterFailure: when the writer fails or accepts less than
// it was given, WriteTo reports the bytes the writer took and the cause, and the
// buffer keeps the rest unread, so a later write continues where this one stopped.
func TestChunkBufferWriteToWriterFailure(t *testing.T) {
	t.Parallel()

	content := strings.Repeat("0123456789", 3)
	errClosed := errors.New("connection closed")

	tests := []struct {
		name    string
		limit   int
		err     error
		wantErr error
	}{
		{name: "error mid chunk", limit: 11, err: errClosed, wantErr: errClosed},
		{name: "error on a chunk boundary", limit: testChunkSize * 2, err: errClosed, wantErr: errClosed},
		{name: "error before any byte", limit: 0, err: errClosed, wantErr: errClosed},
		{name: "short write without an error", limit: 11, wantErr: io.ErrShortWrite},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b := newTestChunkBuffer()
			_, _ = b.WriteString(content)

			w := &limitedWriter{limit: tt.limit, err: tt.err}
			n, err := b.WriteTo(w)
			require.ErrorIs(t, err, tt.wantErr)
			require.EqualValues(t, tt.limit, n)
			require.Equal(t, content[:tt.limit], w.buf.String())
			require.Equal(t, len(content)-tt.limit, b.Len())

			var rest bytes.Buffer
			_, err = b.WriteTo(&rest)
			require.NoError(t, err)
			require.Equal(t, content[tt.limit:], rest.String(), "the unwritten bytes are still there")
		})
	}
}

// TestChunkBufferTruncateKeepsReadPosition: truncating a buffer that has been read
// partway leaves the unread bytes that are still within the content, and puts the
// position at the new end when the content no longer reaches it.
func TestChunkBufferTruncateKeepsReadPosition(t *testing.T) {
	t.Parallel()

	content := strings.Repeat("abcdefghij", 3) // Four chunks.

	tests := []struct {
		name     string
		consume  int
		truncate int
		want     string
	}{
		{name: "cut after the position, same chunk", consume: 10, truncate: 12, want: content[10:12]},
		{name: "cut after the position, later chunk", consume: 3, truncate: 20, want: content[3:20]},
		{name: "position on a chunk boundary", consume: testChunkSize, truncate: 25, want: content[testChunkSize:25]},
		{name: "cut at the position", consume: 10, truncate: 10, want: ""},
		{name: "cut before the position", consume: 20, truncate: 9, want: ""},
		{name: "cut to empty", consume: 5, truncate: 0, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b := newTestChunkBuffer()
			_, _ = b.WriteString(content)
			_, err := io.ReadFull(b, make([]byte, tt.consume))
			require.NoError(t, err)

			b.Truncate(tt.truncate)
			require.Equal(t, len(tt.want), b.Len())
			require.Equal(t, tt.want, b.String())

			got, err := io.ReadAll(b)
			require.NoError(t, err)
			require.Equal(t, tt.want, string(got))

			// A write after the cut is read next, from the new end.
			_, _ = b.WriteString("Z")
			got, err = io.ReadAll(b)
			require.NoError(t, err)
			require.Equal(t, "Z", string(got))
		})
	}
}

// TestLeasedReaderWriteTo: a replay of a leased body is written out whole, from the
// start, whatever the first attempt read; it finishes only when the write
// succeeds, so a failed write leaves the buffer held until the reader is closed.
func TestLeasedReaderWriteTo(t *testing.T) {
	t.Parallel()

	const content = "0123456789abcdefghij0123456789"

	newLeased := func(t *testing.T) (*chunkBuffer, *leasedBody) {
		t.Helper()

		buf := newTestChunkBuffer()
		_, _ = buf.WriteString(content)
		body := buf.lease()
		_, err := io.ReadAll(body)
		require.NoError(t, err)
		require.True(t, buf.idle())

		return buf, body
	}

	t.Run("writes the whole content and finishes", func(t *testing.T) {
		t.Parallel()

		buf, body := newLeased(t)
		replay := body.NewReader()
		require.False(t, buf.idle())

		wt, ok := replay.(io.WriterTo)
		require.True(t, ok)

		var out bytes.Buffer
		n, err := wt.WriteTo(&out)
		require.NoError(t, err)
		require.EqualValues(t, len(content), n)
		require.Equal(t, content, out.String())
		require.True(t, buf.idle(), "a completed write ends the replay")

		// Closing afterwards does not release the buffer a second time.
		closer, ok := replay.(io.Closer)
		require.True(t, ok)
		require.NoError(t, closer.Close())
		require.True(t, buf.idle())
		require.Zero(t, buf.active.Load())
	})

	t.Run("a failed write keeps the buffer held until close", func(t *testing.T) {
		t.Parallel()

		buf, body := newLeased(t)
		replay := body.NewReader()

		wt, ok := replay.(io.WriterTo)
		require.True(t, ok)

		errClosed := errors.New("connection closed")
		n, err := wt.WriteTo(&limitedWriter{limit: 12, err: errClosed})
		require.ErrorIs(t, err, errClosed)
		require.EqualValues(t, 12, n)
		require.False(t, buf.idle(), "the segments may still be read")

		ran := false
		buf.whenIdle(func() { ran = true })
		require.False(t, ran)

		closer, ok := replay.(io.Closer)
		require.True(t, ok)
		require.NoError(t, closer.Close())
		require.True(t, buf.idle())
		require.True(t, ran, "close releases what waited for the buffer")
	})
}

// TestChunkPoolReusesChunks fills and resets a buffer repeatedly and requires
// that chunks are taken from the pool far more often than they are made.
func TestChunkPoolReusesChunks(t *testing.T) {
	t.Parallel()

	const (
		cycles         = 50
		chunksPerCycle = 10
	)

	pool := newChunkPool(testChunkSize)
	b := newChunkBuffer(pool)
	payload := strings.Repeat("x", testChunkSize*chunksPerCycle)

	for range cycles {
		_, _ = b.WriteString(payload)
		b.Reset()
	}

	served, allocated := pool.served.Load(), pool.allocated.Load()
	require.EqualValues(t, cycles*chunksPerCycle, served)
	// Without reuse every chunk handed out is a new one. sync.Pool may drop a
	// fraction of what is returned to it (the race detector drops one in four on
	// purpose), so ask for a clear majority rather than every chunk.
	require.Less(t, allocated, served/2, "chunks must come from the pool, not from the allocator")
}

// receive waits for a signal, and fails the test rather than hang when a broken
// implementation never sends it.
func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()

	select {
	case v := <-ch:
		return v
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for the budget to react")
		panic("unreachable")
	}
}

// testBudgetLimit is the limit of the budgets the tests below build.
const testBudgetLimit = 100

// testBudgetKey is the shard the tests below take bytes for when only one is needed.
var testBudgetKey = shardKey{index: "idx", shard: 0}

// budgetWith builds a budget whose blocking is observable: blocked receives once
// each time an acquire begins to wait.
func budgetWith() (*byteBudget, <-chan struct{}) {
	b := newByteBudget(testBudgetLimit)
	blocked := make(chan struct{}, 8)
	b.onBlock = func() { blocked <- struct{}{} }

	return b, blocked
}

func TestByteBudgetAcquireAndRelease(t *testing.T) {
	t.Parallel()

	b, _ := budgetWith()

	n, err := b.acquire(t.Context(), nil, testBudgetKey, 60)
	require.NoError(t, err)
	require.EqualValues(t, 60, n)
	require.EqualValues(t, 60, b.inUse())

	n, err = b.acquire(t.Context(), nil, testBudgetKey, 40)
	require.NoError(t, err)
	require.EqualValues(t, 40, n)
	require.EqualValues(t, 100, b.inUse())

	b.release(testBudgetKey, 100)
	require.Zero(t, b.inUse())

	blocked, _ := b.blockedStats()
	require.Zero(t, blocked, "nothing waited")
}

func TestByteBudgetBlocksUntilReleased(t *testing.T) {
	t.Parallel()

	b, blocked := budgetWith()
	_, err := b.acquire(t.Context(), nil, testBudgetKey, 100)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, acquireErr := b.acquire(t.Context(), nil, testBudgetKey, 30)
		done <- acquireErr
	}()

	receive(t, blocked)
	require.EqualValues(t, 100, b.inUse(), "the waiter must not hold bytes yet")

	b.release(testBudgetKey, 30)
	require.NoError(t, receive(t, done))
	require.EqualValues(t, 100, b.inUse())

	count, _ := b.blockedStats()
	require.EqualValues(t, 1, count)
}

func TestByteBudgetServesWaitersInOrder(t *testing.T) {
	t.Parallel()

	b, blocked := budgetWith()
	_, err := b.acquire(t.Context(), nil, testBudgetKey, 100)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for _, n := range []int64{60, 10} { // The large one arrives first.
		wg.Go(func() {
			_, acquireErr := b.acquire(t.Context(), nil, testBudgetKey, n)
			require.NoError(t, acquireErr)
		})
		receive(t, blocked)
	}

	// 50 bytes free: the second waiter fits but must not pass the first.
	b.release(testBudgetKey, 50)
	require.EqualValues(t, 50, b.inUse(), "a small waiter must not overtake a large one")

	// 90 free: the first waiter takes 60, then the second takes 10.
	b.release(testBudgetKey, 40)
	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()
	receive(t, finished)
	require.EqualValues(t, 80, b.inUse())
}

func TestByteBudgetWaiterGivesUp(t *testing.T) {
	t.Parallel()

	stopped := make(chan struct{})

	tests := []struct {
		name    string
		stop    <-chan struct{}
		trigger func(cancel context.CancelFunc)
		wantErr error
	}{
		{
			name:    "context cancelled",
			trigger: func(cancel context.CancelFunc) { cancel() },
			wantErr: context.Canceled,
		},
		{
			name:    "indexer stopped",
			stop:    stopped,
			trigger: func(context.CancelFunc) { close(stopped) },
			wantErr: errBudgetStopped,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b, blocked := budgetWith()
			_, err := b.acquire(t.Context(), nil, testBudgetKey, 100)
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			done := make(chan error, 1)
			go func() {
				_, acquireErr := b.acquire(ctx, tt.stop, testBudgetKey, 50)
				done <- acquireErr
			}()

			receive(t, blocked)
			tt.trigger(cancel)

			require.ErrorIs(t, receive(t, done), tt.wantErr)
			require.EqualValues(t, 100, b.inUse(), "a waiter that gave up must take nothing")

			// The abandoned waiter left the queue, so the next caller is served.
			b.release(testBudgetKey, 100)
			n, err := b.acquire(t.Context(), nil, testBudgetKey, 100)
			require.NoError(t, err)
			require.EqualValues(t, 100, n)
		})
	}
}

func TestByteBudgetCancelledHeadUnblocksTheNext(t *testing.T) {
	t.Parallel()

	b, blocked := budgetWith()
	_, err := b.acquire(t.Context(), nil, testBudgetKey, 80)
	require.NoError(t, err)

	headCtx, cancelHead := context.WithCancel(t.Context())
	headDone := make(chan error, 1)
	go func() {
		_, acquireErr := b.acquire(headCtx, nil, testBudgetKey, 90) // Cannot fit until 80 is released.
		headDone <- acquireErr
	}()
	receive(t, blocked)

	tailDone := make(chan error, 1)
	go func() {
		_, acquireErr := b.acquire(t.Context(), nil, testBudgetKey, 10) // Fits, but waits behind the head.
		tailDone <- acquireErr
	}()
	receive(t, blocked)

	cancelHead()
	require.ErrorIs(t, receive(t, headDone), context.Canceled)
	require.NoError(t, receive(t, tailDone))
	require.EqualValues(t, 90, b.inUse())
}

func TestByteBudgetLetsAnOversizeItemThrough(t *testing.T) {
	t.Parallel()

	b, blocked := budgetWith()

	n, err := b.acquire(t.Context(), nil, testBudgetKey, 10_000)
	require.NoError(t, err)
	require.EqualValues(t, 100, n, "an item beyond the whole budget takes the whole budget")

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = b.acquire(t.Context(), nil, testBudgetKey, 1)
	}()
	<-blocked // It runs alone: everything else waits.

	b.release(testBudgetKey, n)
	receive(t, done)
}

func TestByteBudgetReportsBlockedTime(t *testing.T) {
	t.Parallel()

	const tick = 7 * time.Millisecond

	b, blocked := budgetWith()

	var mu sync.Mutex
	clock := time.Unix(0, 0)
	b.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		clock = clock.Add(tick)
		return clock
	}

	_, err := b.acquire(t.Context(), nil, testBudgetKey, 100)
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = b.acquire(t.Context(), nil, testBudgetKey, 10)
	}()
	receive(t, blocked)
	b.release(testBudgetKey, 100)
	receive(t, done)

	count, total := b.blockedStats()
	require.EqualValues(t, 1, count)
	require.Equal(t, tick, total, "one start reading and one end reading, a tick apart")
}

func TestNilByteBudgetImposesNoLimit(t *testing.T) {
	t.Parallel()

	var b *byteBudget

	n, err := b.acquire(t.Context(), nil, testBudgetKey, 1<<40)
	require.NoError(t, err)
	require.Zero(t, n)
	b.release(testBudgetKey, 5)
	require.Zero(t, b.inUse())
	count, total := b.blockedStats()
	require.Zero(t, count)
	require.Zero(t, total)
}

// TestChunkBufferLeaseCountsActiveReaders: a body handed to a transport is in use
// until it has been read to its end or closed, whichever comes first and however
// many replays there are, and whatever waits for the buffer to fall idle runs when
// the last reader finishes.
func TestChunkBufferLeaseCountsActiveReaders(t *testing.T) {
	t.Parallel()

	const content = "0123456789abcdefghij"

	tests := []struct {
		name string
		// finish ends the body's use in one way.
		finish func(t *testing.T, body *leasedBody)
	}{
		{"read to the end", func(t *testing.T, body *leasedBody) {
			t.Helper()
			_, err := io.ReadAll(body)
			require.NoError(t, err)
		}},
		{"written out", func(t *testing.T, body *leasedBody) {
			t.Helper()
			_, err := body.WriteTo(io.Discard)
			require.NoError(t, err)
		}},
		{"closed unread", func(t *testing.T, body *leasedBody) {
			t.Helper()
			require.NoError(t, body.Close())
		}},
		{"closed twice", func(t *testing.T, body *leasedBody) {
			t.Helper()
			require.NoError(t, body.Close())
			require.NoError(t, body.Close())
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			buf := newTestChunkBuffer()
			_, _ = buf.WriteString(content)
			require.True(t, buf.idle())

			body := buf.lease()
			require.False(t, buf.idle(), "leased")

			ran := 0
			buf.whenIdle(func() { ran++ })
			require.Zero(t, ran, "waits for the reader")

			tt.finish(t, body)
			require.True(t, buf.idle())
			require.Equal(t, 1, ran, "runs when the last reader finishes, once")

			// Finishing again does not count the reader a second time.
			require.NoError(t, body.Close())
			require.True(t, buf.idle())
		})
	}

	t.Run("a replay is a reader of its own", func(t *testing.T) {
		t.Parallel()

		buf := newTestChunkBuffer()
		_, _ = buf.WriteString(content)

		body := buf.lease()
		_, err := io.ReadAll(body)
		require.NoError(t, err)
		require.True(t, buf.idle())

		again := body.NewReader()
		require.False(t, buf.idle(), "a retry reads the chunks again")

		ran := false
		buf.whenIdle(func() { ran = true })
		got, err := io.ReadAll(again)
		require.NoError(t, err)
		require.Equal(t, content, string(got), "the whole content, from the start")
		require.True(t, ran)

		closer, ok := again.(io.Closer)
		require.True(t, ok)
		require.NoError(t, closer.Close())
		require.True(t, buf.idle())
	})

	t.Run("runs at once when nothing is reading", func(t *testing.T) {
		t.Parallel()

		ran := false
		newTestChunkBuffer().whenIdle(func() { ran = true })
		require.True(t, ran)
	})
}

func TestByteBudgetLendsTheWholeLimitToALoneShard(t *testing.T) {
	t.Parallel()

	b, _ := budgetWith()

	n, err := b.acquire(t.Context(), nil, testBudgetKey, testBudgetLimit)
	require.NoError(t, err)
	require.EqualValues(t, testBudgetLimit, n, "with nobody else waiting, one shard may use all of it")
}

func TestByteBudgetAlwaysLetsAShardThatHoldsNothingTakeOneItem(t *testing.T) {
	t.Parallel()

	b, _ := budgetWith()
	stalled, other := shardKey{index: "idx", shard: 0}, shardKey{index: "idx", shard: 1}

	_, err := b.acquire(t.Context(), nil, stalled, testBudgetLimit)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	n, err := b.acquire(ctx, nil, other, 30)
	require.NoError(t, err, "a shard holding nothing must not wait on a limit another shard has spent")
	require.EqualValues(t, 30, n)
	require.EqualValues(t, testBudgetLimit+30, b.inUse(), "past the limit by that one item")
}

// TestByteBudgetServesOtherShardsPastOneHeldToItsShare: a shard over its share waits
// while another shard is waiting, and the other is served past it. Once nobody else
// wants room it may borrow again, without waiting for another release.
func TestByteBudgetServesOtherShardsPastOneHeldToItsShare(t *testing.T) {
	t.Parallel()

	b, blocked := budgetWith()
	big, small := shardKey{index: "idx", shard: 0}, shardKey{index: "idx", shard: 1}

	_, err := b.acquire(t.Context(), nil, big, 70)
	require.NoError(t, err)
	_, err = b.acquire(t.Context(), nil, small, 20)
	require.NoError(t, err)

	bigDone, smallDone := make(chan error, 1), make(chan error, 1)
	go func() {
		_, acquireErr := b.acquire(t.Context(), nil, big, 20) // Past the limit: waits.
		bigDone <- acquireErr
	}()
	receive(t, blocked)
	go func() {
		_, acquireErr := b.acquire(t.Context(), nil, small, 20) // Past the limit: waits behind big.
		smallDone <- acquireErr
	}()
	receive(t, blocked)

	// 30 comes back: big still holds 40 of a share of 50, and wants 20 more, so it is
	// passed over for small, which is within its share.
	b.release(big, 30)
	require.NoError(t, receive(t, smallDone), "the shard within its share is served past the one over it")
	require.NoError(t, receive(t, bigDone), "and the other, alone now in wanting room, borrows what is left")
	require.EqualValues(t, testBudgetLimit, b.inUse())
}
