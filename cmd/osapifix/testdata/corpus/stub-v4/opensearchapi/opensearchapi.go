// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package opensearchapi is a minimal stand-in for opensearch-go/v4's
// opensearchapi package. It carries the nested Config, the typed Client, and
// AliasDeleteResp - a v4 response type removed outright in v5 - so a fixture can
// reference the removed type and exercise the cross-hop removed-type diagnostic.
package opensearchapi

import "github.com/opensearch-project/opensearch-go/v4"

// Config wraps the root client config, matching v4's exact shape.
type Config struct {
	Client opensearch.Config
}

// Client is the v4 typed API client.
type Client struct {
	Client *opensearch.Client
}

// NewClient builds a typed client from Config.
func NewClient(config Config) (*Client, error) {
	return &Client{}, nil
}

// AliasDeleteResp is a v4 response type with no v5 counterpart (removed in the
// v5 API). A reference to it must trip the removed-type MANUAL diagnostic on the
// v4 -> v5 hop.
type AliasDeleteResp struct {
	Acknowledged bool
}
