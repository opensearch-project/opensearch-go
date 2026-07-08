// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5"
)

func TestAPIClientClose(t *testing.T) {
	t.Run("nil root is no-op", func(t *testing.T) {
		c := &Client{}
		require.NoError(t, c.Close())
	})

	t.Run("delegates to root client", func(t *testing.T) {
		c, err := NewDefaultClient()
		require.NoError(t, err)
		require.NoError(t, c.Close())
	})
}

func TestAPINewDefaultClientCaches(t *testing.T) {
	c1, err := NewDefaultClient()
	require.NoError(t, err)
	t.Cleanup(func() { _ = c1.Close() })
	c2, err := NewDefaultClient()
	require.NoError(t, err)
	t.Cleanup(func() { _ = c2.Close() })

	require.NotSame(t, c1, c2)
	require.Same(t, c1.Client.Transport, c2.Client.Transport,
		"default api clients must share one transport")
	// The shared root must preserve config so GetConfig()-based patterns
	// (e.g. opensearchapi.NewClient(Config{Client: *c.Client.GetConfig()}))
	// do not nil-deref.
	require.NotNil(t, c1.Client.GetConfig(), "cached api client must preserve root config")
}

// TestAPIDefaultClientRidesOpensearchCache locks in that the api default
// client shares the one transport minted by the opensearch default cache
// instead of running a second cache of its own. Closing each client
// decrements the shared refcount exactly once (idempotent double-close).
func TestAPIDefaultClientRidesOpensearchCache(t *testing.T) {
	root, err := opensearch.NewDefaultClient()
	require.NoError(t, err)
	t.Cleanup(func() { _ = root.Close() })

	api, err := NewDefaultClient()
	require.NoError(t, err)
	t.Cleanup(func() { _ = api.Close() })

	require.Same(t, root.Transport, api.Client.Transport,
		"api default client must share the opensearch default cache's transport")

	// Close is idempotent: a second Close on the same wrapper is a no-op.
	require.NoError(t, api.Close())
	require.NoError(t, api.Close())
}

// TestAPINewClientNeverCaches locks in the acceptance criterion that
// user-built NewClient(config) clients never enter the shared cache -- only
// the implicit default path (NewDefaultClient) is cached. Two explicit calls
// with identical config must build independent transports.
func TestAPINewClientNeverCaches(t *testing.T) {
	cfg := Config{Client: opensearch.Config{Addresses: []string{"http://never-cache:9200"}}}
	c1, err := NewClient(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c1.Close() })
	c2, err := NewClient(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c2.Close() })

	require.NotSame(t, c1.Client.Transport, c2.Client.Transport,
		"explicit NewClient must not share a cached transport")
}
