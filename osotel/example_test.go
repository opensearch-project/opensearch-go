// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package osotel_test

import (
	"context"

	"go.opentelemetry.io/otel/sdk/metric"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/osotel"
)

// Example wires an osotel.Registry into an opensearch client: an OpenTelemetry
// meter plus one or more Observer bundles. The Registry is the single
// ConnectionObserver the transport holds, and its Run loop records metrics off
// the request hot path. Swap the ManualReader for an OTLP exporter's reader in
// production.
func Example() {
	provider := metric.NewMeterProvider(metric.WithReader(metric.NewManualReader()))
	meter := provider.Meter("github.com/opensearch-project/opensearch-go/v5/osotel")

	reg, err := osotel.New(meter, 1024, osotel.NewRequestObserver())
	if err != nil {
		panic(err)
	}

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

	// Output:
}
