// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package osprom provides a Prometheus-backed registry that turns
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
// Each [Observer] is a sink: it receives the full transport event
// ([opensearchtransport.RequestResponseEvent] or
// [opensearchtransport.StreamResponseEvent]) and routes it to whatever metrics it
// owns. The shipped [RequestObserver] is a low-cardinality RED default (method,
// status class, mode); to label by route, index, or any other high-cardinality
// dimension, implement [Observer] over your own pre-registered metrics.
//
// The Prometheus client library lives only in this module's dependency graph;
// the core opensearch-go modules do not depend on it.
package osprom

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// Prometheus metric namespace and subsystem shared by every collector in this
// module.
const (
	metricNamespace = "opensearch"
	metricSubsystem = "client"
)

// Metric label names and shared label values, declared once so the observers and
// their tests spell them identically.
const (
	labelMethod = "method"
	labelStatus = "status"
	labelMode   = "mode"
	labelPool   = "pool"
	labelState  = "state"

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
// construction to register the sink's collectors with the backend; the On*
// hooks are called with the relevant transport event. The request hooks
// (OnRequestResponse/OnStreamResponse) run on a dispatch worker off the request
// hot path; the connection-lifecycle hooks run synchronously on the transport
// goroutine that fired them (they are low-frequency, so they are not buffered).
// Every event pointer is valid only for the duration of the call and must not be
// retained. Embed [BaseObserver] for no-op defaults and override only the hooks
// the sink needs.
type Observer interface {
	// Register registers the sink's collectors with reg.
	Register(reg prometheus.Registerer) error

	// OnRequestResponse records a completed buffered (full-read) request.
	OnRequestResponse(*opensearchtransport.RequestResponseEvent)
	// OnStreamResponse records a completed streaming (time-to-first-byte) request.
	OnStreamResponse(*opensearchtransport.StreamResponseEvent)

	// OnPromote is called when a dead connection becomes ready (resurrection).
	OnPromote(*opensearchtransport.ConnectionEvent)
	// OnDemote is called when a ready connection becomes dead (request failure).
	OnDemote(*opensearchtransport.ConnectionEvent)
	// OnOverloadDetected is called when a connection is demoted because the
	// node's resource usage exceeds configured thresholds.
	OnOverloadDetected(*opensearchtransport.ConnectionEvent)
	// OnOverloadCleared is called when an overload-demoted connection returns
	// to ready after resource usage normalizes.
	OnOverloadCleared(*opensearchtransport.ConnectionEvent)
	// OnHealthCheckFail is called when a connection fails a health check.
	OnHealthCheckFail(*opensearchtransport.ConnectionEvent)
}

// BaseObserver is an embeddable no-op [Observer]. Embed it and override only the
// hooks you need.
type BaseObserver struct{}

// Register implements Observer (no-op).
func (BaseObserver) Register(prometheus.Registerer) error { return nil }

// OnRequestResponse implements Observer (no-op).
func (BaseObserver) OnRequestResponse(*opensearchtransport.RequestResponseEvent) {}

// OnStreamResponse implements Observer (no-op).
func (BaseObserver) OnStreamResponse(*opensearchtransport.StreamResponseEvent) {}

// OnPromote implements Observer (no-op).
func (BaseObserver) OnPromote(*opensearchtransport.ConnectionEvent) {}

// OnDemote implements Observer (no-op).
func (BaseObserver) OnDemote(*opensearchtransport.ConnectionEvent) {}

// OnOverloadDetected implements Observer (no-op).
func (BaseObserver) OnOverloadDetected(*opensearchtransport.ConnectionEvent) {}

// OnOverloadCleared implements Observer (no-op).
func (BaseObserver) OnOverloadCleared(*opensearchtransport.ConnectionEvent) {}

// OnHealthCheckFail implements Observer (no-op).
func (BaseObserver) OnHealthCheckFail(*opensearchtransport.ConnectionEvent) {}

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

	dropped prometheus.Counter
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
// below 1 are ignored. Defaults to max(1, GOMAXPROCS/2). Prometheus collectors
// are safe for concurrent use, so multiple workers may record into the same
// metric.
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

// New returns a Registry that registers its own metrics and every observer's
// metrics with reg, buffering events between the request hot path and the
// dispatch workers. The buffer defaults to a GOMAXPROCS-scaled size; override it
// with [WithBufferSize]. The caller must run [Registry.Run] to process events
// and call [Registry.Close] to stop.
func New(reg prometheus.Registerer, observers ...Observer) (*Registry, error) {
	return NewWithOptions(reg, observers, nil)
}

// NewWithOptions is [New] with functional options.
func NewWithOptions(reg prometheus.Registerer, observers []Observer, opts []Option) (*Registry, error) {
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
		return nil, fmt.Errorf("osprom: buffer size must be at least 1, got %d", cfg.bufferSize)
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
		dropped: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "observer_dropped_total",
			Help:      "Number of observer events dropped because the osprom buffer was full.",
		}),
	}
	obs := append([]Observer(nil), observers...)
	r.observers.Store(&obs)
	r.pool.New = func() any { return new(envelope) }
	r.stop = sync.OnceFunc(func() { close(r.done) })

	if err := reg.Register(r.dropped); err != nil {
		return nil, err
	}
	for _, o := range observers {
		if err := o.Register(reg); err != nil {
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
// SetObservers changes only the fan-out; it does NOT register or unregister the
// observers' collectors with the backend. The caller owns collector lifecycle:
// register a new observer's collectors with the Prometheus registerer before
// adding it (and unregister them after removing it, via [prometheus.Registerer.Unregister])
// to avoid duplicate-registration errors or orphaned series. The observers
// passed to [New]/[NewWithOptions] are registered for you; those added later are
// not.
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
// Close-triggered stop).
func (r *Registry) Run(ctx context.Context) error {
	r.log.Debug("osprom registry running", "buffer_size", cap(r.ch), "observers", len(r.sinks()), "workers", r.workers)

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
					r.dispatch(env)
				}
			}
		}()
	}
	wg.Wait()

	// All workers have stopped; drain any events buffered at shutdown on this
	// single goroutine, so a clean stop does not discard already-accepted events.
	r.drain()

	if err := ctx.Err(); err != nil {
		return err
	}
	r.log.Debug("osprom registry stopped")
	return nil
}

// drain records every event currently buffered without blocking. Called from
// Run after all workers have exited, so it runs single-threaded.
func (r *Registry) drain() {
	for {
		select {
		case env := <-r.ch:
			r.dispatch(env)
		default:
			return
		}
	}
}

// dispatch fans one envelope out to the observers and returns it to the pool.
func (r *Registry) dispatch(env *envelope) {
	sinks := r.sinks()
	switch env.mode {
	case modeRequest:
		for _, o := range sinks {
			o.OnRequestResponse(&env.req)
		}
	case modeStream:
		for _, o := range sinks {
			o.OnStreamResponse(&env.stream)
		}
	}
	*env = envelope{}
	r.pool.Put(env)
}

// OnRequestResponse records a buffered (full-read) request. Safe for concurrent
// use; returns without blocking. Applies the request filter, then enqueues the
// event onto the dispatch channel; on a full buffer it drops the event,
// increments the dropped counter, and invokes any overflow handler.
func (r *Registry) OnRequestResponse(_ context.Context, e opensearchtransport.RequestResponseEvent) {
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
		r.dropped.Inc()
		if r.reqOverflow != nil {
			r.reqOverflow(queueLen, &e)
		}
	}
}

// OnStreamResponse records a streaming (time-to-first-byte) request. Safe for
// concurrent use; returns without blocking.
func (r *Registry) OnStreamResponse(_ context.Context, e opensearchtransport.StreamResponseEvent) {
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
		r.dropped.Inc()
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
// them rather than routed through the buffered channel. Each is delivered to
// every wired sink by pointer (valid only for the call).

// OnPromote implements [opensearchtransport.ConnectionObserver].
func (r *Registry) OnPromote(e opensearchtransport.ConnectionEvent) {
	for _, o := range r.sinks() {
		o.OnPromote(&e)
	}
}

// OnDemote implements [opensearchtransport.ConnectionObserver].
func (r *Registry) OnDemote(e opensearchtransport.ConnectionEvent) {
	for _, o := range r.sinks() {
		o.OnDemote(&e)
	}
}

// OnOverloadDetected implements [opensearchtransport.ConnectionObserver].
func (r *Registry) OnOverloadDetected(e opensearchtransport.ConnectionEvent) {
	for _, o := range r.sinks() {
		o.OnOverloadDetected(&e)
	}
}

// OnOverloadCleared implements [opensearchtransport.ConnectionObserver].
func (r *Registry) OnOverloadCleared(e opensearchtransport.ConnectionEvent) {
	for _, o := range r.sinks() {
		o.OnOverloadCleared(&e)
	}
}

// OnHealthCheckFail implements [opensearchtransport.ConnectionObserver].
func (r *Registry) OnHealthCheckFail(e opensearchtransport.ConnectionEvent) {
	for _, o := range r.sinks() {
		o.OnHealthCheckFail(&e)
	}
}
