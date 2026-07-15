// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package osprom provides a Prometheus-backed registry that turns
// [opensearchtransport] observer events into metrics off the request hot path.
//
// A [Registry] is the single [opensearchtransport.ConnectionObserver] wired into
// a transport. The caller wires a Prometheus registerer and an arbitrary set of
// [Observer] bundles into it; the Registry owns one async pipeline (pooled
// envelopes, a buffered channel, and a dispatch goroutine) and fans each event
// out to the wired observers. Recording metrics therefore never blocks a
// request: when the buffer is full, events are dropped and a counter is
// incremented, making backpressure observable rather than latent.
//
// The Prometheus client library lives only in this module's dependency graph;
// the core opensearch-go modules do not depend on it.
package osprom

import (
	"context"
	"log/slog"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// sample is the flat, value-only payload carried across the channel to the
// dispatch goroutine. It holds no references into transport-owned memory, so a
// pooled envelope is safe to reuse once dispatched.
type sample struct {
	method     string
	statusCode int
	seconds    float64
	bytes      int64
	mode       Mode
}

// Mode distinguishes the two response fire points.
type Mode uint8

const (
	// ModeRequest is a buffered request: Duration is full-read time.
	ModeRequest Mode = iota
	// ModeStream is a streaming request: Duration is time-to-first-byte.
	ModeStream
)

// String returns the metric label for the mode ("request" or "stream").
func (m Mode) String() string {
	if m == ModeStream {
		return "stream"
	}
	return "request"
}

// RequestSample is the value delivered to an [Observer] for each completed
// request. All fields are copies; nothing references transport-owned memory.
type RequestSample struct {
	// Method is the HTTP method (e.g. "GET").
	Method string
	// StatusClass is the low-cardinality status label ("2xx", "4xx", "error").
	StatusClass string
	// Seconds is the request duration: full-read for ModeRequest, TTFB for
	// ModeStream.
	Seconds float64
	// Bytes is the response size when known (ModeRequest); 0 otherwise.
	Bytes int64
	// Mode distinguishes buffered (ModeRequest) from streaming (ModeStream).
	Mode Mode
}

// Observer is a metric bundle wired into a [Registry]. Register is called once
// at construction to register the bundle's metrics with the backend; the On*
// hooks are called on the Registry's dispatch goroutine. Embed [BaseObserver]
// for no-op defaults and override only the hooks the bundle needs.
type Observer interface {
	// Register registers the bundle's collectors with reg.
	Register(reg prometheus.Registerer) error
	// OnRequest records a completed request (both buffered and streaming).
	OnRequest(RequestSample)
}

// BaseObserver is an embeddable no-op [Observer]. Embed it and override only the
// hooks you need.
type BaseObserver struct{}

// Register implements Observer (no-op).
func (BaseObserver) Register(prometheus.Registerer) error { return nil }

// OnRequest implements Observer (no-op).
func (BaseObserver) OnRequest(RequestSample) {}

// Registry is the single [opensearchtransport.ConnectionObserver] wired into a
// transport. It copies each event into a pooled envelope, dispatches it to a
// background goroutine over a buffered channel, and fans it out to the wired
// [Observer] bundles. Run the dispatch loop with [Registry.Run]; stop it with
// [Registry.Close] or by cancelling Run's context.
type Registry struct {
	opensearchtransport.BaseConnectionObserver

	observers []Observer
	ch        chan *sample
	pool      sync.Pool
	done      chan struct{}
	stop      func() // idempotent close of done, via sync.OnceFunc
	log       *slog.Logger

	dropped prometheus.Counter
}

// Option configures a [Registry].
type Option func(*options)

type options struct {
	logger *slog.Logger
}

// WithLogger sets the logger used for lifecycle messages. Defaults to
// [slog.Default].
func WithLogger(l *slog.Logger) Option {
	return func(o *options) { o.logger = l }
}

// New returns a Registry that registers its own metrics and every observer's
// metrics with reg, buffering up to bufferSize events between the request hot
// path and the dispatch goroutine. The caller must run [Registry.Run] to
// process events and call [Registry.Close] to stop.
func New(reg prometheus.Registerer, bufferSize int, observers ...Observer) (*Registry, error) {
	return NewWithOptions(reg, bufferSize, observers, nil)
}

// NewWithOptions is [New] with functional options.
func NewWithOptions(reg prometheus.Registerer, bufferSize int, observers []Observer, opts []Option) (*Registry, error) {
	cfg := options{logger: slog.Default()}
	for _, opt := range opts {
		opt(&cfg)
	}

	r := &Registry{
		observers: observers,
		ch:        make(chan *sample, bufferSize),
		done:      make(chan struct{}),
		log:       cfg.logger,
		dropped: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "opensearch",
			Subsystem: "client",
			Name:      "observer_dropped_total",
			Help:      "Number of observer events dropped because the osprom buffer was full.",
		}),
	}
	r.pool.New = func() any { return new(sample) }
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

// Run processes buffered events, fanning each out to the wired observers, until
// ctx is cancelled or [Registry.Close] is called. On shutdown it drains events
// already buffered, then returns ctx.Err() (nil on a Close-triggered stop).
func (r *Registry) Run(ctx context.Context) error {
	r.log.Debug("osprom registry running", "buffer_size", cap(r.ch), "observers", len(r.observers))
	for {
		select {
		case <-ctx.Done():
			r.drain()
			return ctx.Err()
		case <-r.done:
			r.drain()
			r.log.Debug("osprom registry stopped")
			return nil
		case env := <-r.ch:
			r.dispatch(env)
		}
	}
}

// drain records every event currently buffered without blocking, so a clean
// shutdown does not discard already-accepted events.
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
func (r *Registry) dispatch(env *sample) {
	rs := RequestSample{
		Method:      env.method,
		StatusClass: statusClass(env.statusCode),
		Seconds:     env.seconds,
		Bytes:       env.bytes,
		Mode:        env.mode,
	}
	for _, o := range r.observers {
		o.OnRequest(rs)
	}
	*env = sample{}
	r.pool.Put(env)
}

// enqueue copies s into a pooled envelope and offers it to the dispatch
// goroutine without blocking. On a full buffer it drops the event and
// increments the dropped counter. It never sends on a closed channel: the
// channel is never closed; Close signals via the done channel instead.
func (r *Registry) enqueue(s sample) {
	env := r.pool.Get().(*sample) //nolint:forcetypeassert // pool only stores *sample
	*env = s
	select {
	case r.ch <- env:
	default:
		*env = sample{}
		r.pool.Put(env)
		r.dropped.Inc()
	}
}

// OnRequestResponse records a buffered (full-read) request. Safe for concurrent
// use; returns without blocking.
func (r *Registry) OnRequestResponse(e opensearchtransport.RequestResponseEvent) {
	r.enqueue(sample{
		method:     e.Request.Method,
		statusCode: e.StatusCode,
		seconds:    e.Duration.Seconds(),
		bytes:      e.ResponseBytes,
		mode:       ModeRequest,
	})
}

// OnStreamResponse records a streaming (time-to-first-byte) request. Safe for
// concurrent use; returns without blocking.
func (r *Registry) OnStreamResponse(e opensearchtransport.StreamResponseEvent) {
	r.enqueue(sample{
		method:     e.Request.Method,
		statusCode: e.StatusCode,
		seconds:    e.Duration.Seconds(),
		mode:       ModeStream,
	})
}

// Close stops the dispatch loop started by [Registry.Run]. It is idempotent and
// safe to call concurrently; subsequent calls are no-ops.
func (r *Registry) Close() error {
	r.stop()
	return nil
}
