// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package osotel provides an OpenTelemetry-backed registry that turns
// [opensearchtransport] observer events into metrics off the request hot path.
//
// A [Registry] is the single [opensearchtransport.ConnectionObserver] wired into
// a transport. The caller wires an arbitrary set of [Observer] sinks into it; the
// Registry owns one async pipeline (pooled envelopes, a buffered channel, and a
// pool of dispatch workers) and fans each event out to the wired sinks.
// Recording metrics therefore never blocks a request: when the buffer is full,
// events are dropped and a counter is incremented, making backpressure
// observable rather than latent.
//
// Each [Observer] is a sink: it receives the full transport event and routes it
// to whatever instruments it owns. The shipped [RequestObserver] is a
// low-cardinality RED default and [PoolObserver] a USE default; to attribute by
// route, index, or any other high-cardinality dimension, implement [Observer]
// over your own instruments.
//
// The OpenTelemetry libraries live only in this module's dependency graph; the
// core opensearch-go modules do not depend on them. The design mirrors the
// osprom module; the difference is the metric backend.
package osotel

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"

	"go.opentelemetry.io/otel/metric"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// OpenTelemetry instrument name prefix shared by every instrument in this module.
const instrumentPrefix = "opensearch.client."

// Metric attribute keys and shared attribute values, declared once so the
// observers and their tests spell them identically.
const (
	attrMethod = "method"
	attrStatus = "status"
	attrMode   = "mode"
	attrPool   = "pool"
	attrState  = "state"

	modeRequestLabel = "request"
	modeStreamLabel  = "stream"
	statusError      = "error"
)

// mode discriminates which event an [envelope] carries across the channel.
type mode uint8

const (
	modeRequest mode = iota // buffered request: OnRequestResponse
	modeStream              // streaming request: OnStreamResponse
)

// envelope is the pooled payload carried across the channel to a dispatch
// worker. It holds the concrete transport event by value (the events are flat
// value types) plus a discriminator; only the field matching mode is populated.
// Cleared before return to the pool so a retained error reference cannot pin
// heap objects across reuse.
type envelope struct {
	mode   mode
	req    opensearchtransport.RequestResponseEvent
	stream opensearchtransport.StreamResponseEvent
}

// Observer is a metric sink wired into a [Registry]. Register is called once at
// construction to create the sink's instruments from the meter; the On* hooks
// are called with the relevant transport event. The request hooks
// (OnRequestResponse/OnStreamResponse) run on a dispatch worker off the request
// hot path with the dispatch context; the connection-lifecycle hooks run
// synchronously on the transport goroutine that fired them (they are
// low-frequency, so they are not buffered) with a background context. Every
// event pointer is valid only for the duration of the call and must not be
// retained. Embed [BaseObserver] for no-op defaults and override only the hooks
// the sink needs.
type Observer interface {
	// Register creates the sink's instruments from meter.
	Register(meter metric.Meter) error

	// OnRequestResponse records a completed buffered (full-read) request.
	OnRequestResponse(ctx context.Context, e *opensearchtransport.RequestResponseEvent)
	// OnStreamResponse records a completed streaming (time-to-first-byte) request.
	OnStreamResponse(ctx context.Context, e *opensearchtransport.StreamResponseEvent)

	// OnPromote is called when a dead connection becomes ready (resurrection).
	OnPromote(ctx context.Context, e *opensearchtransport.ConnectionEvent)
	// OnDemote is called when a ready connection becomes dead (request failure).
	OnDemote(ctx context.Context, e *opensearchtransport.ConnectionEvent)
	// OnOverloadDetected is called when a connection is demoted because the
	// node's resource usage exceeds configured thresholds.
	OnOverloadDetected(ctx context.Context, e *opensearchtransport.ConnectionEvent)
	// OnOverloadCleared is called when an overload-demoted connection returns
	// to ready after resource usage normalizes.
	OnOverloadCleared(ctx context.Context, e *opensearchtransport.ConnectionEvent)
	// OnHealthCheckFail is called when a connection fails a health check.
	OnHealthCheckFail(ctx context.Context, e *opensearchtransport.ConnectionEvent)
}

// BaseObserver is an embeddable no-op [Observer]. Embed it and override only the
// hooks you need.
type BaseObserver struct{}

// Register implements Observer (no-op).
func (BaseObserver) Register(metric.Meter) error { return nil }

// OnRequestResponse implements Observer (no-op).
func (BaseObserver) OnRequestResponse(context.Context, *opensearchtransport.RequestResponseEvent) {}

// OnStreamResponse implements Observer (no-op).
func (BaseObserver) OnStreamResponse(context.Context, *opensearchtransport.StreamResponseEvent) {}

// OnPromote implements Observer (no-op).
func (BaseObserver) OnPromote(context.Context, *opensearchtransport.ConnectionEvent) {}

// OnDemote implements Observer (no-op).
func (BaseObserver) OnDemote(context.Context, *opensearchtransport.ConnectionEvent) {}

// OnOverloadDetected implements Observer (no-op).
func (BaseObserver) OnOverloadDetected(context.Context, *opensearchtransport.ConnectionEvent) {}

// OnOverloadCleared implements Observer (no-op).
func (BaseObserver) OnOverloadCleared(context.Context, *opensearchtransport.ConnectionEvent) {}

// OnHealthCheckFail implements Observer (no-op).
func (BaseObserver) OnHealthCheckFail(context.Context, *opensearchtransport.ConnectionEvent) {}

// RequestFilter decides whether a buffered request event is recorded. It runs on
// the caller's request goroutine before the event is enqueued, so returning
// false skips the event entirely (it never crosses the channel). It must not
// block and must not retain the event pointer.
type RequestFilter func(*opensearchtransport.RequestResponseEvent) bool

// StreamFilter is the streaming counterpart of [RequestFilter].
type StreamFilter func(*opensearchtransport.StreamResponseEvent) bool

// RequestOverflowHandler is invoked on the caller's request goroutine each time a
// buffered ([Registry.OnRequestResponse]) event is dropped because the registry
// buffer is full. queueLen is the channel occupancy read at drop time; it is at
// most the configured buffer size (a dispatch worker may drain an entry between
// the full-buffer detection and the read, so it can be less). dropped points to
// the discarded event; it is valid only for the duration of the call and must
// not be retained. The handler must not block: it runs on the request hot path
// precisely when the buffer is already saturated. The built-in dropped counter
// is incremented regardless of whether a handler is set.
type RequestOverflowHandler func(queueLen int, dropped *opensearchtransport.RequestResponseEvent)

// StreamOverflowHandler is the streaming ([Registry.OnStreamResponse])
// counterpart of [RequestOverflowHandler]. The same calling contract applies.
type StreamOverflowHandler func(queueLen int, dropped *opensearchtransport.StreamResponseEvent)

// Registry is the single [opensearchtransport.ConnectionObserver] wired into a
// transport. It copies each event into a pooled envelope, dispatches it to a
// pool of background workers over a buffered channel, and fans it out to the
// wired [Observer] sinks. Run the dispatch workers with [Registry.Run]; stop
// them with [Registry.Close] or by canceling Run's context.
type Registry struct {
	opensearchtransport.BaseConnectionObserver

	observers atomic.Pointer[[]Observer]
	ch        chan *envelope
	pool      sync.Pool
	done      chan struct{}
	stop      func() // idempotent close of done, via sync.OnceFunc
	log       *slog.Logger
	workers   int

	reqFilter      RequestFilter
	streamFilter   StreamFilter
	reqOverflow    RequestOverflowHandler
	streamOverflow StreamOverflowHandler

	dropped metric.Int64Counter
}

// Option configures a [Registry].
type Option func(*options)

type options struct {
	logger         *slog.Logger
	workers        int
	bufferSize     int
	reqFilter      RequestFilter
	streamFilter   StreamFilter
	reqOverflow    RequestOverflowHandler
	streamOverflow StreamOverflowHandler
}

// WithLogger sets the logger used for lifecycle messages. Defaults to
// [slog.Default].
func WithLogger(l *slog.Logger) Option {
	return func(o *options) { o.logger = l }
}

// WithBufferSize sets the capacity of the channel buffering events between the
// request hot path and the dispatch workers. Values below 1 are ignored, and a
// GOMAXPROCS-scaled default is used. A larger buffer absorbs bigger bursts
// before events are dropped, at the cost of memory.
func WithBufferSize(n int) Option {
	return func(o *options) {
		if n >= 1 {
			o.bufferSize = n
		}
	}
}

// WithWorkers sets the number of dispatch workers draining the buffer. Values
// below 1 are ignored. Defaults to max(1, GOMAXPROCS/2). OpenTelemetry
// instruments are safe for concurrent use, so multiple workers may record into
// the same instrument.
func WithWorkers(n int) Option {
	return func(o *options) {
		if n >= 1 {
			o.workers = n
		}
	}
}

// WithRequestFilter sets a predicate deciding whether a buffered request event
// is recorded. It runs on the request goroutine before enqueue, so a false
// result skips the event without touching the channel. See [RequestFilter].
func WithRequestFilter(fn RequestFilter) Option {
	return func(o *options) { o.reqFilter = fn }
}

// WithStreamFilter is the streaming counterpart of [WithRequestFilter].
func WithStreamFilter(fn StreamFilter) Option {
	return func(o *options) { o.streamFilter = fn }
}

// WithOverflowHandler sets a handler invoked whenever a buffered request event
// is dropped because the buffer is full. It fires in addition to the built-in
// dropped counter, so callers can log the overflow without losing the metric.
// See [RequestOverflowHandler] for the calling contract.
func WithOverflowHandler(fn RequestOverflowHandler) Option {
	return func(o *options) { o.reqOverflow = fn }
}

// WithStreamOverflowHandler sets a handler invoked whenever a streaming request
// event is dropped because the buffer is full. It fires in addition to the
// built-in dropped counter. See [StreamOverflowHandler] for the calling
// contract.
func WithStreamOverflowHandler(fn StreamOverflowHandler) Option {
	return func(o *options) { o.streamOverflow = fn }
}

// New returns a Registry that creates its own instruments and every observer's
// instruments from meter, buffering events between the request hot path and the
// dispatch workers. The buffer defaults to a GOMAXPROCS-scaled size; override it
// with [WithBufferSize]. The caller must run [Registry.Run] to process events
// and call [Registry.Close] to stop.
func New(meter metric.Meter, observers ...Observer) (*Registry, error) {
	return NewWithOptions(meter, observers, nil)
}

// NewWithOptions is [New] with functional options.
func NewWithOptions(meter metric.Meter, observers []Observer, opts []Option) (*Registry, error) {
	cfg := options{logger: slog.Default()}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.workers < 1 {
		cfg.workers = defaultWorkers()
	}
	if cfg.bufferSize < 1 {
		cfg.bufferSize = defaultBufferSize()
	}
	if cfg.bufferSize < 1 {
		return nil, fmt.Errorf("osotel: buffer size must be at least 1, got %d", cfg.bufferSize)
	}

	dropped, err := meter.Int64Counter(
		instrumentPrefix+"observer.dropped",
		metric.WithDescription("Number of observer events dropped because the osotel buffer was full."),
		metric.WithUnit("{event}"),
	)
	if err != nil {
		return nil, err
	}

	r := &Registry{
		ch:             make(chan *envelope, cfg.bufferSize),
		done:           make(chan struct{}),
		log:            cfg.logger,
		workers:        cfg.workers,
		reqFilter:      cfg.reqFilter,
		streamFilter:   cfg.streamFilter,
		reqOverflow:    cfg.reqOverflow,
		streamOverflow: cfg.streamOverflow,
		dropped:        dropped,
	}
	obs := append([]Observer(nil), observers...)
	r.observers.Store(&obs)
	r.pool.New = func() any { return new(envelope) }
	r.stop = sync.OnceFunc(func() { close(r.done) })

	for _, o := range observers {
		if err := o.Register(meter); err != nil {
			return nil, err
		}
	}

	return r, nil
}

// sinks returns the current observer set. The returned slice is the live backing
// array and must be treated as read-only (it may be replaced by [Registry.SetObservers]
// concurrently, but its contents are never mutated in place).
func (r *Registry) sinks() []Observer {
	if p := r.observers.Load(); p != nil {
		return *p
	}
	return nil
}

// SetObservers atomically replaces the set of observers events are fanned out to.
// It is safe to call concurrently with event dispatch: in-flight events finish
// against the previous set, and every subsequent dispatch uses the new one.
//
// SetObservers changes only the fan-out; it does NOT create or clean up the
// observers' instruments on the meter. The caller owns instrument lifecycle by
// choosing when to call each observer's Register. Note that OpenTelemetry sync
// instruments cannot be removed from a meter once created; a swapped-out
// observer simply stops receiving events. The observers passed to
// [New]/[NewWithOptions] have Register called for you; those added later do not.
func (r *Registry) SetObservers(observers ...Observer) {
	obs := append([]Observer(nil), observers...)
	r.observers.Store(&obs)
}

// defaultWorkers returns the default dispatch-worker count: half the available
// parallelism, at least one.
func defaultWorkers() int {
	return max(1, runtime.GOMAXPROCS(0)/2)
}

// defaultBufferPerProc is the per-GOMAXPROCS channel-buffer capacity used to
// derive the default buffer size, so throughput headroom scales with the
// machine.
const defaultBufferPerProc = 1024

// defaultBufferSize returns the default channel buffer capacity, scaled with
// available parallelism so throughput headroom grows with the machine.
func defaultBufferSize() int {
	return defaultBufferPerProc * max(1, runtime.GOMAXPROCS(0))
}

// Run starts the dispatch workers, fanning each buffered event out to the wired
// observers, until ctx is cancelled or [Registry.Close] is called. On shutdown
// it drains events already buffered, then returns ctx.Err() (nil on a
// Close-triggered stop). The context passed to observers is ctx, so instrument
// recordings carry its cancellation and any baggage.
func (r *Registry) Run(ctx context.Context) error {
	r.log.Debug("osotel registry running", "buffer_size", cap(r.ch), "observers", len(r.sinks()), "workers", r.workers)

	var wg sync.WaitGroup
	wg.Add(r.workers)
	for range r.workers {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case <-r.done:
					return
				case env := <-r.ch:
					r.dispatch(ctx, env)
				}
			}
		}()
	}
	wg.Wait()

	// All workers have stopped; drain any events buffered at shutdown on this
	// single goroutine, so a clean stop does not discard already-accepted events.
	r.drain(ctx)

	if err := ctx.Err(); err != nil {
		return err
	}
	r.log.Debug("osotel registry stopped")
	return nil
}

// drain records every event currently buffered without blocking. Called from
// Run after all workers have exited, so it runs single-threaded.
func (r *Registry) drain(ctx context.Context) {
	for {
		select {
		case env := <-r.ch:
			r.dispatch(ctx, env)
		default:
			return
		}
	}
}

// dispatch fans one envelope out to the observers and returns it to the pool.
func (r *Registry) dispatch(ctx context.Context, env *envelope) {
	sinks := r.sinks()
	switch env.mode {
	case modeRequest:
		for _, o := range sinks {
			o.OnRequestResponse(ctx, &env.req)
		}
	case modeStream:
		for _, o := range sinks {
			o.OnStreamResponse(ctx, &env.stream)
		}
	}
	*env = envelope{}
	r.pool.Put(env)
}

// OnRequestResponse records a buffered (full-read) request. Safe for concurrent
// use; returns without blocking. Applies the request filter, then enqueues the
// event onto the dispatch channel; on a full buffer it drops the event,
// increments the dropped counter, and invokes any overflow handler.
func (r *Registry) OnRequestResponse(ctx context.Context, e opensearchtransport.RequestResponseEvent) {
	env := r.pool.Get().(*envelope) //nolint:forcetypeassert // pool only stores *envelope
	env.mode = modeRequest
	env.req = e

	if r.reqFilter != nil && !r.reqFilter(&env.req) {
		*env = envelope{}
		r.pool.Put(env)
		return
	}

	select {
	case r.ch <- env:
	default:
		queueLen := len(r.ch)
		*env = envelope{}
		r.pool.Put(env)
		r.dropped.Add(ctx, 1)
		if r.reqOverflow != nil {
			r.reqOverflow(queueLen, &e)
		}
	}
}

// OnStreamResponse records a streaming (time-to-first-byte) request. Safe for
// concurrent use; returns without blocking.
func (r *Registry) OnStreamResponse(ctx context.Context, e opensearchtransport.StreamResponseEvent) {
	env := r.pool.Get().(*envelope) //nolint:forcetypeassert // pool only stores *envelope
	env.mode = modeStream
	env.stream = e

	if r.streamFilter != nil && !r.streamFilter(&env.stream) {
		*env = envelope{}
		r.pool.Put(env)
		return
	}

	select {
	case r.ch <- env:
	default:
		queueLen := len(r.ch)
		*env = envelope{}
		r.pool.Put(env)
		r.dropped.Add(ctx, 1)
		if r.streamOverflow != nil {
			r.streamOverflow(queueLen, &e)
		}
	}
}

// Close stops the dispatch workers started by [Registry.Run]. It is idempotent
// and safe to call concurrently; subsequent calls are no-ops.
//
//nolint:unparam // Returns error to satisfy io.Closer; stop never fails
func (r *Registry) Close() error {
	r.stop()
	return nil
}

// Connection-lifecycle events are low-frequency and carry no per-request cost,
// so they are fanned out synchronously on the transport goroutine that fired
// them rather than routed through the buffered channel. There is no request
// context available on that path, so a background context is used.

// OnPromote implements [opensearchtransport.ConnectionObserver].
func (r *Registry) OnPromote(e opensearchtransport.ConnectionEvent) {
	for _, o := range r.sinks() {
		o.OnPromote(context.Background(), &e)
	}
}

// OnDemote implements [opensearchtransport.ConnectionObserver].
func (r *Registry) OnDemote(e opensearchtransport.ConnectionEvent) {
	for _, o := range r.sinks() {
		o.OnDemote(context.Background(), &e)
	}
}

// OnOverloadDetected implements [opensearchtransport.ConnectionObserver].
func (r *Registry) OnOverloadDetected(e opensearchtransport.ConnectionEvent) {
	for _, o := range r.sinks() {
		o.OnOverloadDetected(context.Background(), &e)
	}
}

// OnOverloadCleared implements [opensearchtransport.ConnectionObserver].
func (r *Registry) OnOverloadCleared(e opensearchtransport.ConnectionEvent) {
	for _, o := range r.sinks() {
		o.OnOverloadCleared(context.Background(), &e)
	}
}

// OnHealthCheckFail implements [opensearchtransport.ConnectionObserver].
func (r *Registry) OnHealthCheckFail(e opensearchtransport.ConnectionEvent) {
	for _, o := range r.sinks() {
		o.OnHealthCheckFail(context.Background(), &e)
	}
}
