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

// do calls the opensearch.Client.Do() and checks the response for response errors
func (c *Client) do(ctx context.Context, req opensearch.Request, dataPointer any) (*opensearch.Response, error) {
	resp, err := c.Client.Do(ctx, req, dataPointer)
	if err != nil {
		return nil, err
	}

	if resp.IsError() {
		if dataPointer != nil {
			return resp, opensearch.ParseError(resp)
		} else {
			return resp, fmt.Errorf("status: %s", resp.Status())
		}
	}

	return resp, nil
}

// FailedIndex contains information about fieled actions
type FailedIndex struct {
	IndexName string `json:"index_name"`
	IndexUUID string `json:"index_uuid"`
	Reason    string `json:"reason"`
}
