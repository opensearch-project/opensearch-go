// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/internal/apiutil"
)

// Config represents the client configuration
type Config struct {
	Client opensearch.Config
}

// NewClient returns an api client
func NewClient(config Config) (*Client, error) {
	rootClient, err := opensearch.NewClient(config.Client)
	if err != nil {
		return nil, err
	}

	return clientInit(rootClient), nil
}

// NewDefaultClient returns an api client using defaults
func NewDefaultClient() (*Client, error) {
	defaultAddress := opensearch.DefaultScheme + "://" + net.JoinHostPort(opensearch.DefaultHost, strconv.Itoa(opensearch.DefaultPort))
	rootClient, err := opensearch.NewClient(opensearch.Config{
		Addresses: []string{defaultAddress},
	})
	if err != nil {
		return nil, err
	}

	return clientInit(rootClient), nil
}

// NewFromClient creates an api client from an existing opensearch.Client
func NewFromClient(client *opensearch.Client) *Client {
	return clientInit(client)
}

// do calls [opensearch.Do] and checks the response for OpenSearch API errors.
func do[T any](ctx context.Context, c *Client, method string, req opensearch.Request, dataPointer *T) (*opensearch.Response, error) {
	resp, err := opensearch.Do(ctx, c.Client, method, req, dataPointer)
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

// formatDuration converts duration to a string in the format accepted by
// OpenSearch. Delegates to apiutil.FormatDuration so the encoding lives in a
// single place; generated plugin packages reference apiutil directly.
func formatDuration(d time.Duration) string {
	return apiutil.FormatDuration(d)
}
