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
	"strings"
	"sync"
	"time"

	"github.com/opensearch-project/opensearch-go/v4/internal/version"
	"github.com/opensearch-project/opensearch-go/v4/signer"
)

const (
	// Version returns the package version as a string.
	Version                   = version.Client
	defaultMaxRetries         = 6
	defaultHealthCheckTimeout = 5 * time.Second
	defaultRetryJitter        = 0.1
)

var (
	reGoVersion          = regexp.MustCompile(`go(\d+\.\d+\..+)`)
	errHealthCheckFailed = errors.New("connection health check error")
)

// getConnectionFromPool gets a connection and handles client locking internally.
func getConnectionFromPool(c *Client, req *http.Request) (*Connection, error) {
	if c.router != nil {
		// Use request routing
		return c.router.Route(req.Context(), req)
	}

	// Fall back to original connection pool behavior
	c.mu.RLock()
	connectionPool := c.mu.connectionPool
	c.mu.RUnlock()
	return connectionPool.Next()
}

// Interface defines the interface for HTTP client.
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
	Password string

	Header http.Header
	CACert []byte

	Signer signer.Signer

	RetryOnStatus        []int
	DisableRetry         bool
	EnableRetryOnTimeout bool
	MaxRetries           int
	RetryBackoff         func(attempt int) time.Duration

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

	// ResurrectTimeoutInitial sets the initial timeout for resurrecting dead connections.
	// 0 = use default (60s), >0 = explicit timeout
	// Default: 60s
	ResurrectTimeoutInitial time.Duration

	// ResurrectTimeoutFactorCutoff sets the exponential backoff cutoff factor for dead connection resurrection.
	// 0 = use default (5), >0 = explicit cutoff factor
	// Default: 5
	ResurrectTimeoutFactorCutoff int

	Transport http.RoundTripper
	Logger    Logger
	Selector  Selector
	Router    Router // Optional router for cluster-aware request routing

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
	discoverNodesInterval time.Duration

	includeDedicatedClusterManagers bool
	discoveryHealthCheckRetries     int
	healthCheckTimeout              time.Duration
	healthCheckMaxRetries           int
	healthCheckJitter               float64

	resurrectTimeoutInitial      time.Duration
	resurrectTimeoutFactorCutoff int

	compressRequestBody  bool
	pooledGzipCompressor *gzipCompressor

	metrics *metrics

	transport http.RoundTripper
	logger    Logger
	selector  Selector
	router    Router // Optional router for cluster-aware routing
	poolFunc  func([]*Connection, Selector) ConnectionPool

	mu struct {
		sync.RWMutex
		connectionPool      ConnectionPool // Used for both single-node and multi-node
		discoverNodesTimer  *time.Timer
		discoveryInProgress bool // Prevents concurrent discovery operations
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

	if len(cfg.RetryOnStatus) == 0 && cfg.RetryOnStatus == nil {
		cfg.RetryOnStatus = []int{502, 503, 504}
	}

	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = defaultMaxRetries
	}

	if cfg.DiscoveryHealthCheckRetries == 0 {
		cfg.DiscoveryHealthCheckRetries = 3
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
	var resurrectTimeoutFactorCutoff int

	switch {
	case cfg.ResurrectTimeoutInitial == 0:
		resurrectTimeoutInitial = defaultResurrectTimeoutInitial
	default:
		resurrectTimeoutInitial = cfg.ResurrectTimeoutInitial
	}

	switch {
	case cfg.ResurrectTimeoutFactorCutoff == 0:
		resurrectTimeoutFactorCutoff = defaultResurrectTimeoutFactorCutoff
	default:
		resurrectTimeoutFactorCutoff = cfg.ResurrectTimeoutFactorCutoff
	}

	conns := make([]*Connection, len(cfg.URLs))
	for idx, u := range cfg.URLs {
		conns[idx] = &Connection{URL: u}
	}

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
		discoverNodesInterval: cfg.DiscoverNodesInterval,

		includeDedicatedClusterManagers: cfg.IncludeDedicatedClusterManagers,
		discoveryHealthCheckRetries:     cfg.DiscoveryHealthCheckRetries,
		healthCheckTimeout:              healthCheckTimeout,
		healthCheckMaxRetries:           healthCheckMaxRetries,
		healthCheckJitter:               healthCheckJitter,

		resurrectTimeoutInitial:      resurrectTimeoutInitial,
		resurrectTimeoutFactorCutoff: resurrectTimeoutFactorCutoff,

		compressRequestBody: cfg.CompressRequestBody,

		transport: cfg.Transport,
		logger:    cfg.Logger,
		router:    cfg.Router,
		selector:  cfg.Selector,
		poolFunc:  cfg.ConnectionPoolFunc,
	}

	client.userAgent = initUserAgent()

	// Initialize connection pool using the same logic as the public API
	if client.poolFunc != nil {
		client.mu.connectionPool = client.poolFunc(conns, cfg.Selector)
	} else {
		// Use client-configured timeout settings for the main connection pool
		if len(conns) == 1 {
			client.mu.connectionPool = &singleConnectionPool{connection: conns[0]}
		} else {
			pool := &statusConnectionPool{
				resurrectTimeoutInitial:      resurrectTimeoutInitial,
				resurrectTimeoutFactorCutoff: resurrectTimeoutFactorCutoff,
			}
			pool.mu.live = conns
			pool.mu.dead = []*Connection{}
			client.mu.connectionPool = pool
		}
	}

	if cfg.EnableDebugLogger {
		debugLogger = &debuggingLogger{Output: os.Stdout}
	}

	if cfg.EnableMetrics {
		client.metrics = &metrics{}
		client.metrics.mu.responses = make(map[int]int)

		if len(conns) == 1 {
			// Single node - assign metrics to connection pool
			if pool, ok := client.mu.connectionPool.(*singleConnectionPool); ok {
				pool.metrics = client.metrics
			} else {
				return nil, fmt.Errorf("unexpected connection pool type for single node: %T", client.mu.connectionPool)
			}
		} else {
			// Multi-node - assign metrics to status connection pool
			if pool, ok := client.mu.connectionPool.(*statusConnectionPool); ok {
				pool.metrics = client.metrics
			}
		}
	}

	if client.discoverNodesInterval > 0 {
		time.AfterFunc(client.discoverNodesInterval, func() {
			client.scheduleDiscoverNodes()
		})
	}

	if cfg.CompressRequestBody {
		client.pooledGzipCompressor = newGzipCompressor()
	}

	// Configure pool factories for all policies in the router
	if client.router != nil {
		// Use type assertion to check if the router (which is a Policy) implements poolFactoryConfigurable
		if configurablePolicy, ok := client.router.(poolFactoryConfigurable); ok {
			if err := configurablePolicy.configurePoolFactories(client.createPoolFactory()); err != nil {
				return nil, fmt.Errorf("failed to configure pool factories: %w", err)
			}
		}
	}

	return &client, nil
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
				// We have to return a new reader each time so that retries don't read from an already-consumed body.
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
			shouldRetry     bool
			shouldCloseBody bool
		)

		if c.router != nil {
			conn, err = c.router.Route(req.Context(), req)
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
			return nil, fmt.Errorf("cannot get connection: %w", err)
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
		start := time.Now().UTC()
		res, err = c.transport.RoundTrip(req)
		dur := time.Since(start)

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

			// Report the connection as unsuccessful
			// Always use connection pool OnFailure with locking
			c.mu.Lock()
			//nolint:errcheck // Questionable if the function even returns an error
			c.mu.connectionPool.OnFailure(conn)
			c.mu.Unlock()

			// Retry on EOF errors
			if errors.Is(err, io.EOF) {
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
			c.mu.Lock()
			c.mu.connectionPool.OnSuccess(conn)
			c.mu.Unlock()
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
		}

		// Break if retry should not be performed
		if !shouldRetry {
			break
		}

		// Drain and close body when retrying after response
		if shouldCloseBody && i < c.maxRetries {
			if res.Body != nil {
				//nolint:errcheck // undexpected but okay if it failes
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
	// Read, close and replace the http response body to close the connection
	if res != nil && res.Body != nil {
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err == nil {
			res.Body = io.NopCloser(bytes.NewReader(body))
		}
	}

	// TODO(karmi): Wrap error
	return res, err
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

	if u.Path != "" {
		var b strings.Builder
		b.Grow(len(u.Path) + len(req.URL.Path))
		b.WriteString(u.Path)
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

// defaultHealthCheck validates that the target URL is responding as an OpenSearch node
// by making a GET / request and verifying basic response structure.
// This is the built-in health check implementation with a default timeout.
func (c *Client) defaultHealthCheck(ctx context.Context, url *url.URL) (*http.Response, error) {
	var healthCtx context.Context
	var cancel context.CancelFunc

	// Handle timeout configuration
	if c.healthCheckTimeout > 0 {
		healthCtx, cancel = context.WithTimeout(ctx, c.healthCheckTimeout)
		defer cancel()
	} else {
		healthCtx = ctx // No timeout
	}

	req, err := http.NewRequestWithContext(healthCtx, http.MethodGet, "/", nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errHealthCheckFailed, err)
	}

	c.setReqURL(url, req)
	c.setReqAuth(url, req)
	c.setReqUserAgent(req)

	// Execute the request
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

	// Read and parse the response
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

	// Minimal validation - just check that core fields exist
	if info.Name == "" || info.ClusterName == "" || info.Version.Number == "" {
		return nil, fmt.Errorf("%w: invalid response structure", errHealthCheckFailed)
	}

	// Restore body for caller and return the response
	res.Body = io.NopCloser(bytes.NewReader(body))
	return res, nil
}

// backoffRetry performs retries with exponential backoff and jitter.
// It calls the provided function up to maxRetries times with delays between attempts.
// Returns nil on success, or the last error encountered.
func backoffRetry(baseDelay time.Duration, maxRetries int, jitter float64, fn func() error) error {
	if maxRetries <= 0 {
		return fn() // Single attempt when retries disabled
	}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := fn(); err == nil {
			return nil // Success
		} else {
			lastErr = err
		}

		// If this is not the last attempt, wait before retrying
		if attempt < maxRetries-1 && baseDelay > 0 {
			// Exponential backoff: base delay * 2^attempt
			delay := time.Duration(int64(baseDelay) * (1 << uint(attempt)))

			// Apply jitter to avoid thundering herd
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
func (c *Client) healthCheckWithRetries(conn *Connection, maxRetries int) bool {
	ctx := context.Background()

	// Use the provided maxRetries parameter, but respect client's timeout/jitter config
	baseDelay := c.healthCheckTimeout / 2 // Start with half the timeout as base delay
	if baseDelay <= 0 {
		baseDelay = defaultHealthCheckTimeout / 2 // Fallback if timeout is disabled
	}

	err := backoffRetry(baseDelay, maxRetries, c.healthCheckJitter, func() error {
		res, err := c.defaultHealthCheck(ctx, conn.URL)
		if err != nil {
			return err
		}
		if res != nil && res.Body != nil {
			res.Body.Close()
		}
		return nil
	})

	if err == nil {
		conn.markAsHealthyWithLock()
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

// promoteConnectionPoolWithLock converts a singleConnectionPool to statusConnectionPool while preserving
// metrics, timeout settings, and client configuration. MUST be called while holding client write lock.
// Returns existing pool unchanged if already a statusConnectionPool.
func (c *Client) promoteConnectionPoolWithLock(liveConnections, deadConnections []*Connection) *statusConnectionPool {
	switch currentPool := c.mu.connectionPool.(type) {
	case *singleConnectionPool:
		// Promote from single to multi-node pool using client-configured timeouts
		metrics := currentPool.metrics

		filteredLive := make([]*Connection, 0, len(liveConnections))
		filteredDead := make([]*Connection, 0, len(deadConnections))
		c.applyConnectionFiltering(liveConnections, deadConnections, &filteredLive, &filteredDead)

		// Use client-configured timeouts (from Config or defaults)
		pool := &statusConnectionPool{
			resurrectTimeoutInitial:      c.resurrectTimeoutInitial,
			resurrectTimeoutFactorCutoff: c.resurrectTimeoutFactorCutoff,
			metrics:                      metrics,
		}
		pool.mu.live = filteredLive
		pool.mu.dead = filteredDead

		if debugLogger != nil {
			debugLogger.Logf("Promoted singleConnectionPool to statusConnectionPool: %d live, %d dead connections (timeouts: %v, %d)\n",
				len(pool.mu.live), len(pool.mu.dead), pool.resurrectTimeoutInitial, pool.resurrectTimeoutFactorCutoff)
		}

		return pool

	case *statusConnectionPool:
		// Already a statusConnectionPool - return unchanged
		return currentPool

	default:
		panic(fmt.Sprintf("unsupported connection pool type for promotion: %T", currentPool))
	}
}

// demoteConnectionPoolWithLock converts a statusConnectionPool to singleConnectionPool while preserving
// metrics and selecting the best available connection. MUST be called while holding client write lock.
// Returns existing pool unchanged if already a singleConnectionPool.
func (c *Client) demoteConnectionPoolWithLock() *singleConnectionPool {
	switch currentPool := c.mu.connectionPool.(type) {
	case *statusConnectionPool:
		// Demote from multi-node to single-node pool
		metrics := currentPool.metrics

		currentPool.mu.RLock()
		var connection *Connection

		if len(currentPool.mu.live) > 0 {
			connection = currentPool.mu.live[0]
			if debugLogger != nil {
				debugLogger.Logf("Demoting statusConnectionPool to singleConnectionPool using live connection: %s\n", connection.URL)
			}
		} else if len(currentPool.mu.dead) > 0 {
			connection = currentPool.mu.dead[0]
			if debugLogger != nil {
				debugLogger.Logf("Demoting statusConnectionPool to singleConnectionPool using dead connection: %s\n", connection.URL)
			}
		} else if debugLogger != nil {
			debugLogger.Logf("Warning: Demoting statusConnectionPool with no connections available\n")
		}

		currentPool.mu.RUnlock()

		return &singleConnectionPool{
			connection: connection,
			metrics:    metrics,
		}

	case *singleConnectionPool:
		// Already a singleConnectionPool - return unchanged
		return currentPool

	default:
		panic(fmt.Sprintf("unsupported connection pool type for demotion: %T", currentPool))
	}
}

// applyConnectionFiltering applies client-level filtering for dedicated cluster managers
func (c *Client) applyConnectionFiltering(liveConnections, deadConnections []*Connection, filteredLive, filteredDead *[]*Connection) {
	for _, conn := range liveConnections {
		if !c.includeDedicatedClusterManagers && conn.Roles.isDedicatedClusterManager() {
			if debugLogger != nil {
				debugLogger.Logf("Excluding dedicated cluster manager %q from connection pool\n", conn.Name)
			}
			continue
		}
		*filteredLive = append(*filteredLive, conn)
	}

	for _, conn := range deadConnections {
		if !c.includeDedicatedClusterManagers && conn.Roles.isDedicatedClusterManager() {
			continue
		}
		*filteredDead = append(*filteredDead, conn)
	}
}

// createPoolFactory returns a factory function that creates statusConnectionPool instances
// with this client's configured resurrection timeout settings.
func (c *Client) createPoolFactory() func() *statusConnectionPool {
	return func() *statusConnectionPool {
		return &statusConnectionPool{
			resurrectTimeoutInitial:      c.resurrectTimeoutInitial,
			resurrectTimeoutFactorCutoff: c.resurrectTimeoutFactorCutoff,
		}
	}
}
