// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"context"
	"time"
)

// MGetObserver receives notifications about the requests the direct mget driver
// sends, for statistics and metrics: how widely each mget fans out across the
// nodes and how often a node's documents are sent again. Set it as
// [MGetConfig].Observer.
//
// Implementations must be safe for concurrent use: the requests of one mget run
// at once, and events arrive from their goroutines. They must return quickly,
// since the goroutine that calls them is the one fetching the documents.
// Events are values and may be retained.
type MGetObserver interface {
	// OnMGetRequest is called after each request the driver sends returns,
	// whether it failed or not: each request to a node, each retry, and each
	// request sent through a coordinating node.
	OnMGetRequest(ctx context.Context, event MGetRequestEvent)

	// OnMGetDone is called once per mget the driver answers, when it returns.
	OnMGetDone(ctx context.Context, event MGetDoneEvent)
}

// BaseMGetObserver is an embeddable no-op [MGetObserver]. Embed it in your own
// struct and override only the methods you care about.
type BaseMGetObserver struct{}

// OnMGetRequest implements MGetObserver (no-op).
func (BaseMGetObserver) OnMGetRequest(context.Context, MGetRequestEvent) {}

// OnMGetDone implements MGetObserver (no-op).
func (BaseMGetObserver) OnMGetDone(context.Context, MGetDoneEvent) {}

// MGetRequestEvent describes one request the direct mget driver sent.
type MGetRequestEvent struct {
	// Node is the name of the node the request was sent to. It is empty for a
	// request sent through a coordinating node, which the transport routed.
	Node string

	// Attempt is 1 for the first request that carried its documents and counts
	// up for each retry. It is 0 for a request sent through a coordinating node.
	Attempt int

	// Docs is the number of documents the request asked for.
	Docs int

	// Duration is the time from sending the request until it returned, not
	// counting the wait for room in the node's window.
	Duration time.Duration

	// StatusCode is the HTTP status of the response, or 0 when none arrived.
	StatusCode int

	// Err is non-nil when the request failed as a whole.
	Err error

	// Rejected reports that the node refused the request, or some of its
	// documents, because it was overloaded. It halves the node's window.
	Rejected bool

	// RetryDocs is the number of the request's documents that will be asked for
	// again: directly in a further attempt, or through a coordinating node.
	RetryDocs int
}

// MGetDoneEvent describes an mget the direct driver has answered.
type MGetDoneEvent struct {
	// Docs is the number of documents the mget asked for.
	Docs int

	// Nodes is the number of distinct nodes sent requests directly: the width of
	// the fan-out.
	Nodes int

	// Requests counts every request sent: to the nodes, again on a retry, and
	// through a coordinating node.
	Requests int

	// Retries counts the requests that carried documents a second or later
	// time, directly to a node.
	Retries int

	// CoordinatorDocs is the number of documents answered through a coordinating
	// node: those the client could not place, and those that ran out of direct
	// attempts.
	CoordinatorDocs int

	// FailedDocs is the number of documents the driver itself answered with an
	// error, because no request for them could be completed.
	FailedDocs int

	// Resent reports that a node refused a request in a way the cluster would
	// have refused the whole mget, so the driver sent the whole mget through a
	// coordinating node and returned its answer.
	Resent bool

	// Duration is the time the mget took.
	Duration time.Duration

	// Err is the error the mget returned, if any.
	Err error
}
