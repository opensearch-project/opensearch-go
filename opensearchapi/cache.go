// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"io"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/internal/clientcache"
	"github.com/opensearch-project/opensearch-go/v5/internal/envvars"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// defaultClientCache caches implicit default api clients. Keying happens on the
// caller's config BEFORE default-router injection, so the common bulk-indexer
// path (which injects a router) stays cacheable -- keying after injection would
// bypass on the un-hashable Router every time.
//
//nolint:gochecknoglobals // process-wide singleton cache is the feature's purpose
var defaultClientCache = clientcache.New[*Client](envvars.DefaultClientTTLValue())

// keyForConfig returns the cache key for config and whether it is cacheable. It
// folds the resolved error mask into the hash so configs differing only by
// error mask do not share a client.
func keyForConfig(config Config) (uint64, bool) {
	base, ok := opensearch.HashConfig(config.Client)
	if !ok {
		return 0, false
	}
	//nolint:mnd // fixed 64-bit odd multiplier (golden ratio) for mask mixing
	return base ^ (uint64(resolveErrorMask(config)) * 0x9E3779B97F4A7C15), true
}

// newCachedAPIDefault builds or reuses a cached api client for config. Each hit
// returns a fresh api Client over a fresh opensearch.Client that shares the
// cached transport (via SharedCopy) and carries its own release hook, so one
// Close maps to exactly one refcount decrement.
func newCachedAPIDefault(config Config, key uint64) (*Client, error) {
	value, release, err := defaultClientCache.GetOrCreate(clientcache.HashKey(key), func() (clientcache.Constructed[*Client], error) {
		c, cerr := buildClient(config)
		if cerr != nil {
			return clientcache.Constructed[*Client]{}, cerr
		}
		closer, _ := c.Client.Transport.(io.Closer)
		liveness := func() int64 {
			m, ok := c.Client.Transport.(interface {
				Metrics() (opensearchtransport.Metrics, error)
			})
			if !ok {
				return -1
			}
			metrics, merr := m.Metrics()
			if merr != nil {
				return -1
			}
			return int64(metrics.Requests)
		}
		return clientcache.Constructed[*Client]{Value: c, Closer: clientcache.ClusterFunc{Closer: closer}, Liveness: liveness}, nil
	})
	if err != nil {
		return nil, err
	}
	return clientInit(value.Client.SharedCopy(release), value.errorMask()), nil
}
