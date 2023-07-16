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
