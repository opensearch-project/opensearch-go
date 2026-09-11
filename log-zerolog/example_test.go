// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package logzerolog_test

import (
	"os"

	"github.com/rs/zerolog"

	logzerolog "github.com/opensearch-project/opensearch-go/log-zerolog/v5"
	"github.com/opensearch-project/opensearch-go/v5"
)

// Example installs the client's debug records into zerolog's package-level
// logger, so they inherit whatever format, level, and writer the application has
// already configured. Nothing is emitted unless a DebugLogger is installed, so
// this is the whole opt-in.
func Example() {
	client, err := opensearch.NewClient(opensearch.Config{
		Addresses:   []string{"http://localhost:9200"},
		DebugLogger: logzerolog.Default(),
	})
	if err != nil {
		panic(err)
	}
	defer func() { _ = client.Close() }()
}

// Example_new passes an explicit logger instead of the package-level one, which
// is how to give the client's records their own writer or a field that
// distinguishes them from the rest of the application's output.
func Example_new() {
	zl := zerolog.New(os.Stderr).With().Str("component", "opensearch").Logger()

	client, err := opensearch.NewClient(opensearch.Config{
		Addresses:   []string{"http://localhost:9200"},
		DebugLogger: logzerolog.New(zl),
	})
	if err != nil {
		panic(err)
	}
	defer func() { _ = client.Close() }()
}
