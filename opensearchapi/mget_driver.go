// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	opensearch "github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/internal/envvars"
	"github.com/opensearch-project/opensearch-go/v5/internal/scheduler"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// MGetDriver selects how [DocumentClient.MGet] fetches its documents.
type MGetDriver uint8

const (
	// MGetDriverDefault leaves the choice to the OPENSEARCH_GO_MGET environment
	// variable and, when that is not set, to this release's default,
	// MGetDriverCoordinator.
	MGetDriverDefault MGetDriver = iota

	// MGetDriverCoordinator sends each mget as one request. The node that
	// receives it splits the documents by shard, fetches each shard's from a
	// node that holds it, and answers for all of them.
	MGetDriverCoordinator

	// MGetDriverDirect splits each mget by shard in the client, using the shard
	// map the transport keeps, and sends each node only the documents it holds,
	// as concurrent requests the node answers from its own shard copies. The
	// answers are put back together in the order the documents were asked for,
	// so the caller gets the response the cluster would have given.
	//
	// A document the client cannot place -- one in an index whose shard map is
	// not known yet, named through an alias or a data stream, or with no _index
	// -- is fetched through a coordinating node in a request of its own. So is
	// every document of an mget the client cannot split as the cluster would:
	// one with a preference, a filter_path, a format or a source parameter, a raw
	// BodyReader, or ids in place of docs.
	MGetDriverDirect
)

// defaultMGetDriver is the driver MGetDriverDefault resolves to when
// OPENSEARCH_GO_MGET is not set.
const defaultMGetDriver = MGetDriverCoordinator

// String returns the driver as OPENSEARCH_GO_MGET spells it.
func (d MGetDriver) String() string {
	switch d {
	case MGetDriverDefault:
		return "default"
	case MGetDriverCoordinator:
		return "coordinator"
	case MGetDriverDirect:
		return "direct"
	default:
		return fmt.Sprintf("MGetDriver(%d)", uint8(d))
	}
}

// ParseMGetDriver parses a driver as OPENSEARCH_GO_MGET spells it: "direct"
// or "coordinator", in any case. The empty string is MGetDriverDefault.
func ParseMGetDriver(s string) (MGetDriver, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return MGetDriverDefault, nil
	case "coordinator":
		return MGetDriverCoordinator, nil
	case "direct":
		return MGetDriverDirect, nil
	default:
		return MGetDriverDefault, fmt.Errorf("unknown mget driver %q, want direct or coordinator", s)
	}
}

// MGetConfig configures how [DocumentClient.MGet] fetches documents. Set it as
// [Config].MGet.
type MGetConfig struct {
	// Driver selects the driver. OPENSEARCH_GO_MGET overrides it, and
	// [WithMGetDriver] overrides both for one call.
	Driver MGetDriver

	// Observer, when set, is told about every request MGetDriverDirect sends and
	// about each mget it completes.
	Observer MGetObserver

	// MaxDocsPerRequest is the most documents MGetDriverDirect puts in one
	// request to a node; a node's share of a larger mget goes as several
	// requests. 0 means 500.
	MaxDocsPerRequest int

	// MaxAttempts is how many requests MGetDriverDirect sends a document in
	// directly when a node rejects it, fails, or answers that the shard is not
	// available, before it asks a coordinating node for it instead. 0 means 3.
	MaxAttempts int

	// MaxInFlightDocsPerNode bounds the documents MGetDriverDirect has
	// outstanding to one node at once, across every mget the client sends. A
	// node that rejects requests has the bound halved, and it grows back as the
	// node keeps up. 0 means 8 times MaxDocsPerRequest.
	MaxInFlightDocsPerNode int
}

const (
	defaultMGetDocsPerRequest  = 500
	defaultMGetAttempts        = 3
	defaultMGetInFlightPerDocs = 8
	mgetWindowBackoff          = 0.5
)

// mgetDriverKey is the context key for [WithMGetDriver].
type mgetDriverKey struct{}

// WithMGetDriver returns a context that has [DocumentClient.MGet] use driver for
// calls made with it, whatever the client's configuration and
// OPENSEARCH_GO_MGET say. MGetDriverDefault leaves the client's choice in
// place.
func WithMGetDriver(ctx context.Context, driver MGetDriver) context.Context {
	return context.WithValue(ctx, mgetDriverKey{}, driver)
}

// shardLookup is what the direct driver needs of a transport;
// [opensearchtransport.Transport] provides it.
type shardLookup interface {
	LookupShard(index, key string, dst *[]*opensearchtransport.ShardTarget) error
}

// newMGetDriver builds the driver a client's MGet tries first. It is built even
// when the coordinator driver is selected, so that WithMGetDriver can choose the
// direct one for a call; it fetches nothing directly when the transport keeps
// no shard map.
func newMGetDriver(cfg MGetConfig, root *opensearch.Client) *mgetDriver {
	driver := cfg.Driver
	if v, ok := os.LookupEnv(envvars.MGet); ok {
		if parsed, err := ParseMGetDriver(v); err == nil && parsed != MGetDriverDefault {
			driver = parsed
		}
	}
	if driver == MGetDriverDefault {
		driver = defaultMGetDriver
	}

	maxDocs := cfg.MaxDocsPerRequest
	if maxDocs <= 0 {
		maxDocs = defaultMGetDocsPerRequest
	}
	attempts := cfg.MaxAttempts
	if attempts <= 0 {
		attempts = defaultMGetAttempts
	}
	inFlight := cfg.MaxInFlightDocsPerNode
	if inFlight <= 0 {
		inFlight = defaultMGetInFlightPerDocs * maxDocs
	}

	d := &mgetDriver{
		driver:      driver,
		observer:    cfg.Observer,
		maxDocs:     maxDocs,
		maxAttempts: attempts,
		windows: scheduler.NewNodeWindows(scheduler.WindowConfig{
			Start:   float64(inFlight),
			Ceiling: float64(inFlight),
			Floor:   float64(maxDocs),
			Unit:    float64(maxDocs),
			Backoff: mgetWindowBackoff,
		}),
		send:      sendMGetToNode,
		saturated: func(conn *opensearchtransport.Connection) bool { return conn.GetPool().Overloaded },
	}
	if root != nil {
		if lookup, ok := root.Transport.(shardLookup); ok {
			d.lookup = lookup
		}
	}

	return d
}

// selected returns the driver a call made with ctx uses.
func (d *mgetDriver) selected(ctx context.Context) MGetDriver {
	if v, ok := ctx.Value(mgetDriverKey{}).(MGetDriver); ok && v != MGetDriverDefault {
		return v
	}

	return d.driver
}

// interceptMGet is the hook DocumentClient.MGet calls first. It answers the
// call itself, and reports true, when the direct driver is selected and can
// place at least one of the documents; otherwise it reports false and the
// mget goes to the cluster as one request.
func (c *Client) interceptMGet(ctx context.Context, req MGetReq) (*MGetResp, bool, error) {
	d := c.ext.mget
	if d == nil || d.lookup == nil || d.selected(ctx) != MGetDriverDirect {
		return nil, false, nil
	}

	call, err := d.write(c, req)
	if errors.Is(err, errMGetNotSplittable) {
		// write says why the mget cannot be split; it goes to the cluster as one
		// request, which is not a failure of the call.
		return nil, false, nil
	}

	resp, err := call.flush(ctx)

	return resp, true, err
}
