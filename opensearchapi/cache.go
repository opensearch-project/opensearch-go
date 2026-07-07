// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"context"
	"io"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/internal/envvars"
	"github.com/opensearch-project/opensearch-go/v5/internal/ttlcache"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// defaultClientCache caches implicit default api clients. Keying happens on the
// caller's config BEFORE default-router injection, so the common bulk-indexer
// path (which injects a router) stays cacheable -- keying after injection would
// bypass on the un-hashable Router every time.
//
//nolint:gochecknoglobals // process-wide singleton cache is the feature's purpose
var defaultClientCache = ttlcache.New(
	envvars.DefaultClientTTLValue(),
	ttlcache.WithLogger[*Client](ttlcacheDebugf),
)

// ttlcacheDebugf routes ttlcache's should-never-happen diagnostics to the
// shared debug logger, resolved per call so a logger installed after init is
// still honored. It is a no-op when none is installed (OPENSEARCH_GO_DEBUG unset).
func ttlcacheDebugf(format string, a ...any) {
	if dl := opensearchtransport.LoadDebugLogger(); dl != nil {
		_ = dl.Logf(format+"\n", a...)
	}
}

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

// cachedAPIDefault is the ttlcache.Cacheable for an implicit default api client.
// The key is precomputed by keyForConfig (NewDefaultClient only reaches the
// cache for a hashable, router-injectable config), so Key never errors. New
// builds the api client and its liveness probe on a miss.
type cachedAPIDefault struct {
	config Config
	key    uint64
}

// Key returns the precomputed cache key. It never errors: NewDefaultClient only
// constructs a cachedAPIDefault for a config keyForConfig already hashed.
//
//nolint:gosec // G115: key is an fnv hash; the bit pattern is the identity, not a magnitude
func (d cachedAPIDefault) Key() (ttlcache.Key, error) { return ttlcache.Key(d.key), nil }

// New builds the api client and its liveness probe on a cache miss.
//
//nolint:contextcheck // buildClient->NewClient takes no context yet; the ctx param is plumbed for when it does
func (d cachedAPIDefault) New(context.Context) (ttlcache.Value[*Client], error) {
	c, err := buildClient(d.config)
	if err != nil {
		return ttlcache.Value[*Client]{}, err
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
	return ttlcache.Value[*Client]{Obj: c, Closer: ttlcache.ClusterFunc{Closer: closer}, Liveness: liveness}, nil
}

// newCachedAPIDefault builds or reuses a cached api client for config. Each hit
// returns a fresh api Client over a fresh opensearch.Client that shares the
// cached transport (via SharedCopy) and carries its own release hook, so one
// Close maps to exactly one refcount decrement.
func newCachedAPIDefault(config Config, key uint64) (*Client, error) {
	value, release, err := defaultClientCache.GetOrCreate(context.Background(), cachedAPIDefault{config: config, key: key})
	if err != nil {
		return nil, err
	}
	return clientInit(value.Client.SharedCopy(release), value.errorMask()), nil
}
