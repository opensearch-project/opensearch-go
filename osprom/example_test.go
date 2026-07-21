// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package osprom_test

import (
	"context"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/osprom"
)

// Example wires an osprom.Registry into an opensearch client: a Prometheus
// registerer plus one or more Observer sinks. The Registry is the single
// ConnectionObserver the transport holds, and its dispatch workers record
// metrics off the request hot path. RequestObserver (RED) and PoolObserver (USE)
// together give full RED+USE coverage.
func Example() {
	promReg := prometheus.NewRegistry()

	// Wire the shipped RED + USE sinks (and any custom Observer sinks) into the
	// registry. The event buffer scales with GOMAXPROCS; set it explicitly with
	// osprom.WithBufferSize via NewWithOptions.
	reg, err := osprom.New(promReg, osprom.NewRequestObserver(), osprom.NewPoolObserver())
	if err != nil {
		panic(err)
	}

	// Run the dispatch workers until ctx is cancelled or reg.Close is called.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = reg.Run(ctx) }()
	defer func() { _ = reg.Close() }()

	client, err := opensearch.NewClient(opensearch.Config{
		Addresses: []string{"http://localhost:9200"},
		Observer:  reg,
	})
	if err != nil {
		panic(err)
	}
	_ = client

	// Expose the metrics for scraping.
	http.Handle("/metrics", promhttp.HandlerFor(promReg, promhttp.HandlerOpts{}))

	// Output:
}
