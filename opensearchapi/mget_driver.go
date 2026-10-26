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

// MGetDriver selects how [DocumentClient.MGet] fetches its documents.
//
// Take MGet(k1, k2, k3, k4), where k1 and k2 are in shard 0 and k3 is in shard
// 2, all read from node M, and k4 is in shard 1, read from node N.
// MGetDriverCoordinator sends the mget to one node, the coordinator. It groups
// the documents by shard (TransportMultiGetAction.doExecute), sends each
// shard's group at once to a node that holds a copy, waits for all of them,
// and answers in the order the documents were asked for:
//
//	client          coordinator           node M          node N
//	  |                  |                  |               |
//	  |--mget(k1..k4)--->|                  |               |
//	  |                  |--shard 0: k1,k2->|               |
//	  |                  |--shard 2: k3---->|               |
//	  |                  |--shard 1: k4-------------------->|
//	  |                  |<----------k1,k2--|               |
//	  |                  |<-------------k3--|               |
//	  |                  |<-----------------------------k4--|
//	  |<--[k1,k2,k3,k4]--|                  |               |
//
// Each shard request is one task on the get thread pool of the node that
// reads it, which reads the request's documents one after another
// (TransportShardMultiGetAction.shardOperation): here two tasks on M, one
// with k1 and k2, and one on N.
//
// MGetDriverDirect sends each node the documents it holds, as concurrent
// requests with preference=_local. Each node coordinates its own share and
// reads every shard from its own copy, so there is no second hop. The client
// puts the answers back in order:
//
//	client          coordinator           node M          node N
//	  |                  |                  |               |
//	  |--mget(k1,k2,k3) _local------------->|               |
//	  |--mget(k4) _local----------------------------------->|
//	  |                  |                  |--+ shard 0    |--+ shard 1
//	  |                  |                  |<-+ k1,k2      |<-+ k4
//	  |                  |                  |--+ shard 2    |
//	  |                  |                  |<-+ k3         |
//	  |<------------------------[k1,k2,k3]--|               |
//	  |<----------------------------------------------[k4]--|
//	  |--+ merge         |                  |               |
//	  |<-+ [k1,k2,k3,k4] |                  |               |
//
// For an mget of documents in S shards, the drivers cost:
//
//	                        coordinator   direct
//	client requests         1             R
//	node-to-node requests   S - L         0
//	get thread pool tasks   S             >= S
//	hops in sequence        2             1
//	network transfers/doc   2             1
//
// R is the number of requests the direct driver sends: the sum, over the
// nodes, of each node's documents divided by MaxDocsPerRequest and rounded up,
// so one per node for a small mget. L is the shards whose copy the coordinator
// picks on itself, which it reads without a network hop; it picks a copy at
// random, so L is about S/K on K data nodes. The direct driver has more than S
// tasks when it splits a shard's documents between requests, and, with
// MGetFanOutLeastLoaded, between copies; MGetFanOutFewestNodes never splits a
// shard between copies, and reaches at most about K/C nodes when each shard
// has C copies. The example costs the coordinator 1 client request and 3
// node-to-node requests, and the direct driver 2 client requests; each runs 3
// get thread pool tasks.
//
// The direct driver sends the documents it cannot route to the coordinator,
// in one ordinary mget alongside the direct requests. It sends a realtime read
// of a segment-replicated index to the node that holds the shard's primary,
// without preference=_local, and that node routes the read to its primary
// copy the way a coordinator would.
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
	// A document the client cannot route to a node -- one in an index whose shard
	// map is not known yet, named through an alias or a data stream, or with no
	// _index -- is fetched through a coordinating node in a request of its own. So
	// is every document of an mget the client cannot split as the cluster would:
	// one with a preference, a filter_path, a format or a source parameter, a raw
	// BodyReader, or ids in place of docs.
	MGetDriverDirect
)

// defaultMGetDriver is the driver MGetDriverDefault resolves to when
// OPENSEARCH_GO_MGET is not set.
const defaultMGetDriver = MGetDriverCoordinator

// mgetDriverNames spells each driver as OPENSEARCH_GO_MGET does.
//
//nolint:gochecknoglobals // read-only name table shared by String and ParseMGetDriver
var mgetDriverNames = []string{MGetDriverDefault: "default", MGetDriverCoordinator: "coordinator", MGetDriverDirect: "direct"}

// String returns the driver as OPENSEARCH_GO_MGET spells it.
func (d MGetDriver) String() string { return mgetChoiceName(mgetDriverNames, d, "MGetDriver") }

// ParseMGetDriver parses a driver as OPENSEARCH_GO_MGET spells it: "direct"
// or "coordinator", in any case. The empty string is MGetDriverDefault.
func ParseMGetDriver(s string) (MGetDriver, error) {
	return parseMGetChoice[MGetDriver](s, mgetDriverNames, "mget driver")
}

// MGetFanOut selects which copy of each shard [MGetDriverDirect] reads a
// document from, and so how many nodes one mget reaches.
//
// MGetFanOutFewestNodes is the default because it costs the cluster less work
// for the same documents. MGetFanOutLeastLoaded reads a shard's documents from
// whichever copy has the fewest documents planned, so a shard with two or more
// documents is usually read from more than one copy: each copy is a task on a
// get thread pool and a request to one more node. The fewest-nodes cover reads
// each shard from one copy, as the coordinator does, and reaches at most about
// K/C of K data nodes when each shard has C copies. Calculated for one mget on
// idle nodes with evenly placed copies, averaged over 200 random mgets:
//
//	nodes shards copies docs   least-loaded    fewest-nodes    requests tasks
//	                           requests tasks  requests tasks
//	300   300    2      10       10      10      10      10      -3%   -2%
//	300   300    2      100      95      97      73      85     -23%  -13%
//	300   300    2      1000    298     512     150     289     -50%  -44%
//	300   300    3      1000    300     644     100     290     -67%  -55%
//	300   3000   2      1000    299     932     150     851     -50%   -9%
//	300   3000   2      5000    300    3419     150    2432     -50%  -29%
//	300   30     2      1000     60      60      30      30     -50%  -50%
//
// For the same mgets the coordinator runs as many tasks as fewest-nodes, and
// sends one node-to-node request per shard it does not hold itself, 289 to 849
// in the rows above with 1000 documents, against at most 150 client requests.
// Every node an mget reaches is also one more node that can time out or fail
// it, so the cover adds documents to a node the mget already reaches before
// it sends to another, however idle.
//
// What fewest-nodes gives up is parallelism within one mget: more of its
// documents go to each node it reaches. The per-node window,
// MGetConfig.MaxInFlightDocsPerNode, still bounds what is in flight to each
// node.
type MGetFanOut uint8

const (
	// MGetFanOutDefault leaves the choice to the OPENSEARCH_GO_MGET_FANOUT
	// environment variable and, when that is not set, to this release's
	// default, MGetFanOutFewestNodes.
	MGetFanOutDefault MGetFanOut = iota

	// MGetFanOutFewestNodes reads an mget's shards from as few nodes as it can.
	// It takes the node that holds a copy of the most shards still unassigned,
	// the least loaded of those on a tie, and repeats until every shard has a
	// node, skipping a node whose get thread pool is rejecting work while
	// another copy is on a node that is not. Each node it reaches reads more of
	// the mget's shards from its own copies, so the mget costs fewer requests
	// and the cluster pays each request's fixed cost fewer times, in exchange
	// for less parallelism within one mget.
	MGetFanOutFewestNodes

	// MGetFanOutLeastLoaded reads each document from the copy of its shard on
	// the node with the fewest documents in flight, counting the ones this mget
	// has already placed, and from a replica on a tie. It spreads an mget over
	// as many nodes as hold its shards.
	MGetFanOutLeastLoaded
)

// defaultMGetFanOut is the fan-out MGetFanOutDefault resolves to when
// OPENSEARCH_GO_MGET_FANOUT is not set.
const defaultMGetFanOut = MGetFanOutFewestNodes

// mgetFanOutNames spells each fan-out as OPENSEARCH_GO_MGET_FANOUT does.
//
//nolint:gochecknoglobals // read-only name table shared by String and ParseMGetFanOut
var mgetFanOutNames = []string{MGetFanOutDefault: "default", MGetFanOutFewestNodes: "fewest-nodes", MGetFanOutLeastLoaded: "least-loaded"}

// String returns the fan-out as OPENSEARCH_GO_MGET_FANOUT spells it.
func (f MGetFanOut) String() string { return mgetChoiceName(mgetFanOutNames, f, "MGetFanOut") }

// ParseMGetFanOut parses a fan-out as OPENSEARCH_GO_MGET_FANOUT spells it:
// "fewest-nodes" or "least-loaded", in any case. The empty string is
// MGetFanOutDefault.
func ParseMGetFanOut(s string) (MGetFanOut, error) {
	return parseMGetChoice[MGetFanOut](s, mgetFanOutNames, "mget fan-out")
}

// mgetChoice is an [MGetDriver] or an [MGetFanOut]: a choice the client's
// configuration, an environment variable and a call's context can each make,
// whose zero value, its Default, leaves the choice to the next of them.
type mgetChoice interface{ ~uint8 }

// mgetChoiceName returns the name of v in names, or typ(v) for a value names
// does not hold.
func mgetChoiceName[T mgetChoice](names []string, v T, typ string) string {
	if int(v) < len(names) {
		return names[v]
	}

	return fmt.Sprintf("%s(%d)", typ, uint8(v))
}

// parseMGetChoice parses s as one of names after the first, the Default's, in
// any case and ignoring surrounding space. The empty string is the Default.
// what names the choice in the error.
func parseMGetChoice[T mgetChoice](s string, names []string, what string) (T, error) {
	name := strings.ToLower(strings.TrimSpace(s))
	if name == "" {
		return 0, nil
	}
	for i := 1; i < len(names); i++ {
		if name == names[i] {
			return T(i), nil
		}
	}

	return 0, fmt.Errorf("unknown %s %q, want %s", what, s, strings.Join(names[1:], " or "))
}

// resolveMGetChoice returns the choice the environment variable env makes, else
// cfg's, else def. A value of env that does not parse is ignored.
func resolveMGetChoice[T mgetChoice](
	cfg T, getenv func(string) (string, bool), env string, parse func(string) (T, error), def T,
) T {
	if v, ok := getenv(env); ok {
		if parsed, err := parse(v); err == nil && parsed != 0 {
			cfg = parsed
		}
	}
	if cfg == 0 {
		return def
	}

	return cfg
}

// mgetOverride returns the choice ctx carries under key, else fallback.
func mgetOverride[T mgetChoice](ctx context.Context, key any, fallback T) T {
	if v, ok := ctx.Value(key).(T); ok && v != 0 {
		return v
	}

	return fallback
}

// MGetConfig configures how [DocumentClient.MGet] fetches documents. Set it as
// [Config].MGet.
type MGetConfig struct {
	// Driver selects the driver. OPENSEARCH_GO_MGET overrides it, and
	// [WithMGetDriver] overrides both for one call.
	Driver MGetDriver

	// FanOut selects which shard copies MGetDriverDirect reads from.
	// OPENSEARCH_GO_MGET_FANOUT overrides it, and [WithMGetFanOut] overrides
	// both for one call.
	FanOut MGetFanOut

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

// mgetFanOutKey is the context key for [WithMGetFanOut].
type mgetFanOutKey struct{}

// WithMGetFanOut returns a context that has [MGetDriverDirect] use fanOut for
// calls made with it, whatever the client's configuration and
// OPENSEARCH_GO_MGET_FANOUT say. MGetFanOutDefault leaves the client's choice
// in place.
func WithMGetFanOut(ctx context.Context, fanOut MGetFanOut) context.Context {
	return context.WithValue(ctx, mgetFanOutKey{}, fanOut)
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
	return newMGetDriverWith(cfg, root, os.LookupEnv)
}

// newMGetDriverWith is newMGetDriver reading the environment overrides through
// getenv, so a test can supply them without changing the process environment.
func newMGetDriverWith(cfg MGetConfig, root *opensearch.Client, getenv func(string) (string, bool)) *mgetDriver {
	driver := resolveMGetChoice(cfg.Driver, getenv, envvars.MGet, ParseMGetDriver, defaultMGetDriver)
	fanOut := resolveMGetChoice(cfg.FanOut, getenv, envvars.MGetFanOut, ParseMGetFanOut, defaultMGetFanOut)

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
		fanOut:      fanOut,
		observer:    cfg.Observer,
		maxDocs:     maxDocs,
		maxAttempts: attempts,
		windows: scheduler.NewNodeWindows(scheduler.WindowConfig{
			Start:     float64(inFlight),
			Ceiling:   float64(inFlight),
			Floor:     float64(maxDocs),
			Increment: float64(maxDocs),
			Backoff:   mgetWindowBackoff,
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
	return mgetOverride(ctx, mgetDriverKey{}, d.driver)
}

// selectedFanOut returns the fan-out a call made with ctx uses.
func (d *mgetDriver) selectedFanOut(ctx context.Context) MGetFanOut {
	return mgetOverride(ctx, mgetFanOutKey{}, d.fanOut)
}

// interceptMGet is the hook DocumentClient.MGet calls first. It answers the
// call itself, and reports true, when the direct driver is selected and can
// route at least one of the documents to a node; otherwise it reports false
// and the mget goes to the cluster as one request, decoded into the response
// it returns, which allocMGetResp sizes when the request says how many documents
// it asks for.
func (c *Client) interceptMGet(ctx context.Context, req MGetReq) (*MGetResp, bool, error) {
	d := c.ext.mget
	if d == nil || d.lookup == nil || d.selected(ctx) != MGetDriverDirect {
		return allocMGetResp(ctx, req), false, nil
	}

	call, err := d.write(c, req, d.selectedFanOut(ctx))
	if err != nil {
		// write says why the mget cannot be split; it goes to the cluster as one
		// request, which is not a failure of the call.
		return allocMGetResp(ctx, req), false, nil //nolint:nilerr // Not splitting is not the call's error.
	}

	resp, err := call.flush(ctx)

	return resp, true, err
}

// allocMGetResp returns the response a coordinated mget is decoded into: one with
// room for a document per document asked for, so that decoding the answer does
// not grow Docs by copying, and its documents' storage is sized for them. The
// documents are the request's, or, for a direct driver's sub-request, whose body
// is already encoded, those withMGetDocs gave ctx. It returns nil when neither
// says, as for ids or a caller's own BodyReader.
//
// The preallocated response also carries the requested documents to
// MGetResp.UnmarshalJSONFrom, in the first unused element of Docs, so that a
// document whose _id and _index match the request reuses the request's strings.
// The decoder clears that element before it decodes into it, and
// MGetResp.dropPrealloc drops a preallocated response nothing decoded into; see
// takeMGetAsked.
func allocMGetResp(ctx context.Context, req MGetReq) *MGetResp {
	asked, _ := ctx.Value(mgetDocsKey{}).(*mgetAsked)
	if req.Body != nil && len(req.Body.Docs) > 0 {
		asked = &mgetAsked{docs: req.Body.Docs, index: req.Index}
	}
	if asked == nil || asked.len() == 0 {
		return nil
	}

	docs := make([]MGetRespItem, 0, asked.len())
	docs[:1][0].value = asked

	return &MGetResp{Docs: docs}
}

// mgetAsked is the documents an mget asks for, in the order its body names them:
// docs, or, for a direct driver's sub-request, docs[slots[i]] for each i. index
// is the request's default index, for documents that name none.
type mgetAsked struct {
	docs  []MGetOperation
	slots []int
	index string
}

func (a *mgetAsked) len() int {
	if a.slots != nil {
		return len(a.slots)
	}

	return len(a.docs)
}

// doc returns the requested _id and _index of the i-th document, or empty
// strings when a is nil or has no i-th document.
func (a *mgetAsked) doc(i int) (string, string) {
	if a == nil || i >= a.len() {
		return "", ""
	}
	if a.slots != nil {
		i = a.slots[i]
	}
	op := a.docs[i]
	if op.Index != nil {
		return op.ID, *op.Index
	}

	return op.ID, a.index
}

// mgetDocsKey is the context key for withMGetDocs.
type mgetDocsKey struct{}

// withMGetDocs returns ctx saying that the mget sent with it asks for the
// documents of req at slots, which allocMGetResp sizes the response for. They ride
// on ctx because the direct driver's sub-requests carry their body already encoded,
// and the generated DocumentClient.MGet takes nothing else that could carry them.
func withMGetDocs(ctx context.Context, req MGetReq, slots []int) context.Context {
	return context.WithValue(ctx, mgetDocsKey{}, &mgetAsked{docs: req.Body.Docs, slots: slots, index: req.Index})
}
