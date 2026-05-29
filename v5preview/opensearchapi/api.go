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
	"time"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/internal/apiutil"
	"github.com/opensearch-project/opensearch-go/v4/internal/envvars"
	"github.com/opensearch-project/opensearch-go/v4/internal/errmask"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
)

// resolveErrorMask returns the effective [errmask.ErrorMask] for a Client
// by merging the cfg-supplied mask with the OPENSEARCH_GO_ERROR_MASK env var.
//
// In v5preview the default is [errmask.Empty]: every partial-failure
// category is reported as a typed Go error so callers see partial
// failures by default and can mask categories they choose to tolerate.
// The resolver substitutes that default whenever [Config.Errors] is
// nil; a non-nil pointer is honored verbatim, so callers can opt out
// wholesale with `Errors: &errmask.All` or selectively with composite
// masks.
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
	//   &errmask.Empty    caller wants every category reported
	//   &errmask.All      caller wants every category masked
	//
	// [errmask.None] and [errmask.Unknown] are doc-friendly aliases for
	// [errmask.Empty]; all three equal 0. The pointer state is what
	// disambiguates "use the default" from "caller chose Empty".
	//
	// The OPENSEARCH_GO_ERROR_MASK environment variable can override
	// this value with comma-separated +/- tokens (see [errmask.Parse]).
	Errors *errmask.ErrorMask
}

// defaultRouter returns the v5preview default Router. v5preview opts
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
//     inject [opensearchtransport.NewDefaultRouter] -- v5preview opts
//     every caller into intelligent request routing by default.
//
// When the default-router injection runs AND OPENSEARCH_GO_ROUTER is
// explicitly truthy, NewClient also sets DiscoverNodesOnStart=true
// (unless the caller already picked a value). This preserves v4's
// truthy-env semantics: a v4 caller running with
// OPENSEARCH_GO_ROUTER=true relied on auto-discovery as a side
// effect, which would otherwise be silently dropped on v5preview
// because the underlying opensearch.NewClient skips its env-driven
// discovery path when Router != nil.
//
// The OPENSEARCH_GO_ROUTER variable is the same one v4 uses to opt
// into on-start discovery; in v5preview it doubles as the env-var
// opt-out for the default-router injection. The unset behavior is
// the only divergence from v4: v4 does nothing on unset, v5preview
// injects the default router.
func NewClient(config Config) (*Client, error) {
	if config.Client.Router == nil && !envvars.Falsy(envvars.Router) {
		router, err := defaultRouter()
		if err != nil {
			return nil, fmt.Errorf("v5preview/opensearchapi: build default router: %w", err)
		}
		config.Client.Router = router

		// Preserve v4's OPENSEARCH_GO_ROUTER=true side-effect: enable
		// on-start discovery when env is truthy and caller did not
		// pick a value. The opensearch.NewClient env-discovery path
		// at the same env-var key only fires when Router == nil; that
		// condition is now false because we just injected one, so
		// replicate the side-effect here for v4 migrators.
		if config.Client.DiscoverNodesOnStart == nil && envvars.Truthy(envvars.Router) {
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

// NewFromClient creates an api client from an existing opensearch.Client.
// In v4 this preserves the legacy "mask everything" default; use NewClient
// with Config to enable partial-failure errors.
func NewFromClient(client *opensearch.Client) *Client {
	return clientInit(client, resolveErrorMask(Config{}))
}

// NewFromClientWithErrors creates an api client from an existing
// opensearch.Client with every partial-failure category reported as an error.
func NewFromClientWithErrors(client *opensearch.Client) *Client {
	none := errmask.None
	return clientInit(client, resolveErrorMask(Config{Errors: &none}))
}

// do calls [opensearch.Do] and checks the response for OpenSearch API errors.
func do[T any](ctx context.Context, c *Client, method string, req opensearch.Request, dataPointer *T) (*opensearch.Response, error) {
	resp, err := opensearch.Do(ctx, c.Client, method, req, dataPointer)
	if err != nil {
		return nil, err
	}

	if resp.IsError() {
		if dataPointer != nil {
			return resp, opensearch.ParseError(resp)
		} else {
			return resp, fmt.Errorf("status: %s", resp.Status())
		}
	}

	return resp, nil
}

// formatDuration converts duration to a string in the format accepted by
// OpenSearch. Delegates to apiutil.FormatDuration so the encoding lives in a
// single place; generated plugin packages reference apiutil directly.
func formatDuration(d time.Duration) string {
	return apiutil.FormatDuration(d)
}
