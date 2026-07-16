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
// registerer plus one or more Observer bundles. The Registry is the single
// ConnectionObserver the transport holds, and its Run loop records metrics off
// the request hot path.
func Example() {
	promReg := prometheus.NewRegistry()

	// Wire the shipped request-metrics bundle (and any custom Observer bundles)
	// into the registry. Buffer up to 1024 events between requests and recording.
	reg, err := osprom.New(promReg, 1024, osprom.NewRequestObserver())
	if err != nil {
		panic(err)
	}

	// Run the dispatch loop until ctx is cancelled or reg.Close is called.
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
