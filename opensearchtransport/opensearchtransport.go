// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package opensearchtransport

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/opensearch-project/opensearch-go/v4/internal/version"
	"github.com/opensearch-project/opensearch-go/v4/signer"
)

const (
	// Version is the package version as a string.
	Version                      = version.Client
	defaultMaxRetries            = 6
	defaultHealthCheckTimeout    = 5 * time.Second
	defaultRetryJitter           = 0.1
	defaultMaxRetryClusterHealth = 4 * time.Hour
)

var (
	reGoVersion          = regexp.MustCompile(`go(\d+\.\d+\..+)`)
	errHealthCheckFailed = errors.New("connection health check error")
)

// getConnectionFromPool gets a connection and handles client locking internally.
func getConnectionFromPool(c *Client, req *http.Request) (*Connection, error) {
	if c.router != nil {
		// Use request routing
		hop, err := c.router.Route(req.Context(), req)
		return hop.Conn, err
	}

	// Fall back to original connection pool behavior
	c.mu.RLock()
	connectionPool := c.mu.connectionPool
	c.mu.RUnlock()
	conn, err := connectionPool.Next()
	return conn, err
}

// Interface is the HTTP client contract used by the OpenSearch API layer.
type Interface interface {
	Perform(*http.Request) (*http.Response, error)
}

// OpenSearchInfo represents the root endpoint response structure for health checks.
// Non-pointer fields are guaranteed present in all supported OpenSearch versions (>=1.3.0).
// Pointer fields may be missing in certain configurations or versions.
type OpenSearchInfo struct {
	// Permanent fields - guaranteed since OpenSearch 1.3.0
	Name        string `json:"name"`         // Node name
	ClusterName string `json:"cluster_name"` // Cluster name
	ClusterUUID string `json:"cluster_uuid"` // Cluster UUID
	Tagline     string `json:"tagline"`      // "The OpenSearch Project: https://opensearch.org/"
	Version     struct {
		// Permanent fields - guaranteed since OpenSearch 1.3.0
		Number                           string `json:"number"`                              // Version number, e.g. "1.3.0"
		BuildType                        string `json:"build_type"`                          // Build type: "tar", "docker", etc.
		BuildHash                        string `json:"build_hash"`                          // Git commit hash
		BuildDate                        string `json:"build_date"`                          // Build timestamp
		BuildSnapshot                    bool   `json:"build_snapshot"`                      // Is snapshot build
		LuceneVersion                    string `json:"lucene_version"`                      // Underlying Lucene version
		MinimumWireCompatibilityVersion  string `json:"minimum_wire_compatibility_version"`  // Minimum wire protocol version
		MinimumIndexCompatibilityVersion string `json:"minimum_index_compatibility_version"` // Minimum index compatibility version

		// Conditional fields - may be missing in specific configurations
		Distribution *string `json:"distribution,omitempty"` // "opensearch" - missing when compatibility mode enabled in 1.3.x
	} `json:"version"`
}

// Config represents the configuration of HTTP client.
type Config struct {
	URLs     []*url.URL
	Username string
	// Password for HTTP Basic Authentication.
	Password string // #nosec G117

	Header http.Header
	CACert []byte

	// InsecureSkipVerify disables TLS certificate verification.
	// When true, the transport's TLS config is set to skip verification,
	// cloning the existing transport (or http.DefaultTransport) to preserve
	// connection pooling, HTTP/2, and other defaults.
	// This avoids the need to construct a custom http.Transport just for TLS,
	// which is error-prone (bare transports lose DefaultTransport defaults).
	InsecureSkipVerify bool

	Signer signer.Signer

	RetryOnStatus        []int
	DisableRetry         bool
	EnableRetryOnTimeout bool
	MaxRetries           int
	RetryBackoff         func(attempt int) time.Duration

	// RequestTimeout sets an per-attempt timeout for each HTTP round-trip.
	// When set, a context deadline is applied to each individual request attempt
	// (including each retry). This bounds the maximum time a single RoundTrip
	// can block, preventing indefinite hangs on stalled connections.
	// 0 = no per-attempt timeout (default), >0 = explicit timeout.
	RequestTimeout time.Duration

	// DisableResponseBuffering, when true, skips reading and buffering the
	// entire response body in Perform(). The caller receives the raw response
	// body from the underlying RoundTrip and is responsible for fully reading
	// and closing it to release the connection back to the pool.
	//
	// This is useful for proxy and streaming use cases where the caller wants
	// to forward the response incrementally rather than buffering it in memory.
	// With HTTP/2, streams are independent so draining is not required for
	// connection reuse. With HTTP/1.1, the caller must fully read the body
	// before it can be reused.
	//
	// Default: false (existing behavior — body is fully buffered).
	DisableResponseBuffering bool

	CompressRequestBody bool

	EnableMetrics     bool
	EnableDebugLogger bool

	DiscoverNodesInterval time.Duration

	// IncludeDedicatedClusterManagers includes dedicated cluster manager nodes in request routing.
	// When false (default), dedicated cluster manager nodes are excluded from client requests,
	// following best practices and matching the Java client's NodeSelector.SKIP_DEDICATED_CLUSTER_MASTERS behavior.
	// When true, all nodes including dedicated cluster managers can receive client requests.
	// Default: false (excludes dedicated cluster managers for better performance)
	IncludeDedicatedClusterManagers bool

	// DiscoveryHealthCheckRetries sets the number of health check retries during node discovery.
	// During cold start, health checks are performed asynchronously without blocking.
	// During running cluster discovery, health checks are performed with retries before adding nodes.
	// Default: 3
	DiscoveryHealthCheckRetries int

	// HealthCheckTimeout sets the timeout for individual health check requests.
	// 0 = use default (5s), >0 = explicit timeout, <0 = disable timeout
	// Default: 5s
	HealthCheckTimeout time.Duration

	// HealthCheckMaxRetries sets the maximum number of health check retries.
	// 0 = use default (6), >0 = explicit count, <0 = disable retries
	// Default: 6
	HealthCheckMaxRetries int

	// HealthCheckJitter sets the jitter factor for health check retry backoff.
	// 0.0 = use default (0.1), >0.0 = explicit jitter factor, <0.0 = disable jitter
	// Default: 0.1 (10% jitter)
	HealthCheckJitter float64

	// ResurrectTimeoutInitial sets the initial timeout for health check retries on dead connections.
	// Uses exponential backoff: initial * 2^(failures-1), capped at ResurrectTimeoutMax.
	// 0 = use default (5s), >0 = explicit timeout
	// Default: 5s
	ResurrectTimeoutInitial time.Duration

	// ResurrectTimeoutMax caps the base timeout before jitter is applied.
	// The actual wait will be baseTimeout (capped here) + random jitter.
	// 0 = use default (30s), >0 = explicit max
	// Default: 30s
	ResurrectTimeoutMax time.Duration

	// ResurrectTimeoutFactorCutoff sets the exponential backoff cutoff factor for dead connection resurrection.
	// 0 = use default (5), >0 = explicit cutoff factor
	// Default: 5
	ResurrectTimeoutFactorCutoff int

	// MinimumResurrectTimeout sets the minimum time before a dead connection can be resurrected.
	// 0 = use default (500ms), >0 = explicit timeout
	// Default: 500ms
	MinimumResurrectTimeout time.Duration

	// JitterScale controls the jitter multiplier for resurrection timeout calculations.
	// Higher values spread out resurrection attempts more, reducing thundering herd effects.
	// 0.0 = use default (0.5), >0.0 = explicit scale factor
	// Default: 0.5
	JitterScale float64

	// MaxRetryClusterHealth controls how often to retry the cluster health probe
	// (/_cluster/health?local=true) on nodes where it was previously unavailable due to
	// missing cluster:monitor/health permission (401 Unauthorized or 403 Forbidden).
	// Jitter from HealthCheckJitter is applied to the interval to prevent thundering herd
	// when multiple connections were probed at similar times.
	// 0 = disable retries entirely (once unavailable, never retry).
	// <0 = disable cluster health probing entirely.
	// >0 = explicit retry interval.
	// Default: 4h
	MaxRetryClusterHealth time.Duration

	// HealthCheckRequestModifier is called on every health check HTTP request before it is sent.
	// This allows injecting custom authentication headers or other modifications without
	// replacing the entire health check function. Applied inside DefaultHealthCheck.
	// Default: nil (no modification)
	HealthCheckRequestModifier func(*http.Request)

	// Connection pool configuration
	MinHealthyConnections int  // Default: 1, proactively open connections on startup only
	SkipConnectionShuffle bool // Default: false, set true to disable connection randomization

	// ActiveListCap sets the maximum number of connections in the ready list's active partition per pool.
	// When discovery adds connections that would exceed this cap, overflow connections
	// are moved to a standby list for later rotation. This caps the number of active
	// connections per client, preventing fan-out overload in large clusters.
	//
	// 0 = auto-derive from server capacity model:
	//   cap = floor(serverMaxNewConnsPerSec * ResurrectTimeoutInitial / clientsPerServer)
	//   With defaults (8 cores): floor(32 * 5 / 8) = 20
	// >0 = explicit cap.
	// <0 = disabled (all connections go to active, standby disabled).
	//
	// Can be overridden by the OPENSEARCH_GO_ACTIVE_LIST_CAP environment variable.
	// Default: 0 (auto-derive)
	ActiveListCap int

	// StandbyRotationInterval sets how often a standby connection is rotated
	// into the ready list (and an active connection is evicted to standby).
	// Requires ActiveListCap > 0 to have any effect.
	// 0 = use DiscoverNodesInterval, >0 = explicit interval, <0 = disabled.
	//
	// Can be overridden by the OPENSEARCH_GO_STANDBY_ROTATION_INTERVAL environment variable.
	// The env var accepts time.ParseDuration format (e.g., "30s", "1m") or an integer
	// number of seconds.
	// Default: 0 (use DiscoverNodesInterval)
	StandbyRotationInterval time.Duration

	// StandbyRotationCount sets how many standby connections are rotated per
	// discovery cycle. Each rotation health-checks one standby and, if healthy,
	// swaps it with a random active connection.
	// 0 = use default (1), >0 = explicit count.
	//
	// Can be overridden by the OPENSEARCH_GO_STANDBY_ROTATION_COUNT environment variable.
	// Default: 1
	StandbyRotationCount int

	// StandbyPromotionChecks sets the number of consecutive successful health
	// checks required before a standby connection can be promoted to active.
	// This pre-warms the connection before it handles production traffic.
	// 0 = use default (3), >0 = explicit count.
	//
	// Can be overridden by the OPENSEARCH_GO_STANDBY_PROMOTION_CHECKS environment variable.
	// Default: 3
	StandbyPromotionChecks int

	// NodeStatsInterval sets the polling interval for /_nodes/_local/stats/jvm,breaker.
	// A background goroutine polls each ready node's JVM heap usage and circuit breaker
	// metrics to detect overloaded nodes and shed load away from them.
	// 0 = auto-derive from cluster size: clamp(liveNodes * clientsPerServer / healthCheckRate, 5s, 30s),
	//     recalculated on each discovery cycle.
	// >0 = explicit fixed interval, <0 = disabled.
	//
	// Can be overridden by the OPENSEARCH_GO_NODE_STATS_INTERVAL environment variable.
	// The env var accepts time.ParseDuration format (e.g., "30s", "1m") or an integer
	// number of seconds. 0 or unset = use programmatic value or default, <0 = disabled.
	// Default: 0 (auto-derive: 5s for small clusters, up to 30s for large clusters)
	NodeStatsInterval time.Duration

	// OverloadedHeapThreshold sets the JVM heap_used_percent threshold for marking a node
	// as overloaded. When a node's heap usage meets or exceeds this value, the node is
	// demoted from the ready list to the dead list until metrics improve.
	// 0 = use default (85), >0 = explicit threshold (percent, 0-100)
	//
	// Can be overridden by the OPENSEARCH_GO_OVERLOADED_HEAP_THRESHOLD environment variable.
	// Default: 85
	OverloadedHeapThreshold int

	// OverloadedBreakerRatio sets the circuit breaker size ratio threshold for marking a node
	// as overloaded. When any breaker's estimated_size / limit_size meets or exceeds this value,
	// the node is considered overloaded.
	// 0.0 = use default (0.90), >0.0 = explicit ratio (0.0-1.0)
	//
	// Can be overridden by the OPENSEARCH_GO_OVERLOADED_BREAKER_RATIO environment variable.
	// Default: 0.90
	OverloadedBreakerRatio float64

	// Health check function for connection pool health validation.
	// When nil (default), uses the built-in health check that validates OpenSearch
	// nodes with GET / requests. Use NoOpHealthCheck to disable health checking.
	// Returns the HTTP response on success, or an error on failure.
	// A nil response with nil error indicates success (used by NoOpHealthCheck).
	// Callers can extract version info, status codes, or other data from the response.
	HealthCheck HealthCheckFunc

	Transport http.RoundTripper
	Logger    Logger
	Selector  Selector
	Router    Router             // Optional router for cluster-aware request routing
	Observer  ConnectionObserver // Optional observer for connection lifecycle events

	// ShardCostConfig overrides shard cost multipliers used for connection scoring.
	// Passed through to the router via [WithShardCosts] when constructing the
	// default router. Format: bare numeric (sets r:base, the primary read cost
	// at idle) or comma-separated key=value items. Dynamic keys are prefixed
	// with "r:" (r:base, r:amplify, r:exponent) and control the read-primary
	// cost curve. Static keys (unknown, relocating, initializing, replica,
	// write_primary, write_replica) override shard state costs in the lookup
	// tables.
	// Can also be set via OPENSEARCH_GO_SHARD_COST (env var takes precedence).
	ShardCostConfig string

	// Context for background operations (node discovery, health checks, stats polling).
	// If nil, context.Background() is used. The transport derives a child context from
	// this, so canceling the parent automatically stops all background goroutines.
	//nolint:containedctx // Config struct is short-lived, context extracted during New()
	Context context.Context

	ConnectionPoolFunc func([]*Connection, Selector) ConnectionPool
}

// Client represents the HTTP client.
type Client struct {
	urls      []*url.URL
	username  string
	password  string
	header    http.Header
	userAgent string

	signer signer.Signer

	retryOnStatus         []int
	disableRetry          bool
	enableRetryOnTimeout  bool
	maxRetries            int
	retryBackoff          func(attempt int) time.Duration
	requestTimeout        time.Duration
	discoverNodesInterval time.Duration

	includeDedicatedClusterManagers bool
	discoveryHealthCheckRetries     int
	healthCheckTimeout              time.Duration
	healthCheckMaxRetries           int
	healthCheckJitter               float64

	resurrectTimeoutInitial      time.Duration
	resurrectTimeoutMax          time.Duration
	resurrectTimeoutFactorCutoff int
	minimumResurrectTimeout      time.Duration
	rttRingSize                  int
	jitterScale                  float64
	serverMaxNewConnsPerSec      float64
	clientsPerServer             float64
	healthCheckRate              float64 // unified: cores * healthCheckRateMultiplier
	maxRetryClusterHealth        time.Duration
	healthCheckRequestModifier   func(*http.Request)

	// Connection pool configuration
	minHealthyConnections int
	skipConnectionShuffle bool

	// Standby pool configuration
	activeListCap           int  // effective value used at runtime (auto-derived or explicit)
	activeListCapConfig     *int // nil = auto-scale with cluster size; non-nil = user-specified value
	standbyRotationInterval time.Duration
	standbyRotationCount    int
	standbyPromotionChecks  int64

	// Node stats and load shedding
	nodeStatsInterval       time.Duration
	nodeStatsIntervalAuto   bool // true when auto-derived from cluster size (recalculated on each tick)
	overloadedHeapThreshold int
	overloadedBreakerRatio  float64

	// Pool congestion quorum: true once at least one connection has reported
	// thread pool configurations. Before quorum, loadCwnd returns a synthetic
	// ceiling based on allocatedProcessors.
	poolInfoReady atomic.Bool
	poolInfoCount atomic.Int32 // connections with known thread pool configs

	healthCheck HealthCheckFunc

	compressRequestBody      bool
	disableResponseBuffering bool
	pooledGzipCompressor     *gzipCompressor

	metrics *metrics

	transport http.RoundTripper
	logger    Logger
	selector  Selector
	router    Router // Optional router for cluster-aware routing
	observer  atomic.Pointer[ConnectionObserver]
	poolFunc  func([]*Connection, Selector) ConnectionPool

	// Seed URL fallback: last-resort pool built from the original seed URLs.
	// Used when all router policies and connection pools are exhausted.
	// Disabled when OPENSEARCH_GO_FALLBACK=false.
	seedFallbackPool     *multiServerPool
	seedFallbackDisabled bool // true when OPENSEARCH_GO_FALLBACK=false

	// Context for background operations like node discovery.
	// This context is created during client initialization and manages the lifecycle
	// of background goroutines (e.g., periodic node discovery).
	//nolint:containedctx // Long-lived context required for background worker lifecycle
	ctx        context.Context
	cancelFunc context.CancelFunc

	// catRefreshNeeded is set by the request path when a connection
	// error suggests shard placement data may be stale. The discovery
	// loop picks up the flag on its next wake.
	catRefreshNeeded atomic.Bool

	// discoveryFeatures controls which server calls are made during
	// the discovery cycle. Parsed from OPENSEARCH_GO_DISCOVERY_CONFIG
	// at client init time and immutable after.
	discoveryFeatures discoveryFeatures

	// discoveryNeeded is set when a large topology change requires
	// an immediate full node + catalog discovery (e.g., after a
	// cluster membership change detected by the request path).
	discoveryNeeded atomic.Bool

	mu struct {
		sync.RWMutex
		connectionPool      ConnectionPool // Used for both single-node and multi-node
		discoveryInProgress bool           // Prevents concurrent discovery operations
	}
}

// New creates new transport client.
//
// http.DefaultTransport will be used if no transport is passed in the configuration.
func New(cfg Config) (*Client, error) {
	if cfg.Transport == nil {
		cfg.Transport = http.DefaultTransport
	}

	if cfg.CACert != nil {
		httpTransport, ok := cfg.Transport.(*http.Transport)
		if !ok {
			return nil, fmt.Errorf("unable to set CA certificate for transport of type %T", cfg.Transport)
		}

		httpTransport = httpTransport.Clone()
		httpTransport.TLSClientConfig.RootCAs = x509.NewCertPool()

		if ok := httpTransport.TLSClientConfig.RootCAs.AppendCertsFromPEM(cfg.CACert); !ok {
			return nil, errors.New("unable to add CA certificate")
		}

		cfg.Transport = httpTransport
	}

	if cfg.InsecureSkipVerify {
		httpTransport, ok := cfg.Transport.(*http.Transport)
		if !ok {
			return nil, fmt.Errorf("unable to set InsecureSkipVerify for transport of type %T", cfg.Transport)
		}

		httpTransport = httpTransport.Clone()
		if httpTransport.TLSClientConfig == nil {
			httpTransport.TLSClientConfig = &tls.Config{}
		}
		httpTransport.TLSClientConfig.InsecureSkipVerify = true

		cfg.Transport = httpTransport
	}

	if len(cfg.RetryOnStatus) == 0 && cfg.RetryOnStatus == nil {
		cfg.RetryOnStatus = []int{502, 503, 504}
	}

	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = defaultMaxRetries
	}

	if cfg.DiscoveryHealthCheckRetries == 0 {
		cfg.DiscoveryHealthCheckRetries = 3
	}

	// RequestTimeout: 0 = no per-attempt timeout (default), >0 = explicit.
	// OPENSEARCH_GO_REQUEST_TIMEOUT: time.ParseDuration format, integer seconds, or float seconds.
	requestTimeout := cfg.RequestTimeout
	if envVal, ok := os.LookupEnv("OPENSEARCH_GO_REQUEST_TIMEOUT"); ok && envVal != "" {
		if d, ok := parseDuration(envVal); ok {
			requestTimeout = d
		}
	}

	// Set health check defaults using the 0=default, <0=disable, >0=explicit pattern
	var healthCheckTimeout time.Duration
	var healthCheckMaxRetries int
	var healthCheckJitter float64

	switch {
	case cfg.HealthCheckTimeout == 0:
		healthCheckTimeout = defaultHealthCheckTimeout
	case cfg.HealthCheckTimeout < 0:
		healthCheckTimeout = 0
	default:
		healthCheckTimeout = cfg.HealthCheckTimeout
	}

	switch {
	case cfg.HealthCheckMaxRetries == 0:
		healthCheckMaxRetries = defaultMaxRetries
	case cfg.HealthCheckMaxRetries < 0:
		healthCheckMaxRetries = 0
	default:
		healthCheckMaxRetries = cfg.HealthCheckMaxRetries
	}

	switch {
	case cfg.HealthCheckJitter == 0.0:
		healthCheckJitter = defaultRetryJitter
	case cfg.HealthCheckJitter < 0.0:
		healthCheckJitter = 0.0
	default:
		healthCheckJitter = cfg.HealthCheckJitter
	}

	// Set resurrection timeout defaults using the 0=default, >0=explicit pattern
	var resurrectTimeoutInitial time.Duration
	var resurrectTimeoutMax time.Duration
	var resurrectTimeoutFactorCutoff int
	var minimumResurrectTimeout time.Duration
	var jitterScale float64

	if cfg.ResurrectTimeoutInitial == 0 {
		resurrectTimeoutInitial = defaultResurrectTimeoutInitial
	} else {
		resurrectTimeoutInitial = cfg.ResurrectTimeoutInitial
	}

	if cfg.ResurrectTimeoutMax == 0 {
		resurrectTimeoutMax = defaultResurrectTimeoutMax
	} else {
		resurrectTimeoutMax = cfg.ResurrectTimeoutMax
	}

	if cfg.ResurrectTimeoutFactorCutoff == 0 {
		resurrectTimeoutFactorCutoff = defaultResurrectTimeoutFactorCutoff
	} else {
		resurrectTimeoutFactorCutoff = cfg.ResurrectTimeoutFactorCutoff
	}

	if cfg.MinimumResurrectTimeout == 0 {
		minimumResurrectTimeout = defaultMinimumResurrectTimeout
	} else {
		minimumResurrectTimeout = cfg.MinimumResurrectTimeout
	}

	if cfg.JitterScale == 0.0 {
		jitterScale = defaultJitterScale
	} else {
		jitterScale = cfg.JitterScale
	}

	// Derive capacity model from defaultServerCoreCount.
	// Auto-discovery will update these when /_nodes/_local/http,os returns allocated_processors.
	serverCoreCount := defaultServerCoreCount
	serverMaxNewConnsPerSec := float64(serverCoreCount) * serverMaxNewConnsPerSecMultiplier
	clientsPerServer := float64(serverCoreCount)
	healthCheckRate := float64(serverCoreCount) * healthCheckRateMultiplier

	// Set cluster health retry default
	var maxRetryClusterHealth time.Duration
	switch {
	case cfg.MaxRetryClusterHealth == 0:
		maxRetryClusterHealth = defaultMaxRetryClusterHealth
	case cfg.MaxRetryClusterHealth < 0:
		maxRetryClusterHealth = 0
	default:
		maxRetryClusterHealth = cfg.MaxRetryClusterHealth
	}

	if cfg.MinHealthyConnections == 0 {
		cfg.MinHealthyConnections = 1
	}

	// Node stats / load shedding defaults
	//
	// Resolution order for each setting:
	//   1. Environment variable (operator override)
	//   2. Config struct value (programmatic)
	//   3. Built-in default constant
	//
	// NodeStatsInterval: 0 = auto-derive from cluster size, >0 = explicit, <0 = disabled.
	// OPENSEARCH_GO_NODE_STATS_INTERVAL: time.ParseDuration format or integer seconds.
	// 0 or unset = no override, <0 = disabled.
	nodeStatsInterval := cfg.NodeStatsInterval
	nodeStatsIntervalAuto := false
	if envVal, ok := os.LookupEnv("OPENSEARCH_GO_NODE_STATS_INTERVAL"); ok && envVal != "" {
		if d, ok := parseDuration(envVal); ok {
			nodeStatsInterval = d
		}
	}
	switch {
	case nodeStatsInterval == 0:
		// Auto-derive initial value from cluster size; scheduleNodeStats will recalculate.
		nodeStatsInterval = defaultNodeStatsIntervalMin
		nodeStatsIntervalAuto = true
	case nodeStatsInterval < 0:
		nodeStatsInterval = 0 // disabled
	}

	// OverloadedHeapThreshold: 0 = use default, >0 = explicit.
	// OPENSEARCH_GO_OVERLOADED_HEAP_THRESHOLD: integer 0-100.
	overloadedHeapThreshold := cfg.OverloadedHeapThreshold
	if envVal, ok := os.LookupEnv("OPENSEARCH_GO_OVERLOADED_HEAP_THRESHOLD"); ok && envVal != "" {
		if v, err := strconv.Atoi(envVal); err == nil {
			overloadedHeapThreshold = v
		}
	}
	if overloadedHeapThreshold == 0 {
		overloadedHeapThreshold = defaultOverloadedHeapThreshold
	}

	// OverloadedBreakerRatio: 0.0 = use default, >0.0 = explicit.
	// OPENSEARCH_GO_OVERLOADED_BREAKER_RATIO: float 0.0-1.0.
	overloadedBreakerRatio := cfg.OverloadedBreakerRatio
	if envVal, ok := os.LookupEnv("OPENSEARCH_GO_OVERLOADED_BREAKER_RATIO"); ok && envVal != "" {
		if v, err := strconv.ParseFloat(envVal, 64); err == nil && v > 0.0 && v <= 1.0 {
			overloadedBreakerRatio = v
		}
	}
	if overloadedBreakerRatio == 0.0 {
		overloadedBreakerRatio = defaultOverloadedBreakerRatio
	}

	// Standby pool configuration
	//
	// Resolution order for each setting:
	//   1. Environment variable (operator override)
	//   2. Config struct value (programmatic)
	//   3. Built-in default constant
	//
	// ActiveListCap: 0 = auto-derive, >0 = explicit cap, <0 = disabled.
	// OPENSEARCH_GO_ACTIVE_LIST_CAP: integer.
	activeListCap := cfg.ActiveListCap
	if envVal, ok := os.LookupEnv("OPENSEARCH_GO_ACTIVE_LIST_CAP"); ok && envVal != "" {
		if v, err := strconv.Atoi(envVal); err == nil {
			activeListCap = v
		}
	}

	// Resolve activeListCapConfig: nil means auto-scale, non-nil means user-specified.
	var activeListCapConfig *int

	switch {
	case activeListCap == 0:
		// Auto-derive initial value from server capacity model. activeListCapConfig
		// stays nil so discovery can recalculate as the cluster resizes.
		if clientsPerServer > 0 {
			derived := serverMaxNewConnsPerSec * resurrectTimeoutInitial.Seconds() / clientsPerServer
			activeListCap = int(derived)
		}
	case activeListCap < 0:
		// Explicitly disabled -- store the resolved zero to prevent auto-scaling.
		disabled := 0
		activeListCapConfig = &disabled
		activeListCap = 0
	default:
		// Explicit positive value -- store it.
		explicit := activeListCap
		activeListCapConfig = &explicit
	}

	// StandbyRotationInterval: 0 = use DiscoverNodesInterval, >0 = explicit, <0 = disabled.
	// OPENSEARCH_GO_STANDBY_ROTATION_INTERVAL: time.ParseDuration format or integer seconds.
	standbyRotationInterval := cfg.StandbyRotationInterval
	if envVal, ok := os.LookupEnv("OPENSEARCH_GO_STANDBY_ROTATION_INTERVAL"); ok && envVal != "" {
		if d, ok := parseDuration(envVal); ok {
			standbyRotationInterval = d
		}
	}

	// StandbyRotationCount: 0 = use default (1), >0 = explicit.
	// OPENSEARCH_GO_STANDBY_ROTATION_COUNT: integer.
	standbyRotationCount := cfg.StandbyRotationCount
	if envVal, ok := os.LookupEnv("OPENSEARCH_GO_STANDBY_ROTATION_COUNT"); ok && envVal != "" {
		if v, err := strconv.Atoi(envVal); err == nil && v > 0 {
			standbyRotationCount = v
		}
	}
	if standbyRotationCount == 0 {
		standbyRotationCount = defaultStandbyRotationCount
	}

	// StandbyPromotionChecks: 0 = use default, >0 = explicit.
	// OPENSEARCH_GO_STANDBY_PROMOTION_CHECKS: integer.
	standbyPromotionChecks := int64(cfg.StandbyPromotionChecks)
	if envVal, ok := os.LookupEnv("OPENSEARCH_GO_STANDBY_PROMOTION_CHECKS"); ok && envVal != "" {
		if v, err := strconv.Atoi(envVal); err == nil && v > 0 {
			standbyPromotionChecks = int64(v)
		}
	}
	if standbyPromotionChecks == 0 {
		standbyPromotionChecks = defaultStandbyPromotionChecks
	}

	conns := make([]*Connection, len(cfg.URLs))
	for idx, u := range cfg.URLs {
		conn := &Connection{URL: u, URLString: u.String()}
		conn.estLoad.clock = realClock{}
		conn.weight.Store(1)
		conns[idx] = conn
	}

	// Always derive a child context so that:
	// 1. Close() can cancel pollers via cancelFunc regardless of what the user passed
	// 2. If the user's parent context is cancelled (e.g. t.Context() in tests),
	//    the child is automatically cancelled, stopping all background goroutines
	parent := cfg.Context
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)

	client := Client{
		urls:     cfg.URLs,
		username: cfg.Username,
		password: cfg.Password,
		header:   cfg.Header,

		signer: cfg.Signer,

		retryOnStatus:         cfg.RetryOnStatus,
		disableRetry:          cfg.DisableRetry,
		enableRetryOnTimeout:  cfg.EnableRetryOnTimeout,
		maxRetries:            cfg.MaxRetries,
		retryBackoff:          cfg.RetryBackoff,
		requestTimeout:        requestTimeout,
		discoverNodesInterval: cfg.DiscoverNodesInterval,

		includeDedicatedClusterManagers: cfg.IncludeDedicatedClusterManagers,
		discoveryHealthCheckRetries:     cfg.DiscoveryHealthCheckRetries,
		healthCheckTimeout:              healthCheckTimeout,
		healthCheckMaxRetries:           healthCheckMaxRetries,
		healthCheckJitter:               healthCheckJitter,

		resurrectTimeoutInitial:      resurrectTimeoutInitial,
		resurrectTimeoutMax:          resurrectTimeoutMax,
		resurrectTimeoutFactorCutoff: resurrectTimeoutFactorCutoff,
		minimumResurrectTimeout:      minimumResurrectTimeout,
		rttRingSize:                  rttRingSizeFor(cfg.DiscoverNodesInterval, resurrectTimeoutInitial),
		jitterScale:                  jitterScale,
		serverMaxNewConnsPerSec:      serverMaxNewConnsPerSec,
		clientsPerServer:             clientsPerServer,
		healthCheckRate:              healthCheckRate,
		maxRetryClusterHealth:        maxRetryClusterHealth,
		healthCheckRequestModifier:   cfg.HealthCheckRequestModifier,

		// Connection pool configuration
		minHealthyConnections: cfg.MinHealthyConnections,
		skipConnectionShuffle: cfg.SkipConnectionShuffle,

		// Standby pool configuration
		activeListCap:           activeListCap,
		activeListCapConfig:     activeListCapConfig,
		standbyRotationInterval: standbyRotationInterval,
		standbyRotationCount:    standbyRotationCount,
		standbyPromotionChecks:  standbyPromotionChecks,

		// Node stats and load shedding
		nodeStatsInterval:       nodeStatsInterval,
		nodeStatsIntervalAuto:   nodeStatsIntervalAuto,
		overloadedHeapThreshold: overloadedHeapThreshold,
		overloadedBreakerRatio:  overloadedBreakerRatio,

		compressRequestBody:      cfg.CompressRequestBody,
		disableResponseBuffering: cfg.DisableResponseBuffering,

		transport:  cfg.Transport,
		logger:     cfg.Logger,
		router:     cfg.Router,
		selector:   cfg.Selector,
		poolFunc:   cfg.ConnectionPoolFunc,
		ctx:        ctx,
		cancelFunc: cancel,
	}

	client.userAgent = initUserAgent()

	// Parse discovery feature config from environment variable.
	// Controls which server calls are made during the discovery cycle.
	if val, ok := os.LookupEnv(envDiscoveryConfig); ok && val != "" {
		client.discoveryFeatures = parseDiscoveryConfig(val)
	}

	// Parse seed URL fallback config. Default: enabled (zero-value).
	if val, ok := os.LookupEnv(envFallbackConfig); ok && val != "" {
		if b, err := strconv.ParseBool(val); err == nil && !b {
			client.seedFallbackDisabled = true
		}
	}

	// Build the seed URL fallback pool eagerly. This pool holds fresh copies
	// of the original seed URLs and is never modified by DiscoveryUpdate.
	// It serves as the absolute last resort when all router policies and
	// connection pools are exhausted.
	if !client.seedFallbackDisabled && len(cfg.URLs) > 0 {
		seedConns := make([]*Connection, len(cfg.URLs))
		for i, u := range cfg.URLs {
			conn := &Connection{URL: u, URLString: u.String()}
			conn.estLoad.clock = realClock{}
			conn.weight.Store(1)
			conn.mu.Lock()
			conn.casLifecycle(conn.loadConnState(), 0, lcActive, lcUnknown|lcStandby) //nolint:errcheck // lock held; only errLifecycleNoop possible
			conn.mu.Unlock()
			seedConns[i] = conn
		}

		seedPool := &multiServerPool{
			name:                         "seed-fallback",
			ctx:                          ctx,
			resurrectTimeoutInitial:      resurrectTimeoutInitial,
			resurrectTimeoutMax:          resurrectTimeoutMax,
			resurrectTimeoutFactorCutoff: resurrectTimeoutFactorCutoff,
			minimumResurrectTimeout:      minimumResurrectTimeout,
			jitterScale:                  jitterScale,
			serverMaxNewConnsPerSec:      serverMaxNewConnsPerSec,
			clientsPerServer:             clientsPerServer,
		}
		seedPool.mu.ready = seedConns
		seedPool.mu.activeCount = len(seedConns)
		seedPool.mu.dead = []*Connection{}
		seedPool.mu.members = make(map[*Connection]struct{}, len(seedConns))
		for _, conn := range seedConns {
			seedPool.mu.members[conn] = struct{}{}
		}
		client.seedFallbackPool = seedPool
	}

	// Store observer for connection lifecycle notifications
	if cfg.Observer != nil {
		client.observer.Store(&cfg.Observer)
	}

	// Set health check function - use configured one or default to built-in health check
	if cfg.HealthCheck != nil {
		client.healthCheck = cfg.HealthCheck
	} else {
		client.healthCheck = client.DefaultHealthCheck
	}

	// Shuffle connections for load distribution unless disabled
	if !client.skipConnectionShuffle && len(conns) > 1 {
		rand.Shuffle(len(conns), func(i, j int) {
			conns[i], conns[j] = conns[j], conns[i]
		})
	}

	if client.poolFunc != nil {
		client.mu.connectionPool = client.poolFunc(conns, cfg.Selector)
	} else {
		// Use client-configured timeout settings for the main connection pool
		if len(conns) == 1 {
			client.mu.connectionPool = &singleServerPool{connection: conns[0]}
		} else {
			pool := &multiServerPool{
				name:                         "client",
				ctx:                          ctx,
				resurrectTimeoutInitial:      resurrectTimeoutInitial,
				resurrectTimeoutMax:          resurrectTimeoutMax,
				resurrectTimeoutFactorCutoff: resurrectTimeoutFactorCutoff,
				minimumResurrectTimeout:      minimumResurrectTimeout,
				jitterScale:                  jitterScale,
				serverMaxNewConnsPerSec:      serverMaxNewConnsPerSec,
				clientsPerServer:             clientsPerServer,
				activeListCap:                activeListCap,
				activeListCapConfig:          activeListCapConfig,
				standbyPromotionChecks:       standbyPromotionChecks,
			}
			// Initialize all connections as active with proper state.
			for _, conn := range conns {
				conn.mu.Lock()
				conn.casLifecycle(conn.loadConnState(), 0, lcActive, lcUnknown|lcStandby) //nolint:errcheck // lock held; only errLifecycleNoop possible
				conn.mu.Unlock()
			}
			pool.mu.ready = conns
			pool.mu.activeCount = len(conns)
			pool.mu.dead = []*Connection{}
			pool.mu.members = make(map[*Connection]struct{}, max(len(conns), defaultMembersCapacity))
			for _, conn := range conns {
				pool.mu.members[conn] = struct{}{}
			}

			// Enforce the active list cap: moves overflow connections to standby.
			pool.enforceActiveCapWithLock()

			client.mu.connectionPool = pool
		}
	}

	// Set up health check function for pools that support it
	if pool, ok := client.mu.connectionPool.(*multiServerPool); ok {
		pool.healthCheck = client.healthCheck
		if obs := client.observer.Load(); obs != nil {
			pool.observer.Store(obs)
		}
	}

	// Set health check on the seed fallback pool so resurrection works.
	if client.seedFallbackPool != nil {
		client.seedFallbackPool.healthCheck = client.healthCheck
	}

	if cfg.EnableDebugLogger {
		storeDebugLogger(&debuggingLogger{Output: os.Stdout})
	}

	if cfg.EnableMetrics {
		client.metrics = &metrics{}
		client.metrics.mu.responses = make(map[int]int)

		if len(conns) == 1 {
			// Single node - assign metrics to connection pool
			if pool, ok := client.mu.connectionPool.(*singleServerPool); ok {
				pool.metrics = client.metrics
			} else {
				return nil, fmt.Errorf("unexpected connection pool type for single node: %T", client.mu.connectionPool)
			}
		} else {
			// Multi-node - assign metrics to status connection pool
			if pool, ok := client.mu.connectionPool.(*multiServerPool); ok {
				pool.metrics = client.metrics
			}
		}
	}

	if client.discoverNodesInterval > 0 {
		go client.discoveryLoop()
	}

	if cfg.CompressRequestBody {
		client.pooledGzipCompressor = newGzipCompressor()
	}

	// Configure policy settings for all policies in the router
	if client.router != nil {
		config := policyConfig{
			ctx:                          client.ctx,
			resurrectTimeoutInitial:      client.resurrectTimeoutInitial,
			resurrectTimeoutMax:          client.resurrectTimeoutMax,
			resurrectTimeoutFactorCutoff: client.resurrectTimeoutFactorCutoff,
			minimumResurrectTimeout:      client.minimumResurrectTimeout,
			jitterScale:                  client.jitterScale,
			serverMaxNewConnsPerSec:      client.serverMaxNewConnsPerSec,
			clientsPerServer:             client.clientsPerServer,
			healthCheck:                  client.healthCheck,
			observer:                     client.observer.Load(),
			poolInfoReady:                &client.poolInfoReady,
			activeListCap:                client.activeListCapConfig,
			standbyPromotionChecks:       client.standbyPromotionChecks,
		}
		// Use type assertion to check if the router (which is a Policy) implements policyConfigurable
		if configurablePolicy, ok := client.router.(policyConfigurable); ok {
			if err := configurablePolicy.configurePolicySettings(config); err != nil {
				return nil, fmt.Errorf("failed to configure policy settings: %w", err)
			}
		}

		// Apply environment variable overrides (OPENSEARCH_GO_POLICY_*) before
		// the first DiscoveryUpdate so that force-disabled policies never
		// accumulate connections.
		if routerPolicy, ok := client.router.(Policy); ok {
			if overrides := parsePolicyOverrides(); len(overrides) > 0 {
				applyPolicyOverrides(routerPolicy, overrides)
			}
		}

		// Initialize router with seed URLs immediately (without blocking)
		// Add all connections to the router first - they'll start in dead/zombie state
		if err := client.router.DiscoveryUpdate(conns, nil, nil); err != nil {
			return nil, fmt.Errorf("failed to initialize router with seed connections: %w", err)
		}

		// Launch async health checks on seed URLs in parallel
		// First successful connection will allow requests immediately
		// Discovery will be handled by DiscoverNodesOnStart if configured
		go func() {
			ctx, cancel := context.WithTimeout(client.ctx, healthCheckTimeout)
			defer cancel()

			// Use errgroup to manage parallel health checks
			// First success cancels the context, stopping other checks
			g, gctx := errgroup.WithContext(ctx)

			for _, conn := range conns {
				g.Go(func() error {
					// Health check the connection
					if err := conn.healthCheck(gctx, client.healthCheck); err != nil {
						return err // Health check failed
					}

					// Health check succeeded - mark as healthy and cancel others
					// DiscoverNodesOnStart will handle discovery if configured
					client.router.OnSuccess(conn)
					cancel()
					return nil
				})
			}

			// Wait for first success (nil error) or all to fail
			g.Wait() //nolint:errcheck // best-effort parallel health check; individual errors handled per-goroutine
		}()
	}

	// Start node stats poller for load shedding if configured.
	// Gated by OPENSEARCH_GO_DISCOVERY_CONFIG=-node_stats.
	if client.nodeStatsInterval > 0 && client.discoveryFeatures.nodeStatsEnabled() {
		client.scheduleNodeStats()
	}

	// Start periodic cluster health refresh for ready connections if configured
	if client.healthCheckRate > 0 {
		client.scheduleClusterHealthRefresh()
	}

	return &client, nil
}

// Close cancels background operations and cleans up resources.
// The discovery loop goroutine exits when the context is cancelled.
//
//nolint:unparam // Returns error to satisfy io.Closer interface; CloseIdleConnections() is void
func (c *Client) Close() error {
	if c.cancelFunc != nil {
		c.cancelFunc()
	}

	// Close idle connections if the transport supports it
	if transport, ok := c.transport.(interface{ CloseIdleConnections() }); ok {
		transport.CloseIdleConnections()
	}

	return nil
}

// Perform executes the request and returns a response or error.
func (c *Client) Perform(req *http.Request) (*http.Response, error) {
	var (
		res *http.Response
		err error
	)

	// Record metrics, when enabled
	if c.metrics != nil {
		c.metrics.requests.Add(1)
	}

	// Update request
	c.setReqUserAgent(req)
	c.setReqGlobalHeader(req)

	if req.Body != nil && req.Body != http.NoBody {
		if c.compressRequestBody {
			buf, err := c.pooledGzipCompressor.compress(req.Body)
			defer c.pooledGzipCompressor.collectBuffer(buf)
			if err != nil {
				return nil, fmt.Errorf("failed to compress request body: %w", err)
			}

			req.GetBody = func() (io.ReadCloser, error) {
				// A new reader must be returned each time so that retries don't read from an already-consumed body.
				reader := bytes.NewReader(buf.Bytes())
				return io.NopCloser(reader), nil
			}
			//nolint:errcheck // error is always nil
			req.Body, _ = req.GetBody()

			req.Header.Set("Content-Encoding", "gzip")
			req.ContentLength = int64(buf.Len())
		} else if req.GetBody == nil {
			if !c.disableRetry || (c.logger != nil && c.logger.RequestBodyEnabled()) {
				var buf bytes.Buffer
				//nolint:errcheck // ignored as this is only for logging
				buf.ReadFrom(req.Body)
				req.GetBody = func() (io.ReadCloser, error) {
					// Return a new reader each time
					reader := bytes.NewReader(buf.Bytes())
					return io.NopCloser(reader), nil
				}
				//nolint:errcheck // error is always nil
				req.Body, _ = req.GetBody()
			}
		}
	}

	for i := 0; i <= c.maxRetries; i++ {
		var (
			conn            *Connection
			poolName        string
			shouldRetry     bool
			shouldCloseBody bool
		)

		if c.router != nil {
			var hop NextHop
			hop, err = c.router.Route(req.Context(), req)
			conn = hop.Conn
			poolName = hop.PoolName
		} else {
			c.mu.RLock()
			pool := c.mu.connectionPool
			c.mu.RUnlock()
			conn, err = pool.Next()
		}
		if err != nil {
			if c.logger != nil {
				c.logRoundTrip(req, nil, err, time.Time{}, time.Duration(0))
			}
			// Wrap the error for context. If all pools are exhausted
			// (ErrNoConnections), break to allow post-loop seed fallback.
			err = fmt.Errorf("cannot get connection: %w", err)
			if errors.Is(err, ErrNoConnections) {
				break
			}
			return nil, err
		}

		// Update request
		c.setReqURL(conn.URL, req)
		c.setReqAuth(conn.URL, req)

		if !c.disableRetry && i > 0 && req.Body != nil && req.Body != http.NoBody {
			body, err := req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("cannot get request body: %w", err)
			}
			req.Body = body
		}

		if err = c.signRequest(req); err != nil {
			return nil, fmt.Errorf("failed to sign request: %w", err)
		}

		// Set up time measures and execute the request
		if poolName != "" {
			conn.addInFlight(poolName)
		}
		start := time.Now().UTC()

		// Apply per-attempt timeout if configured. This creates a child context
		// with a deadline so that each individual RoundTrip is bounded, preventing
		// indefinite hangs on stalled TCP connections.
		attemptReq := req
		var attemptCancel context.CancelFunc
		if c.requestTimeout > 0 {
			var attemptCtx context.Context
			attemptCtx, attemptCancel = context.WithTimeout(req.Context(), c.requestTimeout)
			attemptReq = req.WithContext(attemptCtx)
		}

		res, err = c.transport.RoundTrip(attemptReq)

		if attemptCancel != nil {
			// If the response body is non-nil, the caller is responsible for
			// reading and closing it. Cancel the attempt context only after
			// the body is fully consumed or on error. For now, cancel
			// immediately — http.Transport will not abort an in-progress
			// body read on a completed RoundTrip when the context expires.
			// The timeout bounds the headers-received phase; body reads
			// proceed independently.
			if err != nil || res == nil {
				attemptCancel()
			} else {
				// Wrap the response body so the attempt context is cancelled
				// when the caller closes the body.
				res.Body = &cancelOnCloseBody{ReadCloser: res.Body, cancel: attemptCancel}
			}
		}
		dur := time.Since(start)
		if poolName != "" {
			conn.releaseInFlight(poolName)
		}

		// Log request and response
		if c.logger != nil {
			if c.logger.RequestBodyEnabled() && req.Body != nil && req.Body != http.NoBody {
				//nolint:errcheck // ignored as this is only for logging
				req.Body, _ = req.GetBody()
			}
			c.logRoundTrip(req, res, err, start, dur)
		}

		if err != nil {
			// Record metrics, when enabled
			if c.metrics != nil {
				c.metrics.failures.Add(1)
			}

			if dl := loadDebugLogger(); dl != nil {
				dl.Logf("Request to %s failed: %v\n", conn.URL, err)
			}

			// Retry on HTTP/2 stream resets (RST_STREAM frames such as REFUSED_STREAM).
			// Go 1.21+ added As bridging on the vendored internal http2.StreamError
			// (h2_error.go), which matches target structs by field name and type
			// convertibility. The local h2StreamError has the same layout, so
			// errors.As succeeds without importing x/net/http2.
			//
			// Note: GOAWAY is handled transparently by Go's HTTP/2 transport, which
			// retries affected requests on a new connection. Stream resets (caught here)
			// are a separate signal indicating the server rejected individual streams.
			var streamErr h2StreamError
			if errors.As(err, &streamErr) {
				if dl := loadDebugLogger(); dl != nil {
					dl.Logf("HTTP/2 stream error from %s: StreamID=%d, Code=%d\n",
						conn.URL, streamErr.StreamID, streamErr.Code)
				}
				// Mark draining so OnSuccess from concurrent requests won't resurrect
				// this connection. Requires defaultDrainingQuiescingChecks consecutive
				// successful health checks before resurrection.
				conn.drainingQuiescingRemaining.Store(defaultDrainingQuiescingChecks)
				shouldRetry = true
			}

			// Report the connection as unsuccessful. This is ordered after the
			// h2StreamError check above so that drainingQuiescingRemaining is set
			// before OnFailure schedules resurrection.
			if c.router != nil {
				if poolErr := c.router.OnFailure(conn); poolErr != nil {
					if dl := loadDebugLogger(); dl != nil {
						dl.Logf("Router error marking connection as failed: %v\n", poolErr)
					}
				}
			} else {
				c.mu.Lock()
				if poolErr := c.mu.connectionPool.OnFailure(conn); poolErr != nil {
					if dl := loadDebugLogger(); dl != nil {
						dl.Logf("Connection pool error marking connection as failed: %v\n", poolErr)
					}
				}
				c.mu.Unlock()
			}

			// Mark connection as needing shard placement confirmation.
			// The node stays out of scored routing candidate sets until
			// /_cat/shards refresh confirms current shard-to-node mappings.
			if conn.setNeedsCatUpdate() == nil {
				if obs := observerFromAtomic(&c.observer); obs != nil {
					obs.OnShardMapInvalidation(ShardMapInvalidationEvent{
						ConnURL: conn.URLString, ConnName: conn.Name,
						Reason: "transport_error", Timestamp: time.Now().UTC(),
					})
				}
			}
			c.requestCatRefresh()

			// Retry on EOF errors (connection closed by peer)
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				shouldRetry = true
			}

			// Retry on network errors, but not on timeout errors, unless configured
			var netError net.Error
			if errors.As(err, &netError) {
				if (!netError.Timeout() || c.enableRetryOnTimeout) && !c.disableRetry {
					shouldRetry = true
				}
			}
		} else {
			// Report the connection as successful
			if c.router != nil {
				c.router.OnSuccess(conn)
			} else {
				c.mu.Lock()
				c.mu.connectionPool.OnSuccess(conn)
				c.mu.Unlock()
			}

			// When the server signals it will close the connection (Connection: close header
			// or HTTP/1.0 without keep-alive), proactively verify the node is still healthy.
			// This detects graceful shutdowns before the next request fails on a dead connection.
			// Go's net/http strips the Connection hop-by-hop header and sets res.Close instead.
			if res.Close {
				c.scheduleProactiveHealthCheck(conn)
			}
		}

		if res != nil && c.metrics != nil {
			c.metrics.incrementResponse(res.StatusCode)
		}

		// Retry on configured response statuses
		if res != nil && !c.disableRetry {
			for _, code := range c.retryOnStatus {
				if res.StatusCode == code {
					shouldRetry = true
					shouldCloseBody = true
				}
			}
			// Retryable HTTP status (typically 502/503/504) suggests the
			// target node is unhealthy -- flag it for shard placement refresh.
			if shouldCloseBody {
				if conn.setNeedsCatUpdate() == nil {
					if obs := observerFromAtomic(&c.observer); obs != nil {
						obs.OnShardMapInvalidation(ShardMapInvalidationEvent{
							ConnURL: conn.URLString, ConnName: conn.Name,
							Reason: "http_status_retry", Timestamp: time.Now().UTC(),
						})
					}
				}
				c.requestCatRefresh()
			}
		}

		// HTTP 429 (Too Many Requests): mark the pool overloaded and halve
		// cwnd so subsequent requests route to less-loaded nodes. The
		// overloaded flag is only cleared by the stats poller when
		// delta(rejected) == 0.
		if res != nil && res.StatusCode == http.StatusTooManyRequests && poolName != "" {
			if pc := conn.pools.get(poolName); pc != nil {
				if pc.mu.TryLock() {
					if !pc.overloaded.Load() {
						pc.overloaded.Store(true)
						newCwnd := max(pc.cwnd.Load()/2, 1)
						pc.cwnd.Store(newCwnd)
						pc.mu.ssthresh = newCwnd
					}
					pc.mu.Unlock()
				}
			}
			if !c.disableRetry {
				shouldRetry = true
				shouldCloseBody = true
			}
		}

		// Break if retry should not be performed
		if !shouldRetry {
			break
		}

		// Drain and close body when retrying after response
		if shouldCloseBody && i < c.maxRetries {
			if res.Body != nil {
				//nolint:errcheck // unexpected but okay if it fails
				io.Copy(io.Discard, res.Body)
				res.Body.Close()
			}
		}

		// Delay the retry if a backoff function is configured
		if c.retryBackoff != nil && i < c.maxRetries {
			var cancelled bool
			timer := time.NewTimer(c.retryBackoff(i + 1))
			select {
			case <-req.Context().Done():
				timer.Stop()
				err = req.Context().Err()
				cancelled = true
			case <-timer.C:
			}
			if cancelled {
				break
			}
		}
	}

	// Seed URL fallback: absolute last resort when the entire retry loop
	// failed to obtain a connection from any router policy or pool.
	if err != nil && errors.Is(err, ErrNoConnections) && !c.seedFallbackDisabled && c.seedFallbackPool != nil {
		res, err = c.performSeedFallback(req.Context(), req)
	}

	// Read, close and replace the http response body to close the connection.
	// Skipped when DisableResponseBuffering is set — the caller is responsible
	// for fully reading and closing the body.
	if !c.disableResponseBuffering && res != nil && res.Body != nil {
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err == nil {
			res.Body = io.NopCloser(bytes.NewReader(body))
		}
	}

	// TODO: Consider wrapping the error with request context.
	return res, err
}

// performSeedFallback attempts a single request using the seed URL fallback pool.
// Called as the absolute last resort when all router policies and connection pools
// are exhausted. Does not retry — this is already the final fallback.
//
// On success: marks the seed connection healthy and sets discoveryNeeded to
// expedite full cluster rediscovery.
// On failure: marks the seed connection as failed so the pool's resurrection
// timer can schedule retries.
func (c *Client) performSeedFallback(ctx context.Context, req *http.Request) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	conn, err := c.seedFallbackPool.Next()
	if err != nil {
		return nil, fmt.Errorf("cannot get connection: %w (seed fallback also exhausted)", err)
	}

	if dl := loadDebugLogger(); dl != nil {
		dl.Logf("Seed fallback: attempting request via %s\n", conn.URL)
	}

	c.setReqURL(conn.URL, req)
	c.setReqAuth(conn.URL, req)

	// Reset body for the fallback attempt.
	if req.Body != nil && req.Body != http.NoBody && req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, fmt.Errorf("cannot get request body for seed fallback: %w", err)
		}
		req.Body = body
	}

	if err := c.signRequest(req); err != nil {
		return nil, fmt.Errorf("failed to sign seed fallback request: %w", err)
	}

	start := time.Now().UTC()

	// Apply per-attempt timeout if configured.
	attemptReq := req
	var attemptCancel context.CancelFunc
	if c.requestTimeout > 0 {
		var attemptCtx context.Context
		attemptCtx, attemptCancel = context.WithTimeout(req.Context(), c.requestTimeout)
		attemptReq = req.WithContext(attemptCtx) //nolint:contextcheck // child of req.Context()
	}

	res, err := c.transport.RoundTrip(attemptReq)

	if attemptCancel != nil {
		if err != nil || res == nil {
			attemptCancel()
		} else {
			res.Body = &cancelOnCloseBody{ReadCloser: res.Body, cancel: attemptCancel}
		}
	}
	dur := time.Since(start)

	if c.logger != nil {
		c.logRoundTrip(req, res, err, start, dur)
	}

	if err != nil {
		if dl := loadDebugLogger(); dl != nil {
			dl.Logf("Seed fallback: request to %s failed: %v\n", conn.URL, err)
		}
		c.seedFallbackPool.OnFailure(conn) //nolint:errcheck,contextcheck // fire-and-forget; context in req
		return nil, fmt.Errorf("seed fallback request failed: %w", err)
	}

	if dl := loadDebugLogger(); dl != nil {
		dl.Logf("Seed fallback: request to %s succeeded, triggering rediscovery\n", conn.URL)
	}
	c.seedFallbackPool.OnSuccess(conn) //nolint:contextcheck // fire-and-forget; context in req
	c.discoveryNeeded.Store(true)
	return res, nil
}

// scheduleProactiveHealthCheck fires an asynchronous health check when a server signals
// connection closure (res.Close == true). This provides early detection of server shutdowns
// before the next request fails on a stale connection.
//
// Throttling uses a double-check RWMutex pattern on conn.proactiveCheck to minimize overhead:
//
//  1. TryRLock: fast-path read of lastAt. If the last check was within resurrectTimeoutInitial,
//     bail. If TryRLock fails, a writer (another goroutine scheduling a check) is active -- bail.
//  2. TryLock: slow-path write to update lastAt and launch the health check. If TryLock fails,
//     another goroutine won the race -- bail.
//
// This ensures that a burst of in-flight requests all receiving Connection: close (e.g., during
// a graceful shutdown) produces at most one health check per resurrectTimeoutInitial interval.
func (c *Client) scheduleProactiveHealthCheck(conn *Connection) {
	if c.healthCheck == nil {
		return
	}

	// Fast path: read-lock to check if a recent proactive check already ran.
	// If TryRLock fails, a writer is active (scheduling a check), so no action is needed.
	if !conn.proactiveCheck.mu.TryRLock() {
		return
	}
	if time.Since(conn.proactiveCheck.mu.lastAt) < c.resurrectTimeoutInitial {
		conn.proactiveCheck.mu.RUnlock()
		return
	}
	conn.proactiveCheck.mu.RUnlock()

	// Slow path: write-lock to update timestamp and launch check.
	// If TryLock fails, another goroutine won the race -- skip.
	if !conn.proactiveCheck.mu.TryLock() {
		return
	}
	// Re-check under write lock: another goroutine may have updated lastAt between
	// the RUnlock above and TryLock.
	if time.Since(conn.proactiveCheck.mu.lastAt) < c.resurrectTimeoutInitial {
		conn.proactiveCheck.mu.Unlock()
		return
	}
	conn.proactiveCheck.mu.lastAt = time.Now()
	conn.proactiveCheck.mu.Unlock()

	if dl := loadDebugLogger(); dl != nil {
		dl.Logf("Connection: close detected for %q, scheduling proactive health check\n", conn.URL)
	}

	go func() {
		resp, err := c.healthCheck(c.ctx, conn, conn.URL)
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}

		if err != nil {
			if dl := loadDebugLogger(); dl != nil {
				dl.Logf("Proactive health check failed for %q: %v\n", conn.URL, err)
			}

			// Mark connection as failed to trigger resurrection
			if c.router != nil {
				if poolErr := c.router.OnFailure(conn); poolErr != nil {
					if dl := loadDebugLogger(); dl != nil {
						dl.Logf("Router error during proactive health check failure for %q: %v\n", conn.URL, poolErr)
					}
				}
			} else {
				c.mu.Lock()
				if poolErr := c.mu.connectionPool.OnFailure(conn); poolErr != nil {
					if dl := loadDebugLogger(); dl != nil {
						dl.Logf("Pool error during proactive health check failure for %q: %v\n", conn.URL, poolErr)
					}
				}
				c.mu.Unlock()
			}
		} else {
			conn.decrementDrainingQuiescing()
		}
	}()
}

// URLs returns a list of transport URLs.
func (c *Client) URLs() []*url.URL {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.mu.connectionPool.URLs()
}

func (c *Client) setReqURL(u *url.URL, req *http.Request) {
	req.URL.Scheme = u.Scheme
	req.URL.Host = u.Host

	if u.Path != "" && u.Path != "/" {
		// Only prepend the base path if it's not empty or just "/"
		// This prevents double slashes like "//" when base URL has trailing slash
		var b strings.Builder
		b.Grow(len(u.Path) + len(req.URL.Path))
		b.WriteString(strings.TrimRight(u.Path, "/")) // Remove trailing slash from base
		b.WriteString(req.URL.Path)
		req.URL.Path = b.String()
	}
}

func (c *Client) setReqAuth(u *url.URL, req *http.Request) {
	if _, ok := req.Header["Authorization"]; !ok {
		if u.User != nil {
			password, _ := u.User.Password()
			req.SetBasicAuth(u.User.Username(), password)
			return
		}

		if c.username != "" && c.password != "" {
			req.SetBasicAuth(c.username, c.password)
			return
		}
	}
}

func (c *Client) signRequest(req *http.Request) error {
	if c.signer != nil {
		return c.signer.SignRequest(req)
	}
	return nil
}

func (c *Client) setReqUserAgent(req *http.Request) {
	req.Header.Set("User-Agent", c.userAgent)
}

func (c *Client) setReqGlobalHeader(req *http.Request) {
	if len(c.header) > 0 {
		for k, v := range c.header {
			if req.Header.Get(k) != k {
				for _, vv := range v {
					req.Header.Add(k, vv)
				}
			}
		}
	}
}

func (c *Client) logRoundTrip(
	req *http.Request,
	res *http.Response,
	err error,
	start time.Time,
	dur time.Duration,
) {
	var dupRes http.Response
	if res != nil {
		dupRes = *res
	}

	if c.logger.ResponseBodyEnabled() {
		if res != nil && res.Body != nil && res.Body != http.NoBody {
			//nolint:errcheck // ignored as this is only for logging
			b1, b2, _ := duplicateBody(res.Body)
			dupRes.Body = b1
			res.Body = b2
		}
	}

	//nolint:errcheck // ignored as this is only for logging
	c.logger.LogRoundTrip(req, &dupRes, err, start, dur)
}

// DefaultHealthCheck performs a health check on the given connection URL, choosing the best
// available endpoint. If the connection has been probed and supports cluster health
// (/_cluster/health?local=true), that endpoint is used for richer data. Otherwise, the
// baseline GET / endpoint is used, and an async probe is launched to detect cluster health support.
//
// This method is exported so users can wrap it with custom logging or metrics.
//
//nolint:nonamedreturns // named returns required for deferred metrics tracking
func (c *Client) DefaultHealthCheck(ctx context.Context, conn *Connection, u *url.URL) (res *http.Response, err error) {
	// Track health check outcomes (success/failure) at the top-level entry point.
	// Type-specific counters (baseline vs cluster health) are incremented in the
	// respective methods to accurately count fallback paths.
	if c.metrics != nil {
		defer func() {
			if err != nil {
				c.metrics.healthChecksFailed.Add(1)
			} else {
				c.metrics.healthChecksSuccess.Add(1)
			}
		}()
	}

	// Build the request modifier closure (may be nil)
	applyModifier := c.healthCheckRequestModifier

	// If conn is nil, fall through to baseline (backward compat for callers without a Connection)
	if conn == nil {
		return c.baselineHealthCheck(ctx, u, applyModifier)
	}

	// If the connection needs hardware info, substitute this health check cycle
	// with a /_nodes/_local/http,os call. This gets the node's core count without
	// an extra request -- we trade one health check cycle for hardware discovery.
	if conn.loadConnState().lifecycle().has(lcNeedsHardware) {
		return c.hardwareInfoHealthCheck(ctx, conn, u, applyModifier)
	}

	// Fast path: if cluster health is available, use the richer endpoint
	if conn.hasClusterHealth() {
		return c.clusterHealthCheck(ctx, conn, u, applyModifier)
	}

	// Otherwise, use baseline GET /
	res, err = c.baselineHealthCheck(ctx, u, applyModifier)
	if err != nil {
		return nil, err
	}

	// After successful baseline, conditionally launch async probe for cluster health.
	// Skip when cluster health probing is disabled via OPENSEARCH_GO_DISCOVERY_CONFIG.
	if !c.discoveryFeatures.clusterHealthEnabled() {
		return res, nil
	}

	switch {
	case conn.clusterHealthPending():
		// Never probed -- launch async probe
		go c.probeClusterHealthLocal(ctx, conn, u, applyModifier)

	case conn.clusterHealthUnavailable() && c.maxRetryClusterHealth > 0:
		// Previously unavailable (401/403 from cluster:monitor/health permission check) --
		// check if jittered retry interval has elapsed before re-probing.
		conn.mu.RLock()
		checkedAt := conn.mu.clusterHealthCheckedAt
		conn.mu.RUnlock()

		if !checkedAt.IsZero() {
			elapsed := time.Since(checkedAt)
			// Apply jitter (using the client's healthCheckJitter setting) to the retry
			// interval to prevent thundering herd when multiple connections were probed
			// around the same time.
			// #nosec G404 -- jitter for retry timing doesn't require cryptographic randomness
			jitteredInterval := c.maxRetryClusterHealth + time.Duration(
				(rand.Float64()*2-1)*c.healthCheckJitter*float64(c.maxRetryClusterHealth),
			)
			if elapsed > jitteredInterval {
				go c.probeClusterHealthLocal(ctx, conn, u, applyModifier)
			}
		}
	}

	return res, nil
}

// baselineHealthCheck performs the standard GET / health check against an OpenSearch node.
// It validates the response contains core fields (name, cluster_name, version.number).
func (c *Client) baselineHealthCheck(ctx context.Context, u *url.URL, applyModifier func(*http.Request)) (*http.Response, error) {
	if c.metrics != nil {
		c.metrics.healthChecks.Add(1)
	}

	var healthCtx context.Context
	var cancel context.CancelFunc

	if c.healthCheckTimeout > 0 {
		healthCtx, cancel = context.WithTimeout(ctx, c.healthCheckTimeout)
		defer cancel()
	} else {
		healthCtx = ctx
	}

	req, err := http.NewRequestWithContext(healthCtx, http.MethodGet, "/", nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errHealthCheckFailed, err)
	}

	c.setReqURL(u, req)
	c.setReqAuth(u, req)
	c.setReqUserAgent(req)

	if applyModifier != nil {
		applyModifier(req)
	}

	res, err := c.transport.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errHealthCheckFailed, err)
	}
	if res == nil {
		return nil, fmt.Errorf("%w: nil response", errHealthCheckFailed)
	}

	if res.StatusCode != http.StatusOK {
		if res.Body != nil {
			res.Body.Close()
		}
		return nil, fmt.Errorf("%w: status %d", errHealthCheckFailed, res.StatusCode)
	}

	if res.Body == nil {
		return nil, fmt.Errorf("%w: nil response body", errHealthCheckFailed)
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		res.Body.Close()
		return nil, fmt.Errorf("%w: %w", errHealthCheckFailed, err)
	}
	res.Body.Close()

	var info OpenSearchInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("%w: %w", errHealthCheckFailed, err)
	}

	if info.Name == "" || info.ClusterName == "" || info.Version.Number == "" {
		return nil, fmt.Errorf("%w: invalid response structure", errHealthCheckFailed)
	}

	res.Body = io.NopCloser(bytes.NewReader(body))
	return res, nil
}

// hardwareInfoHealthCheck substitutes one health check cycle with a
// GET /_nodes/_local/http,os,thread_pool call to discover the node's
// core count and thread pool configurations. On success it stores
// allocatedProcessors and per-pool cwnd ceilings, then clears
// lcNeedsHardware. On any failure it falls back to the baseline health
// check so the connection is not penalized for a hardware info failure.
func (c *Client) hardwareInfoHealthCheck(
	ctx context.Context, conn *Connection, u *url.URL, applyModifier func(*http.Request),
) (*http.Response, error) {
	if c.metrics != nil {
		c.metrics.healthChecks.Add(1)
	}

	var healthCtx context.Context
	var cancel context.CancelFunc

	if c.healthCheckTimeout > 0 {
		healthCtx, cancel = context.WithTimeout(ctx, c.healthCheckTimeout)
		defer cancel()
	} else {
		healthCtx = ctx
	}

	req, err := http.NewRequestWithContext(healthCtx, http.MethodGet, "/_nodes/_local/http,os,thread_pool", nil)
	if err != nil {
		return c.baselineHealthCheck(ctx, u, applyModifier)
	}

	c.setReqURL(u, req)
	c.setReqAuth(u, req)
	c.setReqUserAgent(req)

	if applyModifier != nil {
		applyModifier(req)
	}

	res, err := c.transport.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errHealthCheckFailed, err)
	}
	if res == nil {
		return nil, fmt.Errorf("%w: nil response", errHealthCheckFailed)
	}

	if res.StatusCode != http.StatusOK {
		if res.Body != nil {
			res.Body.Close()
		}
		// Non-200 (e.g. 403 from security plugin) -- fall back to baseline.
		// Clear the lcNeedsHardware bit so we don't retry every health check cycle.
		conn.mu.Lock()
		conn.casLifecycle(conn.loadConnState(), 0, 0, lcNeedsHardware) //nolint:errcheck // lock held; only errLifecycleNoop possible
		conn.mu.Unlock()
		return c.baselineHealthCheck(ctx, u, applyModifier)
	}

	if res.Body == nil {
		return nil, fmt.Errorf("%w: nil response body", errHealthCheckFailed)
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		res.Body.Close()
		return nil, fmt.Errorf("%w: %w", errHealthCheckFailed, err)
	}
	res.Body.Close()

	// Parse the /_nodes/_local/http,os response. Structure:
	//   { "_nodes": {...}, "cluster_name": "...", "nodes": { "<id>": { "os": { "allocated_processors": N }, ... } } }
	var env map[string]json.RawMessage
	if err := json.Unmarshal(body, &env); err != nil {
		// Parse failure -- clear bit and treat as successful health check
		// (the node responded, just not in expected format).
		conn.mu.Lock()
		conn.casLifecycle(conn.loadConnState(), 0, 0, lcNeedsHardware) //nolint:errcheck // parse failure; only errLifecycleNoop possible
		conn.mu.Unlock()
		res.Body = io.NopCloser(bytes.NewReader(body))
		return res, nil
	}

	var nodes map[string]nodeInfo
	if err := json.Unmarshal(env["nodes"], &nodes); err != nil {
		conn.mu.Lock()
		conn.casLifecycle(conn.loadConnState(), 0, 0, lcNeedsHardware) //nolint:errcheck // parse failure; only errLifecycleNoop possible
		conn.mu.Unlock()
		res.Body = io.NopCloser(bytes.NewReader(body))
		return res, nil
	}

	// /_nodes/_local returns exactly one node entry.
	for _, node := range nodes {
		if node.OS != nil && node.OS.AllocatedProcessors != nil && *node.OS.AllocatedProcessors > 0 {
			conn.storeAllocatedProcessors(*node.OS.AllocatedProcessors)
		}
		if len(node.ThreadPool) > 0 {
			storeThreadPoolSizes(conn, node.ThreadPool)
			if c.poolInfoCount.Add(1) >= 1 {
				c.poolInfoReady.Store(true)
			}
		}
		break
	}

	// Clear lcNeedsHardware -- hardware info obtained (or not available).
	conn.mu.Lock()
	conn.casLifecycle(conn.loadConnState(), 0, 0, lcNeedsHardware) //nolint:errcheck // lock held; only errLifecycleNoop possible
	conn.mu.Unlock()

	res.Body = io.NopCloser(bytes.NewReader(body))
	return res, nil
}

// clusterHealthCheck performs a GET /_cluster/health?local=true health check.
// This endpoint requires the cluster:monitor/health action privilege from the
// OpenSearch Security plugin (see [ClusterHealthLocal] for permission details).
//
// Status code handling follows the OpenSearch server's response contract:
//   - 200: Success. Parses [ClusterHealthLocal] and stores it on the connection.
//   - 401: Authentication failure (missing/invalid credentials). Falls back to GET /,
//     resets cluster health state to pending, and zeroes out stale cluster health data.
//   - 403: Authorization failure (user lacks cluster:monitor/health privilege). Same
//     fallback behavior as 401 -- the permission may have been revoked at runtime.
//
// fetchClusterHealth performs GET /_cluster/health?local=true against the given URL
// and returns the parsed health data, the HTTP status code, and any error.
//
// On success (200), it returns the parsed ClusterHealthLocal and status 200.
// On HTTP errors (401, 403, 5xx, etc.), it returns nil health, the status code, and nil error.
// On transport/network errors, it returns 0 status and the error.
//
// The caller is responsible for interpreting the status code and deciding how to
// handle auth failures, transient errors, and state transitions.
func (c *Client) fetchClusterHealth(ctx context.Context, u *url.URL, applyModifier func(*http.Request)) (*ClusterHealthLocal, int, error) {
	if c.metrics != nil {
		c.metrics.clusterHealthChecks.Add(1)
	}

	var healthCtx context.Context
	var cancel context.CancelFunc

	if c.healthCheckTimeout > 0 {
		healthCtx, cancel = context.WithTimeout(ctx, c.healthCheckTimeout)
		defer cancel()
	} else {
		healthCtx = ctx
	}

	req, err := http.NewRequestWithContext(healthCtx, http.MethodGet, "/_cluster/health", nil)
	if err != nil {
		return nil, 0, fmt.Errorf("creating cluster health request: %w", err)
	}

	req.URL.RawQuery = "local=true"

	c.setReqURL(u, req)
	c.setReqAuth(u, req)
	c.setReqUserAgent(req)

	if applyModifier != nil {
		applyModifier(req)
	}

	res, err := c.transport.RoundTrip(req)
	if err != nil {
		return nil, 0, err
	}
	if res == nil {
		return nil, 0, fmt.Errorf("nil response from /_cluster/health")
	}
	defer func() {
		if res.Body != nil {
			res.Body.Close()
		}
	}()

	if res.StatusCode != http.StatusOK {
		return nil, res.StatusCode, nil
	}

	if res.Body == nil {
		return nil, res.StatusCode, fmt.Errorf("nil response body from /_cluster/health")
	}

	body, readErr := io.ReadAll(res.Body)
	if readErr != nil {
		return nil, res.StatusCode, fmt.Errorf("reading /_cluster/health body: %w", readErr)
	}

	var health ClusterHealthLocal
	if jsonErr := json.Unmarshal(body, &health); jsonErr != nil {
		return nil, res.StatusCode, fmt.Errorf("parsing /_cluster/health: %w", jsonErr)
	}

	return &health, res.StatusCode, nil
}

// storeClusterHealth updates the connection's cluster health data under lock.
// storeClusterHealth stores the health data and timestamp on the connection.
// CALLER RESPONSIBILITIES: Caller must hold conn.mu.Lock().
func storeClusterHealth(conn *Connection, health *ClusterHealthLocal) {
	conn.mu.clusterHealth = health
	conn.mu.clusterHealthCheckedAt = time.Now()
}

// resetClusterHealth resets a connection's cluster health state to pending and
// zeros out stale cluster health data. Called when the /_cluster/health endpoint
// returns 401/403 (permission revoked at runtime).
func resetClusterHealth(conn *Connection) {
	conn.mu.Lock()
	conn.mu.clusterHealth = nil
	conn.mu.clusterHealthCheckedAt = time.Time{}
	conn.clearLifecycleBit(lcClusterHealthProbed)    //nolint:errcheck // noop is fine
	conn.clearLifecycleBit(lcClusterHealthAvailable) //nolint:errcheck // noop is fine
	conn.mu.Unlock()
}

//   - 408: Request timeout (only with wait_for_* params, which we don't use). Treated
//     as transient; falls back to GET / without changing state.
//   - 429: Thread pool rejection (backpressure). Transient; falls back to GET /.
//   - 5xx: Server error or node not ready. Transient; falls back to GET /.
func (c *Client) clusterHealthCheck(
	ctx context.Context,
	conn *Connection,
	u *url.URL,
	applyModifier func(*http.Request),
) (*http.Response, error) {
	health, statusCode, err := c.fetchClusterHealth(ctx, u, applyModifier)
	if err != nil {
		// Transport/network error -- fall back to baseline without changing state
		return c.baselineHealthCheck(ctx, u, applyModifier)
	}

	switch {
	case statusCode == http.StatusOK && health != nil:
		conn.mu.Lock()
		storeClusterHealth(conn, health)
		conn.setLifecycleBit(lcClusterHealthProbed | lcClusterHealthAvailable) //nolint:errcheck // noop is fine
		conn.mu.Unlock()

		// Readiness gate: reject nodes that are still recovering (initializing shards).
		// This prevents slamming a node with traffic while it is still absorbing shard data.
		// Cold-start safety: when all ready connections are down, Next() falls through to
		// tryZombieWithLock(), so requests still succeed via zombie connections. The node
		// will be retried on the next health check cycle once shards finish initializing.
		if health.InitializingShards > 0 {
			return nil, fmt.Errorf("%w: node %s has %d initializing shards (not ready)",
				errHealthCheckFailed, u.Host, health.InitializingShards)
		}

		// Return a synthetic response with the health JSON body for callers that inspect it
		body, err := json.Marshal(health)
		if err != nil {
			// Highly unlikely since health is a simple struct we just unmarshaled
			return nil, fmt.Errorf("failed to marshal health response: %w", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil

	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		// Permission revoked -- fall back to GET /, zero out stale data, reset to pending
		resetClusterHealth(conn)
		return c.baselineHealthCheck(ctx, u, applyModifier)

	default:
		// Transient error (5xx, etc.) -- fall back to baseline without changing state
		return c.baselineHealthCheck(ctx, u, applyModifier)
	}
}

// probeClusterHealthLocal asynchronously probes whether /_cluster/health?local=true is accessible
// on the given connection. This is called via `go` after a successful baseline health check
// and does NOT block the critical path -- the connection is already ready and serving traffic.
//
// The probe detects whether the node's credentials have the cluster:monitor/health privilege
// required by the OpenSearch Security plugin. The result determines which endpoint subsequent
// health checks use:
//
//   - 200: Probe succeeded. Sets lcClusterHealthProbed|lcClusterHealthAvailable, populates
//     [Connection.mu.clusterHealth], and records the probe timestamp. Subsequent health
//     checks will use /_cluster/health?local=true for richer data.
//   - 401/403: The cluster:monitor/health privilege is not available. Sets lcClusterHealthProbed
//     only (marking unavailable) and records the timestamp. The client continues using GET /
//     and will retry the probe after MaxRetryClusterHealth elapses (default 4h).
//   - Transient errors (5xx, network, timeout): Leaves state at pending and does NOT
//     record a timestamp, so the probe is retried on the very next health check cycle.
func (c *Client) probeClusterHealthLocal(ctx context.Context, conn *Connection, u *url.URL, applyModifier func(*http.Request)) {
	health, statusCode, err := c.fetchClusterHealth(ctx, u, applyModifier)
	if err != nil {
		// Transient error -- leave at pending (0), retry next health check
		return
	}

	switch {
	case statusCode == http.StatusOK && health != nil:
		// Probe succeeded -- store data and mark available (under conn.mu)
		conn.mu.Lock()
		storeClusterHealth(conn, health)
		conn.setLifecycleBit(lcClusterHealthProbed | lcClusterHealthAvailable) //nolint:errcheck // noop is fine
		conn.mu.Unlock()

	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		// Definitively unavailable -- record timestamp for retry timing
		conn.mu.Lock()
		conn.mu.clusterHealthCheckedAt = time.Now()
		conn.setLifecycleBit(lcClusterHealthProbed)      //nolint:errcheck // noop is fine
		conn.clearLifecycleBit(lcClusterHealthAvailable) //nolint:errcheck // noop is fine
		conn.mu.Unlock()

	default:
		// Transient error (5xx, etc.) -- leave at pending (0), do NOT set timestamp
		// This ensures the probe is retried on the next health check
	}
}

// backoffRetry performs retries with exponential backoff and jitter.
// It calls the provided function up to maxRetries times with delays between attempts.
// Returns nil on success, or the last error encountered.
func backoffRetry(baseDelay time.Duration, maxRetries int, jitter float64, fn func() error) error {
	if maxRetries <= 0 {
		return fn() // Single attempt when retries disabled
	}

	var lastErr error
	for attempt := range maxRetries {
		if err := fn(); err == nil {
			return nil // Success
		} else {
			lastErr = err
		}

		// If this is not the last attempt, wait before retrying
		if attempt < maxRetries-1 && baseDelay > 0 {
			// Exponential backoff: base delay * 2^attempt
			// Cap attempt to prevent overflow (2^30 is ~1 billion, more than enough)
			cappedAttempt := min(attempt, 30)
			delay := time.Duration(int64(baseDelay) * (1 << cappedAttempt))

			// Apply jitter to avoid thundering herd
			// #nosec G404 -- jitter for retry backoff doesn't require cryptographic randomness
			if jitter > 0.0 {
				jitterRange := float64(delay) * jitter
				jitterOffset := (rand.Float64()*2 - 1) * jitterRange // -jitter to +jitter
				delay = time.Duration(float64(delay) + jitterOffset)
			}

			time.Sleep(delay)
		}
	}

	return lastErr
}

// healthCheckWithRetries performs health checks with exponential backoff retry logic.
// It attempts up to the configured maxRetries times with jittered delays between attempts.
// Returns true if the connection is healthy, false otherwise.
//
// On success, records the RTT of the final (successful) attempt to the
// connection's rttRing so that connection scoring has data even for connections
// that have never been through the resurrection path.
func (c *Client) healthCheckWithRetries(ctx context.Context, conn *Connection, maxRetries int) bool {
	// Use the provided maxRetries parameter, but respect client's timeout/jitter config
	baseDelay := c.healthCheckTimeout / 2 // Start with half the timeout as base delay
	if baseDelay <= 0 {
		baseDelay = defaultHealthCheckTimeout / 2 // Fallback if timeout is disabled
	}

	var lastRTT time.Duration
	err := backoffRetry(baseDelay, maxRetries, c.healthCheckJitter, func() error {
		start := time.Now()
		res, err := c.DefaultHealthCheck(ctx, conn, conn.URL)
		if err != nil {
			return err
		}
		lastRTT = time.Since(start)
		if res != nil && res.Body != nil {
			res.Body.Close()
		}
		return nil
	})

	if err == nil {
		conn.rttRing.add(lastRTT)
		conn.mu.Lock()
		conn.markAsHealthyWithLock()
		conn.mu.Unlock()
		return true
	}

	return false
}

func initUserAgent() string {
	var b strings.Builder

	b.WriteString("opensearch-go")
	b.WriteRune('/')
	b.WriteString(Version)
	b.WriteRune(' ')
	b.WriteRune('(')
	b.WriteString(runtime.GOOS)
	b.WriteRune(' ')
	b.WriteString(runtime.GOARCH)
	b.WriteString("; ")
	b.WriteString("Go ")
	if v := reGoVersion.ReplaceAllString(runtime.Version(), "$1"); v != "" {
		b.WriteString(v)
	} else {
		b.WriteString(runtime.Version())
	}
	b.WriteRune(')')

	return b.String()
}

// newMultiServerPoolFromClientWithLock creates a multiServerPool initialized
// with the client's current settings (timeouts, health check, cap, etc.). The
// pool's mu-protected fields (ready, dead, members, activeCount) are left at
// zero values — the caller is responsible for populating them.
//
// This is the single source of truth for client → pool settings propagation.
// Used by promoteConnectionPoolWithLock (single→multi promotion) and
// createOrUpdateMultiNodePoolWithLock (first multi-node discovery).
//
// Caller must hold c.mu.Lock().
func (c *Client) newMultiServerPoolFromClientWithLock(name string, m *metrics) *multiServerPool {
	pool := &multiServerPool{
		name:                         name,
		ctx:                          c.ctx,
		resurrectTimeoutInitial:      c.resurrectTimeoutInitial,
		resurrectTimeoutMax:          c.resurrectTimeoutMax,
		resurrectTimeoutFactorCutoff: c.resurrectTimeoutFactorCutoff,
		minimumResurrectTimeout:      c.minimumResurrectTimeout,
		jitterScale:                  c.jitterScale,
		serverMaxNewConnsPerSec:      c.serverMaxNewConnsPerSec,
		clientsPerServer:             c.clientsPerServer,
		healthCheck:                  c.healthCheck,
		metrics:                      m,
		activeListCap:                c.activeListCap,
		activeListCapConfig:          c.activeListCapConfig,
		standbyPromotionChecks:       c.standbyPromotionChecks,
	}
	if obs := c.observer.Load(); obs != nil {
		pool.observer.Store(obs)
	}
	return pool
}

// promoteConnectionPoolWithLock converts a singleServerPool to multiServerPool while preserving
// metrics, timeout settings, and client configuration. MUST be called while holding client write lock.
// Returns existing pool unchanged if already a multiServerPool.
func (c *Client) promoteConnectionPoolWithLock(readyConnections, deadConnections []*Connection) *multiServerPool {
	switch currentPool := c.mu.connectionPool.(type) {
	case *singleServerPool:
		// Promote from single to multi-node pool using client-configured timeouts
		filteredReady := make([]*Connection, 0, len(readyConnections))
		filteredDead := make([]*Connection, 0, len(deadConnections))
		c.applyConnectionFiltering(readyConnections, deadConnections, &filteredReady, &filteredDead)

		// Shuffle connections for load distribution unless disabled
		if !c.skipConnectionShuffle && len(filteredReady) > 1 {
			rand.Shuffle(len(filteredReady), func(i, j int) {
				filteredReady[i], filteredReady[j] = filteredReady[j], filteredReady[i]
			})
		}

		pool := c.newMultiServerPoolFromClientWithLock("allConns", currentPool.metrics)
		pool.mu.ready = filteredReady
		pool.mu.dead = filteredDead
		pool.mu.members = make(map[*Connection]struct{}, max(len(filteredReady)+len(filteredDead), defaultMembersCapacity))
		for _, conn := range filteredReady {
			pool.mu.members[conn] = struct{}{}
		}
		for _, conn := range filteredDead {
			pool.mu.members[conn] = struct{}{}
		}

		// All ready connections start as active with proper state.
		for _, conn := range filteredReady {
			conn.mu.Lock()
			conn.casLifecycle(conn.loadConnState(), 0, lcActive, lcUnknown|lcStandby) //nolint:errcheck // lock held; only errLifecycleNoop possible
			conn.mu.Unlock()
		}
		pool.mu.activeCount = len(filteredReady)

		// Enforce the active list cap: moves overflow active connections to standby.
		pool.enforceActiveCapWithLock()

		if dl := loadDebugLogger(); dl != nil {
			dl.Logf("Promoted singleServerPool to multiServerPool: %d ready, %d dead connections (timeouts: %v, %d)\n",
				len(pool.mu.ready), len(pool.mu.dead), pool.resurrectTimeoutInitial, pool.resurrectTimeoutFactorCutoff)
		}

		return pool

	case *multiServerPool:
		// Already a multiServerPool - return unchanged
		return currentPool

	default:
		panic(fmt.Sprintf("unsupported connection pool type for promotion: %T", currentPool))
	}
}

// demoteConnectionPoolWithLock converts a multiServerPool to singleServerPool while preserving
// metrics and selecting the best available connection. MUST be called while holding client write lock.
// Returns existing pool unchanged if already a singleServerPool.
func (c *Client) demoteConnectionPoolWithLock() *singleServerPool {
	switch currentPool := c.mu.connectionPool.(type) {
	case *multiServerPool:
		// Demote from multi-node to single-node pool
		metrics := currentPool.metrics

		currentPool.mu.RLock()
		var connection *Connection

		switch {
		case len(currentPool.mu.ready) > 0:
			connection = currentPool.mu.ready[0]
			if dl := loadDebugLogger(); dl != nil {
				dl.Logf("Demoting multiServerPool to singleServerPool using ready connection: %s\n", connection.URL)
			}
		case len(currentPool.mu.dead) > 0:
			connection = currentPool.mu.dead[0]
			if dl := loadDebugLogger(); dl != nil {
				dl.Logf("Demoting multiServerPool to singleServerPool using dead connection: %s\n", connection.URL)
			}
		default:
			if dl := loadDebugLogger(); dl != nil {
				dl.Logf("Warning: Demoting multiServerPool with no connections available\n")
			}
		}

		currentPool.mu.RUnlock()

		return &singleServerPool{
			connection: connection,
			metrics:    metrics,
		}

	case *singleServerPool:
		// Already a singleServerPool - return unchanged
		return currentPool

	default:
		panic(fmt.Sprintf("unsupported connection pool type for demotion: %T", currentPool))
	}
}

// applyConnectionFiltering applies client-level filtering for dedicated cluster managers
func (c *Client) applyConnectionFiltering(readyConnections, deadConnections []*Connection, filteredReady, filteredDead *[]*Connection) {
	for _, conn := range readyConnections {
		if !c.includeDedicatedClusterManagers && conn.Roles.isDedicatedClusterManager() {
			if dl := loadDebugLogger(); dl != nil {
				dl.Logf("Excluding dedicated cluster manager %q from connection pool\n", conn.Name)
			}
			continue
		}
		*filteredReady = append(*filteredReady, conn)
	}

	for _, conn := range deadConnections {
		if !c.includeDedicatedClusterManagers && conn.Roles.isDedicatedClusterManager() {
			continue
		}
		*filteredDead = append(*filteredDead, conn)
	}
}

// NoOpHealthCheck is a no-operation health check that always succeeds.
// This can be used to disable health checking while maintaining the function signature.
// Returns nil, nil to indicate success without creating response objects.
func NoOpHealthCheck(_ context.Context, _ *Connection, _ *url.URL) (*http.Response, error) {
	return nil, nil //nolint:nilnil // Intentional no-op: nil response + nil error signals "healthy, no body".
}

const routingParam = "routing"

// extractRouting returns the value of ?routing=X from the request's query
// string, or "" if absent. Uses a zero-alloc linear scan to avoid
// url.Query() overhead on the hot path.
//
// The returned value is URL-decoded (percent-encoded sequences are resolved)
// since OpenSearch's Murmur3HashFunction hashes the decoded routing string.
func extractRouting(req *http.Request) string {
	raw := req.URL.RawQuery

	const needle = routingParam + "="
	for i := 0; i <= len(raw)-len(needle); i++ {
		if raw[i:i+len(needle)] == needle && (i == 0 || raw[i-1] == '&' || raw[i-1] == '?') {
			// Found "routing=" at a valid anchor point.
			valStart := i + len(needle)

			// Find end of value (next '&' or end of string).
			valEnd := valStart
			for valEnd < len(raw) && raw[valEnd] != '&' {
				valEnd++
			}

			val := raw[valStart:valEnd]
			if val == "" {
				return ""
			}

			// Fast path: no percent-encoding present.
			if !strings.Contains(val, "%") {
				return val
			}

			// Slow path: URL-decode the value.
			decoded, err := url.QueryUnescape(val)
			if err != nil {
				return val // Return raw value on decode error.
			}
			return decoded
		}
	}
	return ""
}

// cancelOnCloseBody wraps a response body so that a context cancel function
// is called when the body is closed. This ensures per-attempt timeout contexts
// are cleaned up after the caller finishes reading the response.
type cancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

// Close closes the underlying body and cancels the per-attempt context.
func (b *cancelOnCloseBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}
