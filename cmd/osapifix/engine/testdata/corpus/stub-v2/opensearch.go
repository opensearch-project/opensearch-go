// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package opensearch is a minimal stand-in for opensearch-go/v2's root package,
// exposing only the shapes osapifix's v2->v3 rewrite recognizes: the Client that
// embeds the API methods, the flat Config, and NewClient. It exists so the
// testdata corpus type-checks against the v2 import path without downloading the
// real module. Keep the exported surface in sync with surface_v2.json; the drift
// guards validate the rewrite tables, and this stub only has to satisfy the type
// checker for the fixtures under ../v2.
package opensearch

import (
	"net/http"

	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

// Config is the v2 flat client config. The v2->v3 rewrite wraps a literal of
// this type into opensearchapi.Config{Client: ...}.
type Config struct {
	Addresses []string
	Username  string
	Password  string
	Transport http.RoundTripper
}

// Client embeds the API method set, exactly as v2's real Client does. The
// idiom-2 rewrite recognizes calls whose receiver resolves to this type.
type Client struct {
	*opensearchapi.API
	Transport http.RoundTripper
}

// NewClient constructs a Client. The rewrite repoints this to
// opensearchapi.NewClient on the v3 side.
func NewClient(cfg Config) (*Client, error) {
	return &Client{API: opensearchapi.New()}, nil
}

// Perform lets *Client satisfy opensearchapi.Transport, so it can be passed as
// the second argument of the idiom-1 form opensearchapi.<X>Request{...}.Do(ctx, client).
func (c *Client) Perform(req *http.Request) (*http.Response, error) {
	return c.Transport.RoundTrip(req)
}

