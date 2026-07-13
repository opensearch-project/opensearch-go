// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package opensearchapi is a minimal stand-in for opensearch-go/v3's
// opensearchapi package, carrying the nested Config, the typed Client, and a
// Ping method so a fixture can exercise the v3 typed sub-client API. See the
// sibling opensearch.go for why this stub exists.
package opensearchapi

import (
	"context"

	"github.com/opensearch-project/opensearch-go/v3"
)

// Config wraps the root client config, matching v3's exact shape.
type Config struct {
	Client opensearch.Config
}

// Client is the v3 typed API client.
type Client struct {
	Client *opensearch.Client
}

// NewClient builds a typed client from Config.
func NewClient(config Config) (*Client, error) {
	return &Client{}, nil
}

// PingReq is the typed ping request.
type PingReq struct{}

// Ping is the v3 typed sub-client call shape.
func (c Client) Ping(ctx context.Context, req *PingReq) (*opensearch.Response, error) {
	return &opensearch.Response{}, nil
}
