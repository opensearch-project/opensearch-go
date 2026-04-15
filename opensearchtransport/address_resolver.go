// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"errors"
	"net/url"
	"time"
)

// ErrAllResolversFailed is returned by the built-in resolution handler when
// every AddressResolverFunc invocation returned (nil, error), leaving zero
// usable nodes.
var ErrAllResolversFailed = errors.New("all address resolver calls failed")

// NodeInfo exposes discovered node metadata to the AddressResolverFunc callback.
//
// WARNING: NodeInfo shares memory with the client's internal node state.
// Resolvers MUST treat all fields as read-only. Mutating reference-type fields
// (Roles, Attributes, URL) corrupts internal state and the damage persists on
// the live Connection until the node is removed from the pool through
// rediscovery (identity change or cluster removal). The client does not
// defensively copy these fields for performance reasons.
type NodeInfo struct {
	// ID is the node's unique identifier from the cluster (the JSON key in /_nodes/http).
	ID string

	// Name is the node's human-readable name.
	Name string

	// Roles lists the node's configured roles (e.g. ["data", "ingest", "cluster_manager"]).
	Roles []string

	// Attributes contains the node's custom attributes (e.g. {"zone": "us-east-1"}).
	Attributes map[string]any

	// PublishAddress is the raw HTTP.PublishAddress string from the server
	// (e.g. "127.0.0.1:9200" or "hostname/127.0.0.1:9200").
	PublishAddress string

	// URL is the default-resolved URL computed by the client from PublishAddress.
	// The resolver can return this unchanged, modify it, or return a completely different URL.
	URL *url.URL
}

// AddressResolverFunc is called during node discovery for each node discovered
// via /_nodes/http. It receives the node's metadata and default URL, and returns
// a possibly-rewritten URL. This allows intercepting discovery to redirect traffic
// through sidecar proxies or rewrite hostnames for network topology reasons.
//
// Return semantics:
//   - (*url.URL, nil): use the returned URL (may be the same as node.URL or different)
//   - (*url.URL, error): use the returned URL; the error is logged but the
//     returned node value is used, even though an error occurred
//   - (nil, nil): keep the default URL (equivalent to returning node.URL)
//   - (nil, error): skip adding this node to the client; the error is logged
//     but does not fail the overall discovery operation
//
// The context carries the deadline/cancellation of the discovery call, allowing
// the resolver to perform network probes with appropriate timeouts.
type AddressResolverFunc func(ctx context.Context, node NodeInfo) (*url.URL, error)

// AddressRewriteEvent is emitted when an AddressResolverFunc rewrites a node's
// URL during discovery. It captures both the original and rewritten addresses
// for observability.
type AddressRewriteEvent struct {
	// ID is the node's unique identifier from the cluster.
	ID string

	// Name is the node's human-readable name.
	Name string

	// Roles lists the node's configured roles.
	Roles []string

	// OriginalURL is the URL computed from the server's publish_address
	// before the resolver modified it.
	OriginalURL string

	// RewrittenURL is the URL returned by the AddressResolverFunc.
	RewrittenURL string

	// Timestamp is when the rewrite occurred.
	Timestamp time.Time
}

// ResolvedAddress is one element of the slice returned by an
// AddressResolverRunnerFunc. Each entry maps a discovered node to the
// final URL that the client should use for that node's connection.
type ResolvedAddress struct {
	// Node is the same NodeInfo that was passed to the runner for this node.
	// The runner must not modify it.
	Node NodeInfo

	// URL is the resolved address for this node. When nil, the node is
	// excluded from the connection pool (the runner decided to drop it).
	URL *url.URL
}

// AddressResolverRunnerFunc replaces the built-in resolution handler when set.
// It receives the full list of discovered nodes (each with a default URL
// already computed from publish_address), the per-node AddressResolverFunc,
// and returns resolved addresses for the nodes that should enter the
// connection pool.
//
// The runner controls its own concurrency, failure policy, and retry logic.
// MaxAddressResolvers has no effect when a runner is configured.
//
// The per-node AddressResolverFunc passed to the runner is instrumented:
// each invocation automatically increments the client's call and error
// metrics counters, so the runner does not need to track these itself.
//
// Nodes absent from the returned slice (or present with a nil URL) are
// excluded from the pool. Returning an empty slice with a nil error causes
// the discovery cycle to fail with ErrAllResolversFailed.
//
// The context carries the deadline/cancellation of the discovery call.
type AddressResolverRunnerFunc func(
	ctx context.Context,
	nodes []NodeInfo,
	resolve AddressResolverFunc,
) ([]ResolvedAddress, error)
