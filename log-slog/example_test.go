// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package logslog_test

import (
	"log/slog"
	"os"

	"github.com/opensearch-project/opensearch-go/v5"
	logslog "github.com/opensearch-project/opensearch-go/v5/log-slog"
)

// Example installs the client's debug records into slog's package-level logger.
// The handler has to admit slog.LevelDebug: the default one discards anything
// below LevelInfo, so without this the records are dropped silently.
func Example() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))

	client, err := opensearch.NewClient(opensearch.Config{
		Addresses:   []string{"http://localhost:9200"},
		DebugLogger: logslog.Default(),
	})
	if err != nil {
		panic(err)
	}
	defer func() { _ = client.Close() }()
}

// Example_new passes an explicit logger instead of the package-level one, which
// is how to give the client's records their own handler or a group that
// distinguishes them from the rest of the application's output.
func Example_new() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})).With("component", "opensearch")

	client, err := opensearch.NewClient(opensearch.Config{
		Addresses:   []string{"http://localhost:9200"},
		DebugLogger: logslog.New(logger),
	})
	if err != nil {
		panic(err)
	}
	defer func() { _ = client.Close() }()
}
