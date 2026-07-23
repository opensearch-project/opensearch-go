// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package opensearch is a minimal stand-in for opensearch-go/v4's root package,
// carrying just enough for a fixture to construct a typed client. See the
// sibling v2/v3 stubs for the general rationale.
package opensearch

import "net/http"

// Config is the v4 root client config (nested inside opensearchapi.Config).
type Config struct {
	Addresses []string
}

// Client is the v4 transport-level client wrapped by the opensearchapi Client.
type Client struct {
	Transport http.RoundTripper
}
