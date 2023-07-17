// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

package opensearchapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

type stubTransport struct {
	req *http.Request
}

func (t *stubTransport) Perform(req *http.Request) (*http.Response, error) {
	t.req = req
	return &http.Response{}, nil
}

func TestIndicesGetDataStreamStatsRequest(t *testing.T) {
	tt := stubTransport{}
	req := IndicesGetDataStreamStatsRequest{}

	expectedPath := "/_data_stream/_stats"

	_, err := req.Do(context.Background(), &tt)
	if err != nil {
		t.Fatalf("Error getting response: %s", err)
	}

	require.Equal(t, expectedPath, tt.req.URL.Path)
}

func TestIndicesGetDataStreamStatsRequestOne(t *testing.T) {
	tt := stubTransport{}
	req := IndicesGetDataStreamStatsRequest{
		Name: "demo-1",
	}

	expectedPath := fmt.Sprintf("/_data_stream/%s/_stats", req.Name)

	_, err := req.Do(context.Background(), &tt)
	if err != nil {
		t.Fatalf("Error getting response: %s", err)
	}

	require.Equal(t, expectedPath, tt.req.URL.Path)
}
