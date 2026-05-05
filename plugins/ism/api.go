// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ism

import (
	"context"
	"fmt"

	"github.com/opensearch-project/opensearch-go/v4"
)

// Config represents the client configuration
type Config struct {
	Client opensearch.Config
}

// Client represents the ism Client summarizing all API calls
type Client struct {
	Client   *opensearch.Client
	Policies policiesClient
}

// clientInit inits the Client with all sub clients
func clientInit(rootClient *opensearch.Client) *Client {
	client := &Client{
		Client: rootClient,
	}
	client.Policies = policiesClient{apiClient: client}
	return client
}

// NewClient returns a ism client
func NewClient(config Config) (*Client, error) {
	rootClient, err := opensearch.NewClient(config.Client)
	if err != nil {
		return nil, err
	}

	return clientInit(rootClient), nil
}

// do calls [opensearch.Do] and checks the response for response errors.
// The generic *T parameter enforces that dataPointer is a pointer at compile time.
func do[T any](ctx context.Context, c *Client, req opensearch.Request, dataPointer *T) (*opensearch.Response, error) {
	resp, err := opensearch.Do(ctx, c.Client, req, dataPointer)
	if err != nil {
		return nil, err
	}

	if resp.IsError() {
		if dataPointer == nil && resp.Body == nil {
			return resp, fmt.Errorf("status: %q", resp.Status())
		}
		return resp, opensearch.ParseError(resp)
	}

	return resp, nil
}

// FailedIndex contains information about fieled actions
type FailedIndex struct {
	IndexName string `json:"index_name"`
	IndexUUID string `json:"index_uuid"`
	Reason    string `json:"reason"`
}
