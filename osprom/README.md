# osprom

`osprom` records OpenSearch Go client request metrics to Prometheus, off the request hot path.

The client fires per-request observer events; `osprom` copies each event into a pooled envelope and processes it on a background goroutine, so recording metrics never blocks or allocates on the request path. When the internal buffer is full, events are dropped and a counter is incremented -- backpressure is made observable rather than turned into latency.

`osprom` is a separate Go module, so `github.com/prometheus/client_golang` stays out of the core client's dependency graph. You opt in by importing it.

## Install

```sh
go get github.com/opensearch-project/opensearch-go/v5/osprom
```

## Concepts

- **`Registry`** is the single [`opensearchtransport.ConnectionObserver`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchtransport#ConnectionObserver) you wire into a client. It owns the async pipeline and fans each event out to the observers wired into it.
- **`Observer`** is a metric bundle. `osprom` ships `RequestObserver` (duration and response-size histograms); you can add your own. A Registry can hold any number of them, so you compose whatever metric set you need.

## Usage

Wire a Prometheus registerer and one or more observers into a `Registry`, run its dispatch loop, and pass it as `Config.Observer`:

```go
package main

import (
	"context"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/osprom"
)

func main() {
	promReg := prometheus.NewRegistry()

	// Wire one or more Observer bundles into the registry. Buffer up to 1024
	// events between the request hot path and the recorder.
	reg, err := osprom.New(promReg, 1024, osprom.NewRequestObserver())
	if err != nil {
		panic(err)
	}

	// Run the dispatch loop until ctx is cancelled or reg.Close is called.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = reg.Run(ctx) }()
	defer reg.Close()

	client, err := opensearch.NewClient(opensearch.Config{
		Addresses: []string{"https://localhost:9200"},
		Observer:  reg, // the Registry is the client's observer
	})
	if err != nil {
		panic(err)
	}
	_ = client

	// Expose the metrics for scraping.
	http.Handle("/metrics", promhttp.HandlerFor(promReg, promhttp.HandlerOpts{}))
	_ = http.ListenAndServe(":2112", nil)
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

`NewRequestObserver` records two histograms, labeled by `method`, status class (`2xx`, `4xx`, `error`, ...), and `mode` (`request` for a buffered full read, `stream` for time-to-first-byte):

| Metric                                       | Description                                                                               |
| -------------------------------------------- | ----------------------------------------------------------------------------------------- |
| `opensearch_client_request_duration_seconds` | Request duration.                                                                         |
| `opensearch_client_response_size_bytes`      | Buffered response size (recorded for `request` mode only, where the exact size is known). |

The Registry also exports `opensearch_client_observer_dropped_total`, the count of events dropped because the buffer was full -- watch it to tell whether the buffer size is adequate for your throughput.

Tune the histogram buckets:

```go
ro := osprom.NewRequestObserver(
	osprom.WithDurationBuckets([]float64{0.005, 0.01, 0.05, 0.1, 0.5, 1, 5}),
	osprom.WithSizeBuckets(prometheus.ExponentialBuckets(256, 4, 8)),
)
reg, err := osprom.New(promReg, 1024, ro)
```

## Custom observers

Any type implementing `osprom.Observer` can be wired into a Registry alongside (or instead of) the shipped bundle. Embed `osprom.BaseObserver` for no-op defaults and override only what you need. `Register` is called once when you pass the observer to `New`; `OnRequest` is called on the dispatch goroutine for every request.

Because `OnRequest` runs once per request, keep it allocation-light. If a call needs scratch state, pool it -- osprom pools the event envelope, but a custom observer owns any allocations it makes. Here an error counter reuses a `prometheus.Labels` map (a map is pointer-shaped, so pooling it avoids a per-request map allocation without boxing):

```go
type errorCounter struct {
	osprom.BaseObserver
	errors *prometheus.CounterVec
	labels sync.Pool // reused prometheus.Labels, one per goroutine touching OnRequest
}

func newErrorCounter() *errorCounter {
	o := &errorCounter{
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "myapp",
			Name:      "opensearch_errors_total",
			Help:      "OpenSearch client error responses by method and status class.",
		}, []string{"method", "status"}),
	}
	o.labels.New = func() any { return prometheus.Labels{} }
	return o
}

func (o *errorCounter) Register(reg prometheus.Registerer) error {
	return reg.Register(o.errors)
}

func (o *errorCounter) OnRequest(s osprom.RequestSample) {
	switch s.StatusClass {
	case "4xx", "5xx", "error":
	default:
		return
	}
	labels := o.labels.Get().(prometheus.Labels)
	labels["method"] = s.Method
	labels["status"] = s.StatusClass
	o.errors.With(labels).Inc() // With reads the map; it does not retain it
	clear(labels)
	o.labels.Put(labels)
}
```

Wire it in together with the shipped bundle:

```go
reg, err := osprom.New(promReg, 1024, osprom.NewRequestObserver(), newErrorCounter())
```

Each observer receives the same `RequestSample` per request, so you can build several independent metric bundles over one event stream.

## Lifecycle

- `New(reg, bufferSize, observers...)` registers every observer's metrics (plus the dropped counter) with `reg` and returns the Registry.
- `Run(ctx)` processes events until `ctx` is cancelled or `Close` is called, draining already-buffered events before returning. Run it in its own goroutine.
- `Close()` stops the dispatch loop. It is idempotent and safe to call concurrently.

See also the [Observer-Based Metrics guide](../guides/transport-observer_metrics.md) for the underlying event model.
