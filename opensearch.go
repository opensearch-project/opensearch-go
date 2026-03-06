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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/opensearch-project/opensearch-go/v4/internal/version"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
	"github.com/opensearch-project/opensearch-go/v4/signer"
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
	envOpenSearchURL   = "OPENSEARCH_URL"
)

// Version returns the package version as a string.
const Version = version.Client

// Error vars
var (
	ErrCreateClient                        = errors.New("cannot create client")
	ErrCreateTransport                     = errors.New("error creating transport")
	ErrParseVersion                        = errors.New("failed to parse opensearch version")
	ErrParseURL                            = errors.New("cannot parse url")
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

	RetryOnStatus        []int // List of status codes for retry. Default: 502, 503, 504.
	DisableRetry         bool  // Default: false.
	EnableRetryOnTimeout bool  // Default: false.
	MaxRetries           int   // Default: 3.

	CompressRequestBody bool // Default: false.

	DiscoverNodesOnStart  bool          // Discover nodes when initializing the client. Default: false.
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

	EnableMetrics     bool // Enable the metrics collection.
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

	// Context for background operations (node discovery, health checks, stats polling).
	// If nil, context.Background() is used. The transport derives a child context from
	// this, so canceling the parent automatically stops all background goroutines.
	// For example, passing t.Context() in tests ensures cleanup when the test ends.
	//nolint:containedctx // Config struct is short-lived, context extracted during New()
	Context context.Context

	// Optional constructor function for a custom ConnectionPool. Default: nil.
	ConnectionPoolFunc func([]*opensearchtransport.Connection, opensearchtransport.Selector) opensearchtransport.ConnectionPool
}

// Client represents the OpenSearch client.
type Client struct {
	Transport opensearchtransport.Interface
	config    *Config
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
	return NewClient(Config{})
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

		Signer: cfg.Signer,

		RetryOnStatus:        cfg.RetryOnStatus,
		DisableRetry:         cfg.DisableRetry,
		EnableRetryOnTimeout: cfg.EnableRetryOnTimeout,
		MaxRetries:           cfg.MaxRetries,
		RetryBackoff:         cfg.RetryBackoff,

		CompressRequestBody: cfg.CompressRequestBody,

		EnableMetrics:     cfg.EnableMetrics,
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

		Transport:          cfg.Transport,
		Logger:             cfg.Logger,
		Selector:           cfg.Selector,
		Router:             cfg.Router,
		Observer:           cfg.Observer,
		ConnectionPoolFunc: cfg.ConnectionPoolFunc,
		Context:            cfg.Context,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCreateTransport, err)
	}

	client := &Client{
		Transport: tp,
		config:    &cfg,
	}

	if cfg.DiscoverNodesOnStart {
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

// Perform delegates to Transport to execute a request and return a response.
func (c *Client) Perform(req *http.Request) (*http.Response, error) {
	// Perform the original request.
	return c.Transport.Perform(req)
}

// Do gets and performs the request. It also tries to parse the response into the dataPointer
func (c *Client) Do(ctx context.Context, req Request, dataPointer any) (*Response, error) {
	httpReq, err := req.GetRequest()
	if err != nil {
		return nil, err
	}

	if ctx != nil {
		httpReq = httpReq.WithContext(ctx)
	}

	//nolint:bodyclose // body got already closed by Perform, this is a nopcloser
	resp, err := c.Perform(httpReq)
	if err != nil {
		return nil, err
	}

	response := &Response{
		StatusCode: resp.StatusCode,
		Body:       resp.Body,
		Header:     resp.Header,
	}

	if dataPointer != nil && resp.Body != nil && !response.IsError() {
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return response, fmt.Errorf("%w, status: %d, err: %w", ErrReadBody, resp.StatusCode, err)
		}

		response.Body = io.NopCloser(bytes.NewReader(data))

		if err := json.Unmarshal(data, dataPointer); err != nil {
			return response, fmt.Errorf("%w, status: %d, body: %s, err: %w", ErrJSONUnmarshalBody, resp.StatusCode, data, err)
		}
	}

	return response, nil
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
func ToPointer[V any](value V) *V {
	return &value
}
