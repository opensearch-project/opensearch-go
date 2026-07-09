// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4"
)

func TestClientClose(t *testing.T) {
	t.Run("delegates to embedded opensearch.Client", func(t *testing.T) {
		tc := &stubTransportCloser{}
		c := &Client{Client: &opensearch.Client{Transport: tc}}
		require.NoError(t, c.Close())
		require.Equal(t, 1, tc.closed)
	})

	t.Run("no-op when embedded client is nil", func(t *testing.T) {
		c := &Client{}
		require.NoError(t, c.Close())
	})
}

// stubTransportCloser implements opensearchtransport.Interface + io.Closer.
type stubTransportCloser struct{ closed int }

//nolint:nilnil // stub: Perform is never called, only Close is exercised
func (s *stubTransportCloser) Perform(*http.Request) (*http.Response, error) { return nil, nil }

//nolint:unparam // Close must return error to satisfy io.Closer; stub never fails
func (s *stubTransportCloser) Close() error { s.closed++; return nil }
