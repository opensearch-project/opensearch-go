// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package opensearchapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/opensearch-project/opensearch-go/v2"
)

// Config represents the client configuration
type Config struct {
	Client opensearch.Config
}

// Client represents the opensearchapi Client summarizing all API calls
type Client struct {
	Client *opensearch.Client
	Cat    catClient
}

// clientInit inits the Client with all sub clients
func clientInit(rootClient *opensearch.Client) *Client {
	client := &Client{
		Client: rootClient,
	}
	client.Cat = catClient{apiClient: client}
	return client
}

// NewClient returns a opensearchapi client
func NewClient(config Config) (*Client, error) {
	rootClient, err := opensearch.NewClient(config.Client)
	if err != nil {
		return nil, err
	}

	return clientInit(rootClient), nil
}

// NewDefaultClient returns a opensearchapi client using defaults
func NewDefaultClient() (*Client, error) {
	rootClient, err := opensearch.NewClient(opensearch.Config{
		Addresses: []string{"http://localhost:9200"},
	})
	if err != nil {
		return nil, err
	}

	return clientInit(rootClient), nil
}

// do calls the opensearch.Client.Do() and checks the response for openseach api errors
func (c *Client) do(ctx context.Context, req opensearch.Request, dataPointer any) (*opensearch.Response, error) {
	resp, err := c.Client.Do(ctx, req, dataPointer)
	if err != nil {
		return nil, err
	}

	if resp.IsError() {
		if dataPointer != nil {
			return resp, parseError(resp)
		} else {
			return resp, fmt.Errorf("status: %s", resp.Status())
		}
	}

	return resp, nil
}

// praseError tries to parse the opensearch api error into an custom error
func parseError(resp *opensearch.Response) error {
	if resp.Body == nil {
		return fmt.Errorf("%w, status: %s", ErrUnexpectedEmptyBody, resp.Status())
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrReadBody, err)
	}

	if resp.StatusCode == http.StatusMethodNotAllowed {
		var apiError StringError
		if err = json.Unmarshal(body, &apiError); err != nil {
			return fmt.Errorf("%w: %w", ErrJSONUnmarshalBody, err)
		}
		return apiError
	}

	var apiError Error
	if err = json.Unmarshal(body, &apiError); err != nil {
		return fmt.Errorf("%w: %w", ErrJSONUnmarshalBody, err)
	}

	return apiError
}

// formatDuration converts duration to a string in the format
// accepted by Opensearch.
func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return strconv.FormatInt(int64(d), 10) + "nanos"
	}

	return strconv.FormatInt(int64(d)/int64(time.Millisecond), 10) + "ms"
}

// ToPointer converts any value to a pointer, mainly used for request parameters
func ToPointer[V any](value V) *V {
	return &value
}
