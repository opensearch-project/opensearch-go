// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
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

// defaultRouter returns the v5 default Router. v5 opts
// every caller into intelligent request routing: role-aware
// dispatch with RTT-based scoring, congestion-window AIMD, and shard-
// cost weighting. Callers who want different routing semantics set
// `config.Client.Router` to their own [opensearchtransport.Router]
// before calling [NewClient]; setting OPENSEARCH_GO_ROUTER=false
// suppresses the default-router injection entirely (the caller's nil
// Router stays nil, matching v4 behavior).
func defaultRouter() (opensearchtransport.Router, error) {
	return opensearchtransport.NewDefaultRouter()
}

// NewClient returns an api client. Routing rule:
//
//   - config.Client.Router != nil: use the caller's Router unchanged.
//   - config.Client.Router == nil and OPENSEARCH_GO_ROUTER=false: leave
//     Router nil (env-driven opt-out).
//   - config.Client.Router == nil otherwise (env unset or truthy):
//     inject [opensearchtransport.NewDefaultRouter] -- v5 opts
//     every caller into intelligent request routing by default.
//
// When the default-router injection runs, NewClient also sets
// DiscoverNodesOnStart=true (unless the caller already picked a value or
// OPENSEARCH_GO_ROUTER=false). The underlying opensearch.NewClient skips
// its own env-driven discovery path when Router != nil, so this replicates
// the on-start discovery side-effect for the injected router.
//
// The OPENSEARCH_GO_ROUTER variable is the same one used to control
// on-start discovery; it doubles as the env-var opt-out for the
// default-router injection. Setting it to "false" disables both the
// default router and on-start discovery.
func NewClient(config Config) (*Client, error) {
	if config.Client.Router == nil && !envvars.Falsy(envvars.Router) {
		router, err := defaultRouter()
		if err != nil {
			return nil, fmt.Errorf("opensearchapi: build default router: %w", err)
		}
		config.Client.Router = router

		// Enable on-start discovery for the injected router unless the
		// caller already picked a value. The opensearch.NewClient
		// env-discovery path at the same env-var key only fires when
		// Router == nil; that condition is now false because we just
		// injected one, so replicate the side-effect here.
		if config.Client.DiscoverNodesOnStart == nil && !envvars.Falsy(envvars.Router) {
			t := true
			config.Client.DiscoverNodesOnStart = &t
		}
	}

	rootClient, err := opensearch.NewClient(config.Client)
	if err != nil {
		return nil, err
	}

	return clientInit(rootClient, resolveErrorMask(config)), nil
}

// NewDefaultClient returns an api client using defaults: localhost on
// the default scheme/port plus the default router (see [NewClient] for
// the router-injection rule).
func NewDefaultClient() (*Client, error) {
	defaultAddress := opensearch.DefaultScheme + "://" + net.JoinHostPort(opensearch.DefaultHost, strconv.Itoa(opensearch.DefaultPort))
	return NewClient(Config{
		Client: opensearch.Config{
			Addresses: []string{defaultAddress},
		},
	})
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

// do calls [opensearch.Do] and checks the response for OpenSearch API errors.
//
// [opensearch.Do] routes through the buffered [opensearchtransport.Transport.Perform],
// so resp.Body here is already an [io.NopCloser] over a [bytes.Reader] -- the
// connection has been drained and returned to the pool. The helper only needs
// to translate IsError into a typed error.
func do[T any](ctx context.Context, c *Client, method string, req opensearch.Request, dataPointer *T) (*opensearch.Response, error) {
	resp, err := opensearch.Do(ctx, c.Client, method, req, dataPointer)
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
