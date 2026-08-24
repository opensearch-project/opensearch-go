// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/errmask"
	"github.com/opensearch-project/opensearch-go/v5/internal/apiutil"
	"github.com/opensearch-project/opensearch-go/v5/internal/envvars"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// resolveErrorMask returns the effective [errmask.ErrorMask] for a Client
// by merging the cfg-supplied mask with the OPENSEARCH_GO_ERROR_MASK env var.
//
// In v5 the default is [errmask.Empty]: every partial-failure
// category is reported as a typed Go error so callers see partial
// failures by default and can mask categories they choose to tolerate.
// The resolver substitutes that default whenever [Config.Errors] is
// nil; a non-nil pointer is honored verbatim, so callers can opt out
// wholesale with `Errors: errmask.New(errmask.All)` or selectively with
// composite masks.
//
// Parsing is liberal (forward-compatible): tokens this binary doesn't
// recognize are skipped and reported via the debug logger, mirroring
// the policy used for OPENSEARCH_GO_ROUTING_CONFIG and
// OPENSEARCH_GO_DISCOVERY_CONFIG. An older client therefore tolerates
// new wrapper bits added by a newer release.
func resolveErrorMask(cfg Config) errmask.ErrorMask {
	var base errmask.ErrorMask
	if cfg.Errors == nil {
		base = errmask.Empty // v5+ default: report every partial-failure category
	} else {
		base = *cfg.Errors
	}
	if v, ok := os.LookupEnv(envvars.ErrorMask); ok && v != "" {
		mask, unknown := errmask.Parse(v, base)
		if len(unknown) > 0 {
			if dl := opensearchtransport.LoadDebugLogger(); dl != nil {
				_ = dl.Logf("%s: ignored unknown tokens %q\n", envvars.ErrorMask, unknown)
			}
		}
		return mask
	}
	return base
}

// Config represents the client configuration
type Config struct {
	Client opensearch.Config

	// Errors masks specific categories of partial-failure errors so they
	// are NOT returned as typed Go errors. A set bit suppresses that
	// category; [errmask.Empty] reports every category.
	//
	//   nil               use this version's default: errmask.Empty
	//                     (report every partial-failure category)
	//   errmask.New()     caller wants every category reported
	//   errmask.New(errmask.All)
	//                     caller wants every category masked
	//
	// [errmask.None] and [errmask.Unknown] are doc-friendly aliases for
	// [errmask.Empty]; all three equal 0. Because the named values are
	// constants (not addressable), use [errmask.New] to build the pointer.
	// The pointer state is what disambiguates "use the default" from "caller
	// chose Empty".
	//
	// The OPENSEARCH_GO_ERROR_MASK environment variable can override
	// this value with comma-separated +/- tokens (see [errmask.Parse]).
	Errors *errmask.ErrorMask
}

// NewClient returns an api client wrapping an [opensearch.Client]. When the
// caller leaves config.Client.Router nil, the underlying transport builds the
// v5 default router itself and opensearch.NewClient enables on-start discovery
// (unless OPENSEARCH_GO_ROUTER is falsy); a caller-provided Router is used
// unchanged.
//
// User-built clients never enter the cache -- only the implicit default path
// (NewDefaultClient, and NewBulkIndexer with no client) is cached, and it rides
// the opensearch default-client cache.
func NewClient(config Config) (*Client, error) {
	return buildClient(config)
}

// buildClient constructs the underlying opensearch client and wraps it with the
// resolved error mask. It never consults a cache. Router selection is left to
// opensearch.NewClient and the transport: a nil Router yields the built-in
// default router plus on-start discovery (unless OPENSEARCH_GO_ROUTER is falsy).
func buildClient(config Config) (*Client, error) {
	rootClient, err := opensearch.NewClient(config.Client)
	if err != nil {
		return nil, err
	}

	return clientInit(rootClient, resolveErrorMask(config)), nil
}

// Close releases the client's background resources by closing the underlying
// opensearch client (see [opensearch.Client.Close]); for a cached default
// client this decrements the shared entry's refcount. Safe on a zero-value.
func (c *Client) Close() error {
	if c.Client != nil {
		return c.Client.Close()
	}
	return nil
}

// NewDefaultClient returns an api client wrapping the shared, cached
// [opensearch.Client] from [opensearch.NewDefaultClient], so identical default
// clients share one transport; the error mask is applied per wrapper. Like
// [opensearch.NewDefaultClient] it uses http://localhost:9200 unless the
// OPENSEARCH_URL/ELASTICSEARCH_URL environment variable is set. The transport
// builds the v5 default router (unless OPENSEARCH_GO_ROUTER is falsy).
func NewDefaultClient() (*Client, error) {
	root, err := opensearch.NewDefaultClient()
	if err != nil {
		return nil, err
	}
	return clientInit(root, resolveErrorMask(Config{})), nil
}

// errMaskWidth is the storage type backing a Client's live error mask.
// It is an alias (not a named type) so the generated clients_gen.go can
// declare the field and call the atomic methods directly, yet the width
// stays an implementation detail: widen it to atomic.Uint64 here if the
// mask ever outgrows 32 bits and no call site changes, because the method
// set is identical. The mask is held behind a pointer on Client so the
// value-receiver API methods (which copy Client per call) all share one
// cell and observe a concurrent SetErrorMask.
type errMaskWidth = atomic.Uint32

// newErrMask returns an error-mask cell initialized to m.
func newErrMask(m errmask.ErrorMask) *errMaskWidth {
	w := new(errMaskWidth)
	w.Store(uint32(m))
	return w
}

// errorMask returns the Client's current effective error mask. Generated
// dispatch code calls this per request so a SetErrorMask applied mid-flight
// takes effect on subsequent calls.
func (c *Client) errorMask() errmask.ErrorMask {
	return errmask.ErrorMask(c.errors.Load())
}

// ErrorMask returns the Client's current partial-failure error mask. Read
// it before [Client.SetErrorMask] to supply the expected prior value.
func (c *Client) ErrorMask() errmask.ErrorMask {
	return c.errorMask()
}

// SetErrorMask atomically replaces the Client's partial-failure error mask,
// but only if its current value still equals prev. It returns an
// [*ErrorMaskConflictError] (without changing the mask) when another writer
// changed it first, so the caller resolves the race by re-reading
// [Client.ErrorMask] and retrying rather than silently clobbering the
// concurrent update. The change is visible to all copies of this Client and
// to in-flight calls that have not yet read the mask.
func (c *Client) SetErrorMask(prev, next errmask.ErrorMask) error {
	if c.errors.CompareAndSwap(uint32(prev), uint32(next)) {
		return nil
	}
	return &ErrorMaskConflictError{Expected: prev, Actual: c.errorMask()}
}

// ErrorMaskConflictError reports that [Client.SetErrorMask] did not apply
// because the mask was changed concurrently: Actual (the observed value)
// did not match Expected (the prev the caller passed).
//
//nolint:errname // the leading "Error" refers to the ErrorMask feature, not error-naming convention
type ErrorMaskConflictError struct {
	Expected errmask.ErrorMask
	Actual   errmask.ErrorMask
}

func (e *ErrorMaskConflictError) Error() string {
	return fmt.Sprintf(
		"opensearchapi: SetErrorMask conflict: expected mask %s, found %s",
		e.Expected, e.Actual,
	)
}

// Clone returns a new Client that shares this Client's underlying
// opensearch.Client -- and therefore its connection pool, auth, transport,
// and router -- but carries an independent error-mask cell seeded from the
// current mask. Mutating the clone's mask via [Client.SetErrorMask] does
// not affect the original, mirroring [net/http.Transport.Clone]'s "same
// config, independent instance" contract.
//
// Use it to derive a client that talks over the same connection but reports
// (or tolerates) a different set of partial-failure categories. To share the
// transport with plugin clients instead, pass the exported [Client.Client]
// field to their constructors.
func (c *Client) Clone() *Client {
	return clientInit(c.Client, c.errorMask())
}

// request calls [opensearch.Execute] and checks the response for OpenSearch API errors.
//
// [opensearch.Execute] routes through [opensearchtransport.Transport.Request] and buffers
// the response body, so resp.Body here is already an [io.NopCloser] over a
// [bytes.Reader] -- the connection has been drained and returned to the pool.
// The helper only needs to translate IsError into a typed error.
func request[T any](ctx context.Context, c *Client, method string, req opensearch.Request, dataPointer *T) (*opensearch.Response, error) {
	resp, err := opensearch.Execute(ctx, c.Client, method, req, dataPointer)
	if err != nil {
		return nil, err
	}

	if resp.IsError() {
		if dataPointer != nil {
			return resp, opensearch.ParseError(resp)
		}
		return resp, fmt.Errorf("status: %s", resp.Status())
	}

	return resp, nil
}

// formatDuration converts duration to a string in the format accepted by
// OpenSearch. Delegates to apiutil.FormatDuration so the encoding lives in a
// single place; generated plugin packages reference apiutil directly.
func formatDuration(d time.Duration) string {
	return apiutil.FormatDuration(d)
}
