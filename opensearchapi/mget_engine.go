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
	"strings"

	opensearch "github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/internal/envvars"
	"github.com/opensearch-project/opensearch-go/v5/internal/scheduler"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// MGetEngine selects how [DocumentClient.MGet] fetches its documents.
type MGetEngine uint8

const (
	// MGetEngineDefault leaves the choice to the OPENSEARCH_GO_MGET environment
	// variable and, when that is not set, to this release's default,
	// MGetEngineCoordinator.
	MGetEngineDefault MGetEngine = iota

	// MGetEngineCoordinator sends each mget as one request. The node that
	// receives it splits the documents by shard, fetches each shard's from a
	// node that holds it, and answers for all of them.
	MGetEngineCoordinator

	// MGetEngineDirect splits each mget by shard in the client, using the shard
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
	MGetEngineDirect
)

// defaultMGetEngine is the engine MGetEngineDefault resolves to when
// OPENSEARCH_GO_MGET is not set.
const defaultMGetEngine = MGetEngineCoordinator

// String returns the engine as OPENSEARCH_GO_MGET spells it.
func (e MGetEngine) String() string {
	switch e {
	case MGetEngineDefault:
		return "default"
	case MGetEngineCoordinator:
		return "coordinator"
	case MGetEngineDirect:
		return "direct"
	default:
		return fmt.Sprintf("MGetEngine(%d)", uint8(e))
	}
}

// ParseMGetEngine parses an engine as OPENSEARCH_GO_MGET spells it: "direct"
// or "coordinator", in any case. The empty string is MGetEngineDefault.
func ParseMGetEngine(s string) (MGetEngine, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return MGetEngineDefault, nil
	case "coordinator":
		return MGetEngineCoordinator, nil
	case "direct":
		return MGetEngineDirect, nil
	default:
		return MGetEngineDefault, fmt.Errorf("unknown mget engine %q, want direct or coordinator", s)
	}
}

// MGetConfig configures how [DocumentClient.MGet] fetches documents. Set it as
// [Config].MGet.
type MGetConfig struct {
	// Engine selects the engine. OPENSEARCH_GO_MGET overrides it, and
	// [WithMGetEngine] overrides both for one call.
	Engine MGetEngine

	// Observer, when set, is told about every request MGetEngineDirect sends and
	// about each mget it completes.
	Observer MGetObserver

	// MaxDocsPerRequest is the most documents MGetEngineDirect puts in one
	// request to a node; a node's share of a larger mget goes as several
	// requests. 0 means 500.
	MaxDocsPerRequest int

	// MaxAttempts is how many requests MGetEngineDirect sends a document in
	// directly when a node rejects it, fails, or answers that the shard is not
	// available, before it asks a coordinating node for it instead. 0 means 3.
	MaxAttempts int

	// MaxInFlightDocsPerNode bounds the documents MGetEngineDirect has
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

// mgetEngineKey is the context key for [WithMGetEngine].
type mgetEngineKey struct{}

// WithMGetEngine returns a context that has [DocumentClient.MGet] use engine for
// calls made with it, whatever the client's configuration and
// OPENSEARCH_GO_MGET say. MGetEngineDefault leaves the client's choice in
// place.
func WithMGetEngine(ctx context.Context, engine MGetEngine) context.Context {
	return context.WithValue(ctx, mgetEngineKey{}, engine)
}

// shardLookup is what the direct engine needs of a transport;
// [opensearchtransport.Transport] provides it.
type shardLookup interface {
	LookupShard(index, key string, dst *[]*opensearchtransport.ShardTarget) error
}

// newMGetEngine builds the engine a client's MGet tries first. It is built even
// when the coordinator engine is selected, so that WithMGetEngine can choose
// the direct one for a call; it fetches nothing directly when the transport
// keeps no shard map.
func newMGetEngine(cfg MGetConfig, root *opensearch.Client) *mgetEngine {
	engine := cfg.Engine
	if v, ok := os.LookupEnv(envvars.MGet); ok {
		if parsed, err := ParseMGetEngine(v); err == nil && parsed != MGetEngineDefault {
			engine = parsed
		}
	}
	if engine == MGetEngineDefault {
		engine = defaultMGetEngine
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

	e := &mgetEngine{
		engine:      engine,
		observer:    cfg.Observer,
		maxDocs:     maxDocs,
		maxAttempts: attempts,
		admission: scheduler.NewAdmission(scheduler.WindowConfig{
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
			e.lookup = lookup
		}
	}

	return e
}

// selected returns the engine a call made with ctx uses.
func (e *mgetEngine) selected(ctx context.Context) MGetEngine {
	if v, ok := ctx.Value(mgetEngineKey{}).(MGetEngine); ok && v != MGetEngineDefault {
		return v
	}

	return e.engine
}

// interceptMGet is the hook DocumentClient.MGet calls first. It answers the
// call itself, and reports true, when the direct engine is selected and can
// place at least one of the documents; otherwise it reports false and the
// mget goes to the cluster as one request.
func (c *Client) interceptMGet(ctx context.Context, req MGetReq) (*MGetResp, bool, error) {
	e := c.mget
	if e == nil || e.lookup == nil || e.selected(ctx) != MGetEngineDirect {
		return nil, false, nil
	}

	call, ok := e.plan(c, req)
	if !ok {
		return nil, false, nil
	}

	resp, err := call.run(ctx)

	return resp, true, err
}
