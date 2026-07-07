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

package opensearch

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/opensearch-project/opensearch-go/v5/internal/envvars"
	"github.com/opensearch-project/opensearch-go/v5/internal/path"
	"github.com/opensearch-project/opensearch-go/v5/internal/ttlcache"
	"github.com/opensearch-project/opensearch-go/v5/internal/version"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
	"github.com/opensearch-project/opensearch-go/v5/signer"
)

const (
	// SchemeInsecure is the HTTP scheme for insecure connections.
	SchemeInsecure = "http"
	// SchemeSecure is the HTTPS scheme for secure connections.
	SchemeSecure = "https"
	// DefaultScheme is the default connection scheme.
	DefaultScheme = SchemeInsecure
	// DefaultHost is the default OpenSearch host.
	DefaultHost = "localhost"
	// DefaultPort is the default OpenSearch port.
	DefaultPort = 9200

	// Internal constants
	defaultScheme      = DefaultScheme
	defaultHost        = DefaultHost
	defaultPort        = "9200"
	defaultURL         = defaultScheme + "://" + defaultHost + ":" + defaultPort
	openSearch         = "opensearch"
	unsupportedProduct = "the client noticed that the server is not a supported distribution"
	envOpenSearchURL   = envvars.OpenSearchURL
	envRouter          = envvars.Router
)

// Version returns the package version as a string.
const Version = version.Client

// Error vars
var (
	ErrCreateClient                        = errors.New("cannot create client")
	ErrCreateTransport                     = errors.New("error creating transport")
	ErrParseVersion                        = errors.New("failed to parse opensearch version")
	ErrParseURL                            = errors.New("cannot parse url")
	ErrPathRequired                        = path.ErrRequired
	ErrTransportMissingMethodMetrics       = errors.New("transport is missing method Metrics()")
	ErrTransportMissingMethodDiscoverNodes = errors.New("transport is missing method DiscoverNodes()")
)

// Config represents the client configuration.
type Config struct {
	Addresses []string // A list of nodes to use.
	Username  string   // Username for HTTP Basic Authentication.
	// Password for HTTP Basic Authentication.
	Password string // #nosec G117

	Header http.Header // Global HTTP request header.

	Signer signer.Signer

	// PEM-encoded certificate authorities.
	// When set, an empty certificate pool will be created, and the certificates will be appended to it.
	// The option is only valid when the transport is not specified, or when it's http.Transport.
	CACert []byte

	// InsecureSkipVerify disables TLS certificate verification.
	// When true, the transport's TLS config is set to skip verification,
	// cloning the existing transport (or http.DefaultTransport) to preserve
	// connection pooling, HTTP/2, and other defaults.
	InsecureSkipVerify bool

	RetryOnStatus        []int // List of status codes for retry. Default: 502, 503, 504.
	DisableRetry         bool  // Default: false.
	EnableRetryOnTimeout bool  // Default: false.
	MaxRetries           int   // Default: 3.

	// RequestTimeout sets a per-attempt timeout for each HTTP round-trip.
	// When set, a context deadline is applied to each individual request attempt
	// (including each retry). This bounds the maximum time a single request can
	// block, preventing indefinite hangs on stalled connections.
	// 0 = no per-attempt timeout (default), >0 = explicit timeout.
	RequestTimeout time.Duration

	// DNSCacheRefresh controls how often the client-side DNS cache re-resolves
	// cached hostnames. The cache is installed only when no custom Transport is
	// provided; a caller-supplied Transport is never modified.
	// 0 = default (60s), <0 = disable caching, >0 = explicit interval.
	DNSCacheRefresh time.Duration

	// DNSDialTimeout sets the dial timeout of the net.Dialer behind the
	// client-side DNS cache; only applies when the cache is installed.
	// 0 = default (30s), <0 = no dial timeout, >0 = explicit timeout.
	DNSDialTimeout time.Duration

	// DNSKeepAlive sets the keep-alive interval of the net.Dialer behind the
	// client-side DNS cache; only applies when the cache is installed.
	// 0 = default (30s), <0 = disable keep-alive probes, >0 = explicit interval.
	DNSKeepAlive time.Duration

	// DNSTimeout bounds each cache refresh lookup behind the client-side DNS
	// cache; only applies when the cache is installed. Prevents one hung
	// resolution from stalling a (sequential) refresh tick.
	// 0 = default (10s), <0 = no per-lookup timeout, >0 = explicit timeout.
	DNSTimeout time.Duration

	CompressRequestBody bool // Default: false.

	// DiscoverNodesOnStart triggers an asynchronous discovery cycle as soon
	// as NewClient returns. nil (the default) means "auto": if Router is
	// also nil and OPENSEARCH_GO_ROUTER is not explicitly false, this is
	// treated as true so the client starts populating topology before the
	// first request. Any explicitly set value (true or false) is respected
	// as-is and suppresses the env-var inheritance. When Router is set
	// programmatically the env var is ignored entirely; the caller is
	// responsible for triggering discovery if desired.
	DiscoverNodesOnStart  *bool
	DiscoverNodesInterval time.Duration // Discover nodes periodically. Default: disabled.

	// Health check configuration
	HealthCheckTimeout    time.Duration // Timeout for health check requests. Default: 3s.
	HealthCheckMaxRetries int           // Max retries for health checks. Default: 3. Set to -1 to disable health checks.
	HealthCheckJitter     float64       // Jitter factor for health check timing (0.0-1.0). Default: 0.2.

	// Resurrection timeout configuration for dead connection recovery.
	// These control how quickly the client retries dead nodes and how aggressively
	// it reconnects during cluster outages or rolling restarts.
	ResurrectTimeoutInitial      time.Duration // Initial backoff for dead connections. Default: 5s.
	ResurrectTimeoutMax          time.Duration // Max backoff before jitter. Default: 30s.
	ResurrectTimeoutFactorCutoff int           // Exponential backoff cutoff factor. Default: 5.
	MinimumResurrectTimeout      time.Duration // Absolute minimum retry interval. Default: 500ms.
	JitterScale                  float64       // Jitter multiplier (0.0-1.0). Default: 0.5.

	// Health check rate limiting to prevent overwhelming recovering servers.
	// During outages, all clients reconnect simultaneously, creating TLS handshake
	// pressure on recovering servers. Health check rates are auto-derived from the
	// server's core count (discovered via /_nodes/_local/http,os per node).
	//
	// MaxRetryClusterHealth controls how often to retry the cluster health probe
	// (/_cluster/health?local=true) on nodes where it was previously unavailable due to
	// missing cluster:monitor/health permission (401 Unauthorized or 403 Forbidden).
	// Jitter from HealthCheckJitter is applied to the interval to prevent thundering herd.
	// 0 = use default (4h), <0 = disable cluster health probing entirely.
	// >0 = explicit retry interval.
	// Default: 4h
	MaxRetryClusterHealth time.Duration

	// HealthCheckRequestModifier is called on every health check HTTP request before it is sent.
	// This allows injecting custom authentication headers or other modifications without
	// replacing the entire health check function.
	// Default: nil (no modification)
	HealthCheckRequestModifier func(*http.Request)

	EnableDebugLogger bool // Enable the debug logging.

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
	// Default: 0 (auto-derive)
	ActiveListCap int

	// StandbyRotationInterval sets how often a standby connection is rotated
	// into the ready list (and an active connection is evicted to standby).
	// 0 = use DiscoverNodesInterval, >0 = explicit interval, <0 = disabled.
	// Default: 0 (use DiscoverNodesInterval)
	StandbyRotationInterval time.Duration

	// StandbyRotationCount sets how many standby connections are rotated per
	// discovery cycle. 0 = use default (1), >0 = explicit count.
	// Default: 1
	StandbyRotationCount int

	// StandbyPromotionChecks sets the number of consecutive successful health
	// checks required before a standby connection can be promoted to live.
	// 0 = use default (3), >0 = explicit count.
	// Default: 3
	StandbyPromotionChecks int

	RetryBackoff func(attempt int) time.Duration // Optional backoff duration. Default: nil.

	Transport http.RoundTripper                      // The HTTP transport object.
	Logger    opensearchtransport.Logger             // The logger object.
	Selector  opensearchtransport.Selector           // The selector object.
	Router    opensearchtransport.Router             // Optional router for request-aware routing.
	Observer  opensearchtransport.ConnectionObserver // Optional observer for connection lifecycle events.

	// ShardCostConfig overrides shard cost multipliers for connection scoring.
	// See [opensearchtransport.Config.ShardCostConfig] for format details.
	ShardCostConfig string

	// Context for background operations (node discovery, health checks, stats polling).
	// If nil, context.Background() is used. The transport derives a child context from
	// this, so canceling the parent automatically stops all background goroutines.
	// For example, passing t.Context() in tests ensures cleanup when the test ends.
	//nolint:containedctx // Config struct is short-lived, context extracted during New()
	Context context.Context

	// Optional constructor function for a custom ConnectionPool. Default: nil.
	ConnectionPoolFunc func([]*opensearchtransport.Connection, opensearchtransport.Selector) opensearchtransport.ConnectionPool

	// AddressResolver is called during node discovery for each node discovered
	// via /_nodes/http. If non-nil, it can rewrite a node's URL before it enters
	// the connection pool. This is useful for redirecting traffic through sidecar
	// proxies or rewriting hostnames for network topology.
	// Default: nil (no address rewriting).
	AddressResolver opensearchtransport.AddressResolverFunc

	// MaxAddressResolvers sets the maximum number of concurrent AddressResolverFunc
	// invocations during a single discovery cycle.
	// 0 = auto-derive: min(len(nodes), runtime.GOMAXPROCS(0)) per cycle.
	// 1 = serialized (one resolver runs at a time, via a weight-1 semaphore).
	// >1 = explicit concurrency cap.
	// <0 = unlimited (all nodes resolved concurrently, no semaphore).
	// Default: 0 (auto-derive). Only meaningful when AddressResolver is non-nil.
	MaxAddressResolvers int

	// AddressResolverRunner replaces the built-in resolution handler when set.
	// It receives all discovered nodes and the per-node AddressResolverFunc,
	// and returns resolved addresses. This allows custom orchestration policies:
	// stricter failure handling, retry logic, batched resolution, etc.
	//
	// When set, MaxAddressResolvers is ignored. AddressResolver is still passed
	// to the runner as the per-node resolve function.
	// Default: nil (built-in handler).
	AddressResolverRunner opensearchtransport.AddressResolverRunnerFunc
}

// Client represents the OpenSearch client.
type Client struct {
	Transport opensearchtransport.Interface
	config    *Config

	// closeFn overrides Close's teardown. It is nil for an ordinary client
	// (Close tears down the transport directly) and non-nil only for a
	// per-holder handle minted by SharedCopy, where it is a cache-release hook
	// so Close decrements the shared entry's refcount in O(1) instead of
	// closing the transport the holder shares with others.
	closeFn func() error
}

// NewDefaultClient creates a new client with default options.
//
// It will use http://localhost:9200 as the default address.
//
// It will use the OPENSEARCH_URL/ELASTICSEARCH_URL environment variable, if set,
// to configure the addresses; use a comma to separate multiple URLs.
//
// It's an error to set both OPENSEARCH_URL and ELASTICSEARCH_URL.
func NewDefaultClient() (*Client, error) {
	return newCachedDefault(Config{})
}

// defaultClientCache holds implicitly-constructed default clients keyed by
// config hash so identical NewDefaultClient calls share one transport (and its
// background goroutines) instead of leaking one set per call.
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

// cachedDefault is the ttlcache.Cacheable for an implicit default client. Key
// hashes the config with env-derived seed addresses folded in (so default
// clients pointed at different env URLs never collide on the empty Config{}
// hash), reporting ttlcache.ErrNotCacheable for an un-hashable config. New
// builds the client and its liveness probe on a miss.
type cachedDefault struct{ cfg Config }

// Key hashes the config with env-derived seed addresses folded in, or reports
// ttlcache.ErrNotCacheable for an un-hashable config.
//
//nolint:gosec // G115: key is an fnv hash; the bit pattern is the identity, not a magnitude
func (d cachedDefault) Key() (ttlcache.Key, error) {
	keyCfg := d.cfg
	if len(keyCfg.Addresses) == 0 {
		keyCfg.Addresses = getAddressFromEnvironment()
	}
	key, ok := HashConfig(keyCfg)
	if !ok {
		return 0, ttlcache.ErrNotCacheable
	}
	return ttlcache.Key(key), nil
}

// New builds the client and its liveness probe on a cache miss.
//
//nolint:contextcheck // NewClient takes no context yet; the ctx param is plumbed for when it does
func (d cachedDefault) New(context.Context) (ttlcache.Value[*Client], error) {
	c, err := NewClient(d.cfg)
	if err != nil {
		return ttlcache.Value[*Client]{}, err
	}
	closer, _ := c.Transport.(io.Closer)
	liveness := func() int64 {
		m, ok := c.Transport.(interface {
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

// newCachedDefault builds (or reuses) a cached client for cfg. Used only by
// NewDefaultClient; explicit NewClient calls are never cached. Un-hashable cfg
// (reported via cachedDefault.Key) falls back to a direct, uncached build.
func newCachedDefault(cfg Config) (*Client, error) {
	value, release, err := defaultClientCache.GetOrCreate(context.Background(), cachedDefault{cfg: cfg})
	if err != nil {
		return nil, err
	}
	// Return a fresh wrapper per holder sharing the transport and config, each
	// with its own release hook, so one Close maps to exactly one refcount
	// decrement.
	return value.SharedCopy(release), nil
}

// NewClient creates a new client with configuration from cfg.
//
// It will use http://localhost:9200 as the default address.
//
// It will use the OPENSEARCH_URL/ELASTICSEARCH_URL environment variable, if set,
// to configure the addresses; use a comma to separate multiple URLs.
//
// It's an error to set both OPENSEARCH_URL and ELASTICSEARCH_URL.
func NewClient(cfg Config) (*Client, error) {
	var addrs []string

	if len(cfg.Addresses) == 0 {
		envAddress := getAddressFromEnvironment()
		addrs = envAddress
	} else {
		addrs = append(addrs, cfg.Addresses...)
	}

	urls, err := addrsToURLs(addrs)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCreateClient, err)
	}

	if len(urls) == 0 {
		//nolint:errcheck // errcheck exclude ???
		u, _ := url.Parse(defaultURL)
		urls = append(urls, u)
	}

	// Extract credentials from the first URL that has them (only if not already configured)
	extractCredentialsFromURLs(&cfg, urls)

	tp, err := opensearchtransport.New(opensearchtransport.Config{
		URLs:     urls,
		Username: cfg.Username,
		Password: cfg.Password,

		Header: cfg.Header,
		CACert: cfg.CACert,

		InsecureSkipVerify: cfg.InsecureSkipVerify,

		Signer: cfg.Signer,

		RetryOnStatus:        cfg.RetryOnStatus,
		DisableRetry:         cfg.DisableRetry,
		EnableRetryOnTimeout: cfg.EnableRetryOnTimeout,
		MaxRetries:           cfg.MaxRetries,
		RetryBackoff:         cfg.RetryBackoff,
		RequestTimeout:       cfg.RequestTimeout,

		DNSCacheRefresh: cfg.DNSCacheRefresh,
		DNSDialTimeout:  cfg.DNSDialTimeout,
		DNSKeepAlive:    cfg.DNSKeepAlive,
		DNSTimeout:      cfg.DNSTimeout,

		CompressRequestBody: cfg.CompressRequestBody,

		EnableDebugLogger: cfg.EnableDebugLogger,

		DiscoverNodesInterval: cfg.DiscoverNodesInterval,

		HealthCheckTimeout:    cfg.HealthCheckTimeout,
		HealthCheckMaxRetries: cfg.HealthCheckMaxRetries,
		HealthCheckJitter:     cfg.HealthCheckJitter,

		ResurrectTimeoutInitial:      cfg.ResurrectTimeoutInitial,
		ResurrectTimeoutMax:          cfg.ResurrectTimeoutMax,
		ResurrectTimeoutFactorCutoff: cfg.ResurrectTimeoutFactorCutoff,
		MinimumResurrectTimeout:      cfg.MinimumResurrectTimeout,
		JitterScale:                  cfg.JitterScale,
		MaxRetryClusterHealth:        cfg.MaxRetryClusterHealth,
		HealthCheckRequestModifier:   cfg.HealthCheckRequestModifier,

		ActiveListCap:           cfg.ActiveListCap,
		StandbyRotationInterval: cfg.StandbyRotationInterval,
		StandbyRotationCount:    cfg.StandbyRotationCount,
		StandbyPromotionChecks:  cfg.StandbyPromotionChecks,

		Transport:             cfg.Transport,
		Logger:                cfg.Logger,
		Selector:              cfg.Selector,
		Router:                cfg.Router,
		Observer:              cfg.Observer,
		ShardCostConfig:       cfg.ShardCostConfig,
		ConnectionPoolFunc:    cfg.ConnectionPoolFunc,
		AddressResolver:       cfg.AddressResolver,
		MaxAddressResolvers:   cfg.MaxAddressResolvers,
		AddressResolverRunner: cfg.AddressResolverRunner,
		Context:               cfg.Context,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCreateTransport, err)
	}

	client := &Client{
		Transport: tp,
		config:    &cfg,
	}

	// When the caller did not set DiscoverNodesOnStart and no programmatic
	// Router was provided, inherit on-start discovery from OPENSEARCH_GO_ROUTER
	// unless that variable is explicitly falsy. An explicit "false" disables
	// on-start discovery; unset or any non-falsy value enables it, matching the
	// router's on-by-default behavior.
	if cfg.DiscoverNodesOnStart == nil && cfg.Router == nil && !envvars.Falsy(envRouter) {
		t := true
		cfg.DiscoverNodesOnStart = &t
	}

	if cfg.DiscoverNodesOnStart != nil && *cfg.DiscoverNodesOnStart {
		// Use the provided context or fall back to background context.
		// The transport has its own derived child context for scheduled discovery;
		// this is only for the initial one-shot discovery on start.
		discoverCtx := cfg.Context
		if discoverCtx == nil {
			discoverCtx = context.Background()
		}
		go func() {
			start := time.Now()
			if err := client.DiscoverNodes(discoverCtx); err != nil {
				if cfg.Logger != nil {
					//nolint:errcheck // Logger errors are not critical for discovery
					cfg.Logger.LogRoundTrip(nil, nil, err, start, time.Since(start))
				}
			}
		}()
	}

	return client, err
}

func getAddressFromEnvironment() []string {
	return addrsFromEnvironment(envOpenSearchURL)
}

// Close releases the client's background resources. For a cached default client
// it decrements the shared entry's refcount (the worker closes the transport
// once no holder remains and it goes idle). Otherwise it closes the transport
// if it implements io.Closer -- the built-in *opensearchtransport.Transport
// does, canceling pollers and closing idle connections -- and is a no-op for a
// custom Transport that does not.
func (c *Client) Close() error {
	if c.closeFn != nil {
		return c.closeFn()
	}
	if closer, ok := c.Transport.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// SharedCopy mints a per-holder handle onto this client's transport: a new
// Client that shares the same Transport and config but closes via closeFn
// instead of tearing the transport down. The default-client cache stores one
// Client per config and hands every caller a SharedCopy whose closeFn
// decrements the shared entry's refcount, so each caller's Close runs exactly
// once and the transport is torn down only after the last holder releases.
//
// A distinct Client per holder is required because Close dispatches on the
// receiver: callers must not share one Client, or one caller's Close would run
// another's release. This is cache-internal wiring (the two call sites are
// newCachedDefault and opensearchapi's newCachedAPIDefault), exported only so
// the opensearchapi package can reach it, and not part of the stable API.
func (c *Client) SharedCopy(closeFn func() error) *Client {
	return &Client{Transport: c.Transport, config: c.config, closeFn: closeFn}
}

// HashConfig returns a stable hash of cfg's hashable fields and true, or
// (0, false) when cfg carries a field that cannot be compared by value (funcs,
// interfaces, a context, or a custom transport). A false return means "do not
// cache". The hash must stay in sync with Config; TestHashConfig_FieldGuard
// fails loudly when Config grows a field.
func HashConfig(cfg Config) (uint64, bool) {
	if cfg.Transport != nil || cfg.Logger != nil || cfg.Selector != nil ||
		cfg.Router != nil || cfg.Observer != nil || cfg.Signer != nil ||
		cfg.ConnectionPoolFunc != nil || cfg.AddressResolver != nil ||
		cfg.AddressResolverRunner != nil || cfg.RetryBackoff != nil ||
		cfg.HealthCheckRequestModifier != nil || cfg.Context != nil {
		return 0, false
	}

	h := fnv.New64a()
	writeString := func(s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		_, _ = h.Write(n[:])
		_, _ = h.Write([]byte(s))
	}
	writeInt := func(i int64) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(i)) //nolint:gosec // G115: bit reinterpretation for hashing; value semantics irrelevant
		_, _ = h.Write(n[:])
	}
	writeBool := func(b bool) {
		if b {
			writeInt(1)
		} else {
			writeInt(0)
		}
	}

	for _, a := range cfg.Addresses {
		writeString(a)
	}
	writeString("\x00sep-user")
	writeString(cfg.Username)
	writeString(cfg.Password)

	// Header: sort keys and values for determinism.
	keys := make([]string, 0, len(cfg.Header))
	for k := range cfg.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		writeString(k)
		vals := append([]string(nil), cfg.Header[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			writeString(v)
		}
	}

	writeString("\x00sep-cacert")
	writeInt(int64(len(cfg.CACert)))
	_, _ = h.Write(cfg.CACert)
	writeBool(cfg.InsecureSkipVerify)

	writeInt(int64(len(cfg.RetryOnStatus)))
	for _, s := range cfg.RetryOnStatus {
		writeInt(int64(s))
	}
	writeBool(cfg.DisableRetry)
	writeBool(cfg.EnableRetryOnTimeout)
	writeInt(int64(cfg.MaxRetries))
	writeInt(int64(cfg.RequestTimeout))
	writeInt(int64(cfg.DNSCacheRefresh))
	writeInt(int64(cfg.DNSDialTimeout))
	writeInt(int64(cfg.DNSKeepAlive))
	writeInt(int64(cfg.DNSTimeout))
	writeBool(cfg.CompressRequestBody)
	if cfg.DiscoverNodesOnStart != nil {
		writeBool(*cfg.DiscoverNodesOnStart)
	} else {
		writeInt(-1)
	}
	writeInt(int64(cfg.DiscoverNodesInterval))
	writeInt(int64(cfg.HealthCheckTimeout))
	writeInt(int64(cfg.HealthCheckMaxRetries))
	writeInt(int64(cfg.ResurrectTimeoutInitial))
	writeInt(int64(cfg.ResurrectTimeoutMax))
	writeInt(int64(cfg.ResurrectTimeoutFactorCutoff))
	writeInt(int64(cfg.MinimumResurrectTimeout))
	writeInt(int64(cfg.MaxRetryClusterHealth))
	writeInt(int64(cfg.ActiveListCap))
	writeInt(int64(cfg.StandbyRotationInterval))
	writeInt(int64(cfg.StandbyRotationCount))
	writeInt(int64(cfg.StandbyPromotionChecks))
	writeInt(int64(cfg.MaxAddressResolvers))
	writeBool(cfg.EnableDebugLogger)
	writeString(cfg.ShardCostConfig)
	// float64 fields via math.Float64bits.
	writeInt(int64(math.Float64bits(cfg.HealthCheckJitter))) //nolint:gosec // G115: bit reinterpretation for hashing
	writeInt(int64(math.Float64bits(cfg.JitterScale)))       //nolint:gosec // G115: bit reinterpretation for hashing

	return h.Sum64(), true
}

// ParseVersion returns an int64 representation of version.
func ParseVersion(version string) (int64, int64, int64, error) {
	reVersion := regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)`)
	matches := reVersion.FindStringSubmatch(version)
	//nolint:mnd // 4 is the minimum regexp match length
	if len(matches) < 4 {
		return 0, 0, 0, fmt.Errorf("%w: regexp does not match on version string", ErrParseVersion)
	}

	major, err := strconv.ParseInt(matches[1], 10, 0)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("%w: %w", ErrParseVersion, err)
	}

	minor, err := strconv.ParseInt(matches[2], 10, 0)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("%w: %w", ErrParseVersion, err)
	}

	patch, err := strconv.ParseInt(matches[3], 10, 0)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("%w: %w", ErrParseVersion, err)
	}

	return major, minor, patch, nil
}

// Stream delegates to Transport.Stream, returning the raw [http.Response] from
// the underlying [http.RoundTripper]. The caller owns the response body and
// must close it. Use Stream for proxy and streaming use cases where bytes are
// forwarded incrementally; use [Do] when you want a decoded Go value.
func (c *Client) Stream(req *http.Request) (*http.Response, error) {
	if req.Header == nil {
		// Pre-allocate for the headers the transport layer sets on every
		// outgoing request (User-Agent, Authorization, Content-Type,
		// Content-Encoding, etc.) so the map does not have to resize on
		// the hot path.
		const defaultHeaderCount = 8
		req.Header = make(http.Header, defaultHeaderCount)
	}
	return c.Transport.Stream(req)
}

// Do gets and performs the request. It also tries to parse the response into the dataPointer.
//
// On error, Do may return a non-nil *Response alongside a non-nil error. This
// happens when the transport received a response but a subsequent failure
// occurred (a body-read failure during buffering, or an unrelated transport
// error such as context cancellation during retry backoff). Callers that need
// to distinguish a hard transport failure should check resp == nil rather than
// err != nil, and may inspect the returned *Response in the error case. A nil
// *Response always signals that no usable response was produced.
//
// Deprecated: Use [Do] instead, which enforces that dataPointer is a pointer at compile time.
// Client.Do accepts any, so passing a non-pointer compiles but fails at runtime during JSON
// unmarshaling. The method remains fully functional and will not be removed; this annotation
// exists to steer callers toward the safer generic alternative.
func (c *Client) Do(ctx context.Context, method string, req Request, dataPointer any) (*Response, error) {
	httpReq, err := req.GetRequest(method)
	if err != nil {
		return nil, err
	}

	if ctx != nil {
		httpReq = httpReq.WithContext(ctx)
	}

	resp, err := c.Stream(httpReq)
	if resp == nil {
		return nil, err
	}

	// Buffer and close the raw body so the TCP connection returns to the pool.
	// Stream returns the body unbuffered; Do owns it from here.
	if resp.Body != nil {
		body, rerr := io.ReadAll(resp.Body)
		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(body))
		if rerr != nil && err == nil {
			err = fmt.Errorf("%w: %w", opensearchtransport.ErrResponseBodyRead, rerr)
		}
	}

	response := &Response{
		StatusCode: resp.StatusCode,
		Header:     resp.Header,
		Body:       resp.Body,
		render:     &renderCache{},
	}

	if err != nil {
		// Stream may return (resp != nil, err != nil) in two cases: a body-read
		// failure (ErrResponseBodyRead) or an unrelated transport error alongside
		// a response (e.g. context cancellation during retry backoff). Only label
		// the former as ErrReadBody so callers can distinguish the two.
		if errors.Is(err, opensearchtransport.ErrResponseBodyRead) {
			return response, fmt.Errorf("%w, status: %d, err: %w", ErrReadBody, resp.StatusCode, err)
		}
		return response, fmt.Errorf("status: %d, err: %w", resp.StatusCode, err)
	}

	if resp.Body != nil {
		// Buffer the response payload into rawBody so the value-receiver String
		// renders via the rawBody fast-path and never drains Body, and a
		// subsequent ParseError still reads an intact Body.
		data, rerr := io.ReadAll(resp.Body)
		if rerr != nil {
			return response, fmt.Errorf("%w, status: %d, err: %w", ErrReadBody, resp.StatusCode, rerr)
		}

		response.rawBody = data
		response.Body = io.NopCloser(bytes.NewReader(data))

		if dataPointer != nil && !response.IsError() {
			if err := json.Unmarshal(data, dataPointer); err != nil {
				return response, fmt.Errorf("%w, status: %d, body: %s, err: %w", ErrJSONUnmarshalBody, resp.StatusCode, data, err)
			}
		}
	}

	return response, nil
}

// NoBody is a marker type for [Do] calls that expect no response body.
// Pass (*NoBody)(nil) to skip JSON unmarshaling while retaining compile-time
// pointer enforcement.
type NoBody struct{}

// Do is a generic version of [Client.Do] that enforces dataPointer as a pointer at compile time.
// It delegates to [Client.Do] after the type system has guaranteed *T.
//
// A nil dataPointer is forwarded as untyped nil so that [Client.Do] skips
// unmarshalling. This prevents a typed nil (e.g. (*MyResp)(nil)) from being
// widened into a non-nil any interface that would reach [json.Unmarshal].
func Do[T any](ctx context.Context, c *Client, method string, req Request, dataPointer *T) (*Response, error) {
	if dataPointer == nil {
		return c.Do(ctx, method, req, nil)
	}
	return c.Do(ctx, method, req, dataPointer)
}

// Metrics returns the client metrics.
func (c *Client) Metrics() (opensearchtransport.Metrics, error) {
	if mt, ok := c.Transport.(opensearchtransport.Measurable); ok {
		return mt.Metrics()
	}

	return opensearchtransport.Metrics{}, ErrTransportMissingMethodMetrics
}

// DiscoverNodes reloads the client connections by fetching information from the cluster.
func (c *Client) DiscoverNodes(ctx context.Context) error {
	if dt, ok := c.Transport.(opensearchtransport.Discoverable); ok {
		return dt.DiscoverNodes(ctx)
	}

	return ErrTransportMissingMethodDiscoverNodes
}

// GetConfig returns the client configuration.
func (c *Client) GetConfig() *Config {
	return c.config
}

// addrsFromEnvironment returns a list of addresses by splitting
// the given environment variable with comma, or an empty list.
func addrsFromEnvironment(name string) []string {
	var addrs []string

	if envURLs, ok := os.LookupEnv(name); ok && envURLs != "" {
		list := strings.Split(envURLs, ",")
		addrs = make([]string, len(list))

		for idx, u := range list {
			addrs[idx] = strings.TrimSpace(u)
		}
	}

	return addrs
}

// addrsToURLs creates a list of url.URL structures from url list.
func addrsToURLs(addrs []string) ([]*url.URL, error) {
	urls := make([]*url.URL, 0)

	for _, addr := range addrs {
		u, err := url.Parse(strings.TrimRight(addr, "/"))
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrParseURL, err)
		}

		urls = append(urls, u)
	}

	return urls, nil
}

// extractCredentialsFromURLs extracts username and password from the first URL that has them.
// Only extracts credentials that are not already configured in cfg.
func extractCredentialsFromURLs(cfg *Config, urls []*url.URL) {
	if len(urls) == 0 || (cfg.Username != "" && cfg.Password != "") {
		return // No URLs or credentials already fully configured
	}

	for _, u := range urls {
		if u.User == nil {
			continue
		}

		if cfg.Username == "" {
			cfg.Username = u.User.Username()
		}
		if cfg.Password == "" {
			if pw, ok := u.User.Password(); ok {
				cfg.Password = pw
			}
		}
		// Stop after finding the first URL with credentials
		break
	}
}

// ToPointer converts any value to a pointer, mainly used for request parameters
//
// Deprecated: ToPointer will be removed in a future major version. The helper is
// intentionally not part of the public API going forward; consumers within this
// module use the unexported `ptr` defined per-package. Once the module's go
// directive moves to 1.26, callers can drop any wrapper in favor of the native
// new(value) form (e.g. new(false)).
func ToPointer[V any](value V) *V {
	return ptr(value)
}

// ptr returns a pointer to a copy of value. Used for the *T query/body
// parameter pattern. Unexported by design.
//
// Once the module's go directive moves to 1.26, this helper can be deleted
// and call sites can switch to the native new(value) form: new(false).
func ptr[V any](value V) *V {
	return &value
}
