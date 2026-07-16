# osotel

`osotel` records OpenSearch Go client request metrics to OpenTelemetry, off the request hot path.

The client fires per-request observer events; `osotel` copies each event into a pooled envelope and processes it on a background goroutine, so recording metrics never blocks or allocates on the request path. When the internal buffer is full, events are dropped and a counter is incremented -- backpressure is made observable rather than turned into latency.

`osotel` is a separate Go module, so the OpenTelemetry libraries stay out of the core client's dependency graph. You opt in by importing it. Its design mirrors the [`osprom`](../osprom) module; the difference is the metric backend.

## Install

```sh
go get github.com/opensearch-project/opensearch-go/v5/osotel
```

## Concepts

- **`Registry`** is the single [`opensearchtransport.ConnectionObserver`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchtransport#ConnectionObserver) you wire into a client. It owns the async pipeline and fans each event out to the observers wired into it.
- **`Observer`** is a metric bundle. `osotel` ships `RequestObserver` (duration and response-size histograms); you can add your own. A Registry can hold any number of them, so you compose whatever metric set you need.

## Usage

Wire an OpenTelemetry meter and one or more observers into a `Registry`, run its dispatch loop, and pass it as `Config.Observer`:

```go
package main

import (
	"context"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/metric"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/osotel"
)

func main() {
	ctx := context.Background()

	// Build a meter provider (here with an OTLP/HTTP exporter; swap in whatever
	// reader/exporter your deployment uses).
	exporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		panic(err)
	}
	provider := metric.NewMeterProvider(metric.WithReader(metric.NewPeriodicReader(exporter)))
	defer func() { _ = provider.Shutdown(ctx) }()

	meter := provider.Meter("github.com/opensearch-project/opensearch-go/v5/osotel")

	// Wire one or more Observer bundles into the registry. Buffer up to 1024
	// events between the request hot path and the recorder.
	reg, err := osotel.New(meter, 1024, osotel.NewRequestObserver())
	if err != nil {
		panic(err)
	}

	// Run the dispatch loop until ctx is cancelled or reg.Close is called.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _ = reg.Run(runCtx) }()
	defer reg.Close()

	client, err := opensearch.NewClient(opensearch.Config{
		Addresses: []string{"https://localhost:9200"},
		Observer:  reg, // the Registry is the client's observer
	})
	if err != nil {
		panic(err)
	}
	_ = client
}
```

With `opensearchapi`, set the same observer on the embedded `opensearch.Config`:

```go
apiClient, err := opensearchapi.NewClient(opensearchapi.Config{
	Client: opensearch.Config{
		Addresses: []string{"https://localhost:9200"},
		Observer:  reg,
	},
})
```

## Shipped metrics

`NewRequestObserver` records two histograms, attributed by `method`, status class (`2xx`, `4xx`, `error`, ...), and `mode` (`request` for a buffered full read, `stream` for time-to-first-byte):

| Instrument                           | Unit | Description                                                                               |
| ------------------------------------ | ---- | ----------------------------------------------------------------------------------------- |
| `opensearch.client.request.duration` | `s`  | Request duration.                                                                         |
| `opensearch.client.response.size`    | `By` | Buffered response size (recorded for `request` mode only, where the exact size is known). |

The Registry also records `opensearch.client.observer.dropped`, the count of events dropped because the buffer was full -- watch it to tell whether the buffer size is adequate for your throughput.

## Custom observers

Any type implementing `osotel.Observer` can be wired into a Registry alongside (or instead of) the shipped bundle. Embed `osotel.BaseObserver` for no-op defaults and override only what you need. `Register` is called once with the meter when you pass the observer to `New`; `OnRequest` is called on the dispatch goroutine (with the dispatch context) for every request.

```go
type errorCounter struct {
	osotel.BaseObserver
	errors metric.Int64Counter
}

func newErrorCounter() *errorCounter { return &errorCounter{} }

func (o *errorCounter) Register(meter metric.Meter) error {
	var err error
	o.errors, err = meter.Int64Counter(
		"myapp.opensearch.errors",
		metric.WithDescription("OpenSearch client error responses by method and status class."),
	)
	return err
}

func (o *errorCounter) OnRequest(ctx context.Context, s osotel.RequestSample) {
	switch s.StatusClass {
	case "4xx", "5xx", "error":
		o.errors.Add(ctx, 1, metric.WithAttributes(
			attribute.String("method", s.Method),
			attribute.String("status", s.StatusClass),
		))
	}
}
```

Wire it in together with the shipped bundle:

```go
reg, err := osotel.New(meter, 1024, osotel.NewRequestObserver(), newErrorCounter())
```

Each observer receives the same `RequestSample` per request, so you can build several independent metric bundles over one event stream.

## Lifecycle

- `New(meter, bufferSize, observers...)` creates every observer's instruments (plus the dropped counter) from `meter` and returns the Registry.
- `Run(ctx)` processes events until `ctx` is cancelled or `Close` is called, draining already-buffered events before returning. The context is passed to each observer's `OnRequest`, so recordings carry its cancellation and baggage. Run it in its own goroutine.
- `Close()` stops the dispatch loop. It is idempotent and safe to call concurrently.

See also the [Observer-Based Metrics guide](../guides/transport-observer_metrics.md) for the underlying event model.
