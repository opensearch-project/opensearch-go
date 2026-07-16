// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package osotel provides an OpenTelemetry-backed registry that turns
// [opensearchtransport] observer events into metrics off the request hot path.
//
// A [Registry] is the single [opensearchtransport.ConnectionObserver] wired into
// a transport. The caller wires an OpenTelemetry [metric.Meter] and an arbitrary
// set of [Observer] bundles into it; the Registry owns one async pipeline
// (pooled envelopes, a buffered channel, and a dispatch goroutine) and fans each
// event out to the wired observers. Recording metrics therefore never blocks a
// request: when the buffer is full, events are dropped and a counter is
// incremented, making backpressure observable rather than latent.
//
// The OpenTelemetry libraries live only in this module's dependency graph; the
// core opensearch-go modules do not depend on them. The design mirrors the
// osprom module; the difference is the metric backend.
package osotel

import (
	"context"
	"log/slog"
	"sync"

	"go.opentelemetry.io/otel/metric"

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

// String returns the metric attribute value for the mode ("request" or
// "stream").
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
// at construction to create the bundle's instruments from the meter; OnRequest
// is called on the Registry's dispatch goroutine with the dispatch context.
// Embed [BaseObserver] for no-op defaults and override only what the bundle
// needs.
type Observer interface {
	// Register creates the bundle's instruments from meter.
	Register(meter metric.Meter) error
	// OnRequest records a completed request (both buffered and streaming).
	OnRequest(ctx context.Context, s RequestSample)
}

// BaseObserver is an embeddable no-op [Observer]. Embed it and override only the
// hooks you need.
type BaseObserver struct{}

// Register implements Observer (no-op).
func (BaseObserver) Register(metric.Meter) error { return nil }

// OnRequest implements Observer (no-op).
func (BaseObserver) OnRequest(context.Context, RequestSample) {}

// Registry is the single [opensearchtransport.ConnectionObserver] wired into a
// transport. It copies each event into a pooled envelope, dispatches it to a
// background goroutine over a buffered channel, and fans it out to the wired
// [Observer] bundles. Run the dispatch loop with [Registry.Run]; stop it with
// [Registry.Close] or by canceling Run's context.
type Registry struct {
	opensearchtransport.BaseConnectionObserver

	observers []Observer
	ch        chan *sample
	pool      sync.Pool
	done      chan struct{}
	stop      func() // idempotent close of done, via sync.OnceFunc
	log       *slog.Logger

	dropped metric.Int64Counter
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

// New returns a Registry that creates its own instruments and every observer's
// instruments from meter, buffering up to bufferSize events between the request
// hot path and the dispatch goroutine. The caller must run [Registry.Run] to
// process events and call [Registry.Close] to stop.
func New(meter metric.Meter, bufferSize int, observers ...Observer) (*Registry, error) {
	return NewWithOptions(meter, bufferSize, observers, nil)
}

// NewWithOptions is [New] with functional options.
func NewWithOptions(meter metric.Meter, bufferSize int, observers []Observer, opts []Option) (*Registry, error) {
	cfg := options{logger: slog.Default()}
	for _, opt := range opts {
		opt(&cfg)
	}

	dropped, err := meter.Int64Counter(
		"opensearch.client.observer.dropped",
		metric.WithDescription("Number of observer events dropped because the osotel buffer was full."),
		metric.WithUnit("{event}"),
	)
	if err != nil {
		return nil, err
	}

	r := &Registry{
		observers: observers,
		ch:        make(chan *sample, bufferSize),
		done:      make(chan struct{}),
		log:       cfg.logger,
		dropped:   dropped,
	}
	r.pool.New = func() any { return new(sample) }
	r.stop = sync.OnceFunc(func() { close(r.done) })

	for _, o := range observers {
		if err := o.Register(meter); err != nil {
			return nil, err
		}
	}

	return r, nil
}

// Run processes buffered events, fanning each out to the wired observers, until
// ctx is cancelled or [Registry.Close] is called. On shutdown it drains events
// already buffered, then returns ctx.Err() (nil on a Close-triggered stop). The
// context passed to observers is ctx, so instrument recordings carry its
// cancellation and any baggage.
func (r *Registry) Run(ctx context.Context) error {
	r.log.Debug("osotel registry running", "buffer_size", cap(r.ch), "observers", len(r.observers))
	for {
		select {
		case <-ctx.Done():
			r.drain(ctx)
			return ctx.Err()
		case <-r.done:
			r.drain(ctx)
			r.log.Debug("osotel registry stopped")
			return nil
		case env := <-r.ch:
			r.dispatch(ctx, env)
		}
	}
}

// drain records every event currently buffered without blocking, so a clean
// shutdown does not discard already-accepted events.
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
func (r *Registry) dispatch(ctx context.Context, env *sample) {
	rs := RequestSample{
		Method:      env.method,
		StatusClass: statusClass(env.statusCode),
		Seconds:     env.seconds,
		Bytes:       env.bytes,
		Mode:        env.mode,
	}
	for _, o := range r.observers {
		o.OnRequest(ctx, rs)
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
		r.dropped.Add(context.Background(), 1)
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
//
//nolint:unparam // Returns error to satisfy io.Closer; stop never fails
func (r *Registry) Close() error {
	r.stop()
	return nil
}
