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

// NewClient returns an api client
func NewClient(config Config) (*Client, error) {
	rootClient, err := opensearch.NewClient(config.Client)
	if err != nil {
		return nil, err
	}

	return clientInit(rootClient, resolveErrorMask(config)), nil
}

// NewDefaultClient returns an api client using defaults
func NewDefaultClient() (*Client, error) {
	defaultAddress := opensearch.DefaultScheme + "://" + net.JoinHostPort(opensearch.DefaultHost, strconv.Itoa(opensearch.DefaultPort))
	rootClient, err := opensearch.NewClient(opensearch.Config{
		Addresses: []string{defaultAddress},
	})
	if err != nil {
		return nil, err
	}

	return clientInit(rootClient, resolveErrorMask(Config{})), nil
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
