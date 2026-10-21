// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchutil

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/opensearch-project/opensearch-go/v5/internal/body"
)

// defaultChunkSize is the size of one slab chunk. A group holds a list of them
// and spills into a new one when the last is full.
//
//nolint:mnd // Well-known power-of-two chunk size.
const defaultChunkSize = 64 << 10 // 64 KiB

// chunkPool hands out fixed-size chunks and takes them back, so buffers that
// fill and flush over and over reuse memory instead of allocating it each time.
// A buffer is a list of chunks, which a group can release in any order: a ring
// would need first-in first-out release, and groups flush when they please.
type chunkPool struct {
	size int
	pool sync.Pool

	// allocated counts chunks the pool had to make because none was free.
	allocated atomic.Uint64
	// served counts every chunk handed out, and returned every one given back.
	served   atomic.Uint64
	returned atomic.Uint64
}

func newChunkPool(size int) *chunkPool {
	p := &chunkPool{size: size}
	p.pool.New = func() any {
		p.allocated.Add(1)
		chunk := make([]byte, size)

		return &chunk
	}

	return p
}

func (p *chunkPool) get() *[]byte {
	p.served.Add(1)

	return p.pool.Get().(*[]byte)
}

func (p *chunkPool) put(chunk *[]byte) {
	p.returned.Add(1)
	p.pool.Put(chunk)
}

// segment is a run of bytes in a chunkBuffer: either a pooled chunk the buffer
// owns and writes into, or a slice the caller lent it.
type segment struct {
	// data is the bytes of the segment. For an owned segment it is the written part
	// of the chunk, and grows as the buffer is written to.
	data []byte
	// chunk is the pooled chunk data lies in, and nil for a borrowed segment, which
	// is never written to or returned to a pool.
	chunk *[]byte
	// pool is the pool chunk came from.
	pool *chunkPool
}

// chunkBuffer is a byte buffer made of pooled chunks and of slices lent to it. It
// writes where a bytes.Buffer would, rolls back with Truncate, and reads out as an
// io.Reader, which is all a worker asks of it. It is not safe for concurrent use.
//
// A document sent without copying is a borrowed segment: the buffer holds the
// caller's slice, reads it where it is, and lets go of it on Truncate or Reset
// without having written to it. The caller must leave the slice alone until then,
// and until the last reader from NewReader has finished.
//
// It is also a [body.BufferedReader], so a request built on it carries a
// Content-Length and a GetBody as one built on a bytes.Buffer does, rather than
// being sent chunked and copied whole by the transport.
type chunkBuffer struct {
	pool *chunkPool
	// small, when set, supplies the chunk that follows a borrowed segment. What
	// follows a lent document is its newline and the next action line, tens of bytes
	// that would otherwise take a whole chunk of pool, so a buffer of lent documents
	// would cost a chunk each.
	small *chunkPool
	// segs holds the segments in order.
	segs []segment
	size int

	// readSeg and readOff are the position Read has reached, and read the bytes
	// before it. Read does not move past the last segment once it has read it, so a
	// write after that lands in the segment Read is in, not behind it.
	readSeg int
	readOff int
	read    int

	// active counts the readers handed to a transport that have neither reached the
	// end of the body nor been closed; see [chunkBuffer.lease]. onIdle runs when it
	// falls to zero.
	active atomic.Int32
	idleMu struct {
		sync.Mutex
		onIdle []idleFunc
	}
}

var _ body.BufferedReader = (*chunkBuffer)(nil)

func newChunkBuffer(pool *chunkPool) *chunkBuffer {
	return &chunkBuffer{pool: pool}
}

// Len returns the number of bytes written and not yet read.
func (b *chunkBuffer) Len() int {
	return b.size - b.read
}

// tail returns the free space at the end of the last chunk, adding a chunk when
// that one is full or the last segment is borrowed.
func (b *chunkBuffer) tail() []byte {
	pool := b.pool

	if n := len(b.segs); n > 0 {
		last := &b.segs[n-1]
		if last.chunk != nil && len(last.data) < len(*last.chunk) {
			return (*last.chunk)[len(last.data):]
		}

		if last.chunk == nil && b.small != nil {
			pool = b.small
		}
	}

	chunk := pool.get()
	b.segs = append(b.segs, segment{data: (*chunk)[:0], chunk: chunk, pool: pool})

	return *chunk
}

// commit records that n bytes were written to the free space tail returned.
func (b *chunkBuffer) commit(n int) {
	last := &b.segs[len(b.segs)-1]
	last.data = last.data[:len(last.data)+n]
	b.size += n
}

// Write appends p, spilling into new chunks as the last one fills. It implements io.Writer.
//
//nolint:unparam // Keeps the io.Writer signature.
func (b *chunkBuffer) Write(p []byte) (int, error) {
	written := len(p)
	for len(p) > 0 {
		n := copy(b.tail(), p)
		b.commit(n)
		p = p[n:]
	}

	return written, nil
}

// WriteString appends s. Like bytes.Buffer.WriteString it returns the length and a nil error,
// which callers that mirror that signature, tests among them, read.
//
//nolint:unparam // Keeps the bytes.Buffer signature.
func (b *chunkBuffer) WriteString(s string) (int, error) {
	written := len(s)
	for len(s) > 0 {
		n := copy(b.tail(), s)
		b.commit(n)
		s = s[n:]
	}

	return written, nil
}

// appendByte appends c.
func (b *chunkBuffer) appendByte(c byte) {
	b.tail()[0] = c
	b.commit(1)
}

// appendRef appends p without copying it. The buffer reads p where it is, so p
// must not change until the buffer has let go of it.
func (b *chunkBuffer) appendRef(p []byte) {
	if len(p) == 0 {
		return
	}

	b.segs = append(b.segs, segment{data: p})
	b.size += len(p)
}

// ReadFrom reads r to EOF into the buffer, as bytes.Buffer.ReadFrom does.
func (b *chunkBuffer) ReadFrom(r io.Reader) (int64, error) {
	var total int64
	for {
		n, err := r.Read(b.tail())
		b.commit(n)
		total += int64(n)

		if errors.Is(err, io.EOF) {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

// release lets go of a segment: a chunk goes back to the pool and a borrowed slice
// is only forgotten.
func (b *chunkBuffer) release(s segment) {
	if s.chunk != nil {
		s.pool.put(s.chunk)
	}
}

// Truncate discards all but the first n bytes, and releases the segments that
// held the rest. It panics when n is negative or beyond the end, as
// bytes.Buffer.Truncate does.
func (b *chunkBuffer) Truncate(n int) {
	if n < 0 || n > b.size {
		panic("opensearchutil: chunkBuffer: truncation out of range")
	}

	cut := b.size - n
	for cut > 0 {
		last := &b.segs[len(b.segs)-1]
		if len(last.data) > cut {
			last.data = last.data[:len(last.data)-cut]

			break
		}

		cut -= len(last.data)
		b.release(*last)
		*last = segment{}
		b.segs = b.segs[:len(b.segs)-1]
	}

	b.size = n

	// An empty chunk at the end is no use to a reader, and a position past the
	// content would stay there.
	if b.read > n {
		b.read = n
	}
	b.seek()
}

// seek puts the read position at b.read, which is at most the size.
func (b *chunkBuffer) seek() {
	b.readSeg, b.readOff = 0, 0

	rest := b.read
	for b.readSeg < len(b.segs)-1 && rest >= len(b.segs[b.readSeg].data) {
		rest -= len(b.segs[b.readSeg].data)
		b.readSeg++
	}
	b.readOff = rest
}

// Reset empties the buffer and returns its chunks to the pool.
func (b *chunkBuffer) Reset() {
	b.read = 0
	b.Truncate(0)
}

// advance moves the read position on by n bytes of the segment Read is in.
func (b *chunkBuffer) advance(n int) {
	b.readOff += n
	b.read += n

	if b.readOff == len(b.segs[b.readSeg].data) && b.readSeg < len(b.segs)-1 {
		b.readSeg++
		b.readOff = 0
	}
}

// Read reads the next unread bytes into p, as bytes.Buffer.Read does.
func (b *chunkBuffer) Read(p []byte) (int, error) {
	if b.Len() == 0 {
		if len(p) == 0 {
			return 0, nil
		}

		return 0, io.EOF
	}

	read := 0
	for len(p) > 0 && b.Len() > 0 {
		n := copy(p, b.unread())
		b.advance(n)
		read += n
		p = p[n:]
	}

	return read, nil
}

// unread returns the written bytes of the segment Read is in that it has not
// returned yet.
func (b *chunkBuffer) unread() []byte {
	return b.segs[b.readSeg].data[b.readOff:]
}

// WriteTo writes the unread bytes to w segment by segment without copying them, as
// io.Copy prefers when its source offers it.
func (b *chunkBuffer) WriteTo(w io.Writer) (int64, error) {
	var total int64
	for b.Len() > 0 {
		part := b.unread()
		n, err := w.Write(part)
		b.advance(n)
		total += int64(n)

		if err != nil {
			return total, err
		}
		if n < len(part) {
			return total, io.ErrShortWrite
		}
	}

	return total, nil
}

// NewReader returns a reader over the whole content, from its start, whatever has
// been read from the buffer itself and independent of it: a request that is sent
// again after the first attempt read the body to its end replays it from here. The
// segments must not be released while it is in use.
func (b *chunkBuffer) NewReader() io.Reader {
	readers := make([]io.Reader, 0, len(b.segs))
	for _, s := range b.segs {
		readers = append(readers, bytes.NewReader(s.data))
	}

	return io.MultiReader(readers...)
}

// String returns the unread bytes as a string. It copies, and exists for debug
// output.
func (b *chunkBuffer) String() string {
	if b == nil {
		return "<nil>"
	}

	out := make([]byte, 0, b.Len())
	for i := b.readSeg; i < len(b.segs); i++ {
		part := b.segs[i].data
		if i == b.readSeg {
			part = part[b.readOff:]
		}
		out = append(out, part...)
	}

	return string(out)
}

// byteBudget bounds the bytes a bulk client holds between Add and the end of the
// flush that sends them. Add allocates bytes from the budget before it queues an
// item, a worker adjusts the amount to what it actually wrote, and the flush that
// ends the item's life returns them. When the budget is spent Add waits; that wait
// is the backpressure that keeps memory near the limit however slow the cluster is.
// The limit is not a hard bound: see the one-item allowance below.
//
// The bytes are counted by the shard the item belongs to, so that one shard whose
// requests are slow cannot take the room every other shard needs. While other
// shards are waiting, a shard may hold no more than an even share of the limit;
// while nobody else is waiting, it may borrow up to the whole limit. A shard that
// holds nothing may always take one item, past the limit if need be, so that no
// shard is ever left with nothing in progress: the limit can be passed by at most
// one item for each shard that holds nothing else.
//
// Waiters are served in arrival order within a shard, so a large item is not
// starved by a stream of small ones. A waiter held back by its own shard's share
// does not hold back the waiters of other shards behind it; one held back by the
// limit does. A nil *byteBudget imposes no limit.
type byteBudget struct {
	limit int64

	// used is the bytes taken. It is written with mu held, so that admission reads a
	// figure no write is changing, and is read without it by inUse, which GetStats
	// calls and which must not contend with Add.
	used atomic.Int64

	mu struct {
		sync.Mutex
		// held is what each shard holds; a shard holding nothing has no entry.
		held    map[shardKey]int64
		waiters []*budgetWaiter
	}

	// onBlock, when set, is called without the lock each time an acquire begins
	// to wait. The bulk client uses it to flush buffered data that is not on its way
	// out already, so the wait does not last until the next flush interval.
	onBlock budgetBlockFunc
	// onUnblock, when set, is called with the time an acquire waited, once the
	// wait is over for whatever reason.
	onUnblock AddBlockedFunc
	// now is the clock the blocked time is read from.
	now func() time.Time
	// halt, when set, ends every wait for room when it closes, as the bulk client's
	// Cancel does.
	halt <-chan struct{}

	blocked     atomic.Uint64
	blockedTime atomic.Int64 // Nanoseconds.
}

// budgetBlockFunc is called each time a byteBudget acquire begins to wait.
type budgetBlockFunc func()

type budgetWaiter struct {
	key     shardKey
	n       int64
	ready   chan struct{}
	granted bool
}

func newByteBudget(limit int64) *byteBudget {
	b := &byteBudget{limit: limit, now: time.Now}
	b.mu.held = map[shardKey]int64{}

	return b
}

// errBudgetStopped is returned by acquire when the stop channel closes first.
var errBudgetStopped = errors.New("opensearchutil: bulk client stopped while waiting for buffer space")

// acquire takes n bytes for the shard key, waiting while they are not there. It
// returns the amount taken: n, or the limit when n is larger, because an item
// bigger than the whole budget must still be able to go through, alone. It fails
// with ctx's error when ctx ends first, or errBudgetStopped when stop closes
// first, and takes nothing.
func (b *byteBudget) acquire(ctx context.Context, stop <-chan struct{}, key shardKey, n int64) (int64, error) {
	if b == nil {
		return 0, nil
	}

	n = min(max(n, 0), b.limit)

	b.mu.Lock()
	if (len(b.mu.waiters) == 0 || b.mu.held[key] == 0) && b.admits(key, n) == budgetAdmit {
		b.take(key, n)
		b.mu.Unlock()

		return n, nil
	}

	w := &budgetWaiter{key: key, n: n, ready: make(chan struct{})}
	b.mu.waiters = append(b.mu.waiters, w)
	// The fast path above gives way to waiters already queued, but grant can still
	// admit this one at once, for example when its shard is under its share.
	b.grant()
	if w.granted {
		b.mu.Unlock()

		return n, nil
	}
	b.mu.Unlock()

	b.blocked.Add(1)
	if b.onBlock != nil {
		b.onBlock()
	}

	start := b.now()
	defer func() {
		waited := b.now().Sub(start)
		b.blockedTime.Add(int64(waited))
		if b.onUnblock != nil {
			b.onUnblock(ctx, waited)
		}
	}()

	var err error
	select {
	case <-w.ready:
		return n, nil
	case <-ctx.Done():
		err = ctx.Err()
	case <-stop:
		err = errBudgetStopped
	case <-b.halt:
		err = errBudgetStopped
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if w.granted {
		// The grant raced the cancellation and won: give the bytes back rather
		// than hand them to a caller that has gone.
		b.give(key, n)
		b.grant()

		return 0, err
	}

	if i := slices.Index(b.mu.waiters, w); i >= 0 {
		b.mu.waiters = slices.Delete(b.mu.waiters, i, i+1)
		// The head may have been the one in the way of those behind it.
		b.grant()
	}

	return 0, err
}

// release gives n bytes of the shard key back and wakes the waiters that now fit.
func (b *byteBudget) release(key shardKey, n int64) {
	if b == nil || n <= 0 {
		return
	}

	b.mu.Lock()
	b.give(key, n)
	b.grant()
	b.mu.Unlock()
}

// take and give move n bytes of the shard key in and out of use; they must be
// called with the lock held.
func (b *byteBudget) take(key shardKey, n int64) {
	b.used.Add(n)
	b.mu.held[key] += n
}

func (b *byteBudget) give(key shardKey, n int64) {
	b.used.Add(-n)
	if left := b.mu.held[key] - n; left > 0 {
		b.mu.held[key] = left
	} else {
		delete(b.mu.held, key)
	}
}

// budgetAdmission is what the budget can do for an acquire now.
type budgetAdmission uint8

const (
	// budgetAdmit: the bytes may be taken.
	budgetAdmit budgetAdmission = iota
	// budgetOverShare: the shard holds its share while other shards wait; the
	// waiters of other shards may be served past it.
	budgetOverShare
	// budgetFull: the limit is spent; nobody behind may be served past it.
	budgetFull
)

// admits says whether n bytes may be taken for the shard key now; it must be
// called with the lock held.
func (b *byteBudget) admits(key shardKey, n int64) budgetAdmission {
	held := b.mu.held[key]
	if held == 0 {
		// One item for a shard that holds nothing, whatever else is held, so that
		// no shard is ever left with nothing in progress.
		return budgetAdmit
	}

	if b.othersWaiting(key) && held+n > b.share() {
		return budgetOverShare
	}

	if b.used.Load()+n > b.limit {
		return budgetFull
	}

	return budgetAdmit
}

// othersWaiting reports whether an acquire for a shard other than key is
// waiting; it must be called with the lock held.
func (b *byteBudget) othersWaiting(key shardKey) bool {
	for _, w := range b.mu.waiters {
		if w.key != key {
			return true
		}
	}

	return false
}

// share is an even split of the limit among the shards that hold bytes or are
// waiting for them; it must be called with the lock held.
func (b *byteBudget) share() int64 {
	active := len(b.mu.held)
	for i, w := range b.mu.waiters {
		if _, holds := b.mu.held[w.key]; holds {
			continue
		}
		if !slices.ContainsFunc(b.mu.waiters[:i], func(e *budgetWaiter) bool { return e.key == w.key }) {
			active++
		}
	}

	return b.limit / int64(max(active, 1))
}

// grant serves the waiters that may be served, in order: within a shard in
// arrival order, and past a shard that is held to its share. Past a waiter the
// spent limit holds back, only a shard that holds nothing is served, its one
// item, so that a full limit never leaves a shard with nothing in progress. It
// must be called with the lock held.
func (b *byteBudget) grant() {
	var (
		passed map[shardKey]struct{} // Shards with a waiter passed over, whose later waiters must keep their place.
		full   bool
	)

	for i := 0; i < len(b.mu.waiters); {
		w := b.mu.waiters[i]
		if _, behind := passed[w.key]; behind || (full && b.mu.held[w.key] > 0) {
			i++

			continue
		}

		// A waiter is passed over when the limit is spent or its shard is over its
		// share. Only a spent limit also holds back the waiters behind it.
		if adm := b.admits(w.key, w.n); adm != budgetAdmit {
			full = full || adm == budgetFull
			if passed == nil {
				passed = map[shardKey]struct{}{}
			}
			passed[w.key] = struct{}{}
			i++

			continue
		}

		b.take(w.key, w.n)
		w.granted = true
		close(w.ready)
		b.mu.waiters = slices.Delete(b.mu.waiters, i, i+1)

		// A waiter passed over for its share may be within it now, or alone in
		// wanting room, so the waiters are looked at again from the oldest.
		i, passed, full = 0, nil, false
	}
}

// waiting returns the number of acquires waiting for room.
func (b *byteBudget) waiting() int {
	if b == nil {
		return 0
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	return len(b.mu.waiters)
}

// inUse returns the bytes currently taken. It takes no lock.
func (b *byteBudget) inUse() int64 {
	if b == nil {
		return 0
	}

	return b.used.Load()
}

// blockedStats returns how many acquires had to wait and for how long in total.
func (b *byteBudget) blockedStats() (uint64, time.Duration) {
	if b == nil {
		return 0, 0
	}

	return b.blocked.Load(), time.Duration(b.blockedTime.Load())
}

// A request body made of pooled chunks is read by the transport on its own
// schedule, and net/http can go on reading it after RoundTrip has returned: when
// the server answers before the whole body is written, or the request's context
// ends. The chunks must not go back to the pool, nor the lent slices back to their
// owners, while that can happen. So a body is handed to the transport through a
// lease that counts the readers that have neither reached the end nor been
// closed, and the chunks are recycled when the count is zero: at once in the
// ordinary case, and by the last reader to finish otherwise. A reader that never
// finishes leaves its chunks to the garbage collector, which is safe and only
// costs the reuse.

// lease returns the buffer as the body of a request. The buffer counts as in use
// until the body reaches its end or is closed.
func (b *chunkBuffer) lease() *leasedBody {
	b.active.Add(1)

	return &leasedBody{b: b}
}

// idle reports whether no reader the buffer handed out is still active.
func (b *chunkBuffer) idle() bool {
	return b.active.Load() == 0
}

// finishReader records that a reader is done, and runs the callback waiting for
// the buffer to fall idle when it was the last.
func (b *chunkBuffer) finishReader() {
	if b.active.Add(-1) != 0 {
		return
	}

	b.idleMu.Lock()
	callbacks := b.idleMu.onIdle
	b.idleMu.onIdle = nil
	b.idleMu.Unlock()

	for _, f := range callbacks {
		f()
	}
}

// idleFunc is run by [chunkBuffer.whenIdle] once no reader is active.
type idleFunc func()

// whenIdle runs f once no reader is active: now, when none is, and otherwise on
// the goroutine of the last reader to finish.
func (b *chunkBuffer) whenIdle(f idleFunc) {
	b.idleMu.Lock()
	if b.active.Load() == 0 {
		b.idleMu.Unlock()
		f()

		return
	}
	b.idleMu.onIdle = append(b.idleMu.onIdle, f)
	b.idleMu.Unlock()
}

// whenAllIdle runs f once none of bufs has an active reader.
func whenAllIdle(bufs []*chunkBuffer, f func()) {
	var pending atomic.Int32
	pending.Store(int32(len(bufs))) //nolint:gosec // A handful of buffers.

	for _, b := range bufs {
		b.whenIdle(func() {
			if pending.Add(-1) == 0 {
				f()
			}
		})
	}
}

// leasedBody is a chunkBuffer as the body of one request.
type leasedBody struct {
	b    *chunkBuffer
	once sync.Once
}

var _ body.BufferedReader = (*leasedBody)(nil)

func (l *leasedBody) finish() { l.once.Do(l.b.finishReader) }

// Read implements [io.Reader].
func (l *leasedBody) Read(p []byte) (int, error) {
	n, err := l.b.Read(p)
	if errors.Is(err, io.EOF) {
		l.finish()
	}

	return n, err
}

// WriteTo implements [io.WriterTo], so the transport can write the chunks out
// without copying them.
func (l *leasedBody) WriteTo(w io.Writer) (int64, error) {
	n, err := l.b.WriteTo(w)
	if err == nil {
		l.finish()
	}

	return n, err
}

// Len returns the number of bytes not yet read.
func (l *leasedBody) Len() int { return l.b.Len() }

// NewReader returns a counted reader over the whole content, for a request that is
// sent again.
func (l *leasedBody) NewReader() io.Reader {
	l.b.active.Add(1)

	return &leasedReader{Reader: l.b.NewReader(), b: l.b}
}

// Close implements [io.Closer].
//
//nolint:unparam // Keeps the io.Closer signature.
func (l *leasedBody) Close() error {
	l.finish()

	return nil
}

// leasedReader is one replay of a leased body.
type leasedReader struct {
	io.Reader
	b    *chunkBuffer
	once sync.Once
}

func (r *leasedReader) finish() { r.once.Do(r.b.finishReader) }

// Read implements [io.Reader].
func (r *leasedReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if errors.Is(err, io.EOF) {
		r.finish()
	}

	return n, err
}

// WriteTo implements [io.WriterTo], keeping the segment-by-segment write of the
// reader it wraps.
func (r *leasedReader) WriteTo(w io.Writer) (int64, error) {
	var (
		n   int64
		err error
	)
	if wt, ok := r.Reader.(io.WriterTo); ok {
		n, err = wt.WriteTo(w)
	} else {
		n, err = io.Copy(w, struct{ io.Reader }{r.Reader})
	}
	if err == nil {
		r.finish()
	}

	return n, err
}

// Close implements [io.Closer].
//
//nolint:unparam // Keeps the io.Closer signature.
func (r *leasedReader) Close() error {
	r.finish()

	return nil
}
