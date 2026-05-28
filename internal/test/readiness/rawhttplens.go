// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package readiness

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// rawHTTPLensCheck observes a single endpoint URL with a plain http.Client
// and advances one synthetic node through LayerTCP and LayerHTTP. Used when
// the caller has not yet constructed an opensearchapi.Client (e.g. transport-level
// pre-flight checks).
//
// Because the lens cannot enumerate node identities, it tracks one
// synthetic node keyed by host:port. Targets above LayerHTTP are not
// supported.
type rawHTTPLensCheck struct {
	target     *url.URL
	httpClient *http.Client
	prepareReq func(*http.Request)
}

// Layer reports the deepest layer this check can advance a node to.
func (c *rawHTTPLensCheck) Layer() State { return LayerHTTP }

// Probe attempts a TCP dial then a GET against the target URL. Auth
// failures are wrapped with ErrTerminal so misconfigured SECURE_INTEGRATION
// fails fast; everything else is recorded as a transient error and the
// harness continues to poll.
func (c *rawHTTPLensCheck) Probe(ctx context.Context, cluster *Cluster) error {
	host := c.target.Host
	if c.target.Port() == "" {
		switch c.target.Scheme {
		case "https":
			host = c.target.Hostname() + ":443"
		default:
			host = c.target.Hostname() + ":80"
		}
	}
	id := "endpoint-" + host
	node := cluster.Node(id, host, host)

	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		cluster.RecordError(err)
		return nil
	}
	_ = conn.Close()
	node.Advance(LayerTCP, "tcp dial ok")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.target.String(), nil)
	if err != nil {
		return AsTerminal(fmt.Errorf("build request: %w", err))
	}
	if c.prepareReq != nil {
		c.prepareReq(req)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if IsPermanentAuthErr(err) {
			return AsTerminal(fmt.Errorf(
				"authentication rejected (verify SECURE_INTEGRATION matches cluster scheme): %w", err))
		}
		cluster.RecordError(err)
		return nil
	}
	defer func() {
		// Drain to EOF (capped) before close so net/http can reuse the
		// connection; without this every probe per tick opens a fresh
		// TLS handshake against the cluster.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
	}()

	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return AsTerminal(fmt.Errorf(
			"authentication rejected: HTTP %d (verify SECURE_INTEGRATION matches cluster scheme)",
			resp.StatusCode))
	default:
		node.Advance(LayerHTTP, fmt.Sprintf("GET %s -> %d", c.target.Path, resp.StatusCode))
	}
	return nil
}

// IsPermanentAuthErr reports whether err signals an authentication or
// authorization failure that will not heal under continued polling.
// Matches on "Unauthorized" and "Forbidden" substrings inside the error
// message; deliberately conservative to avoid false positives. The
// readiness package can't import opensearch's typed errors directly
// without an import cycle (consumers like opensearchtransport tests
// import readiness, and opensearch imports opensearchtransport), so
// callers with access to typed errors should layer errors.As checks
// on top of this fallback.
func IsPermanentAuthErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Unauthorized") ||
		strings.Contains(msg, "Forbidden")
}

// WithRawHTTP registers a layer check that advances one synthetic
// "endpoint-<host:port>" node through LayerTCP and LayerHTTP using a
// plain http.Client. Use this when an opensearchapi.Client is not yet available.
//
// prepareReq is called on every probe request before it is sent and is
// the caller's hook for setting BasicAuth or other headers. Pass nil if
// no per-request mutation is needed.
//
// The check observes only the supplied endpoint, so callers should pair
// it with WithExpectedNodes(1) and a target no higher than LayerHTTP.
func WithRawHTTP(target *url.URL, httpClient *http.Client, prepareReq func(*http.Request)) Option {
	return func(c *config) {
		c.layerChecks = append(c.layerChecks, &rawHTTPLensCheck{
			target:     target,
			httpClient: httpClient,
			prepareReq: prepareReq,
		})
	}
}
