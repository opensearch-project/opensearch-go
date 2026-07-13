// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package opensearch is a minimal stand-in for opensearch-go/v3's root package.
// v3 -> v4 is a quiet hop (the opensearchapi client and its types are identical
// across the two versions; only error types move package, handled as manual
// followups), so this stub carries just enough for a fixture to construct a
// typed client and have the import path bumped to v4. See the sibling v2 stub
// for the general rationale.
package opensearch

import "net/http"

// Config is the v3 root client config (nested inside opensearchapi.Config).
type Config struct {
	Addresses []string
	Username  string
	Password  string
	Header    http.Header
}

// Client is the v3 transport-level client wrapped by the opensearchapi Client.
type Client struct {
	Transport http.RoundTripper
}

// Response is the v3 raw response type returned by opensearchapi calls.
type Response struct {
	StatusCode int
}

func (r *Response) IsError() bool { return false }
