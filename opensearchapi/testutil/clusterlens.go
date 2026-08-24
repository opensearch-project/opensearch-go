// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package testutil

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/internal/test/readiness"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// clusterLensFSMCheck returns an FSMCheck that observes a cluster from
// the API client's perspective. Each tick fans out up to three calls:
//
//   - GET /             reachable -> LayerHTTP
//   - cluster.health   number_of_nodes >= expected -> LayerClusterJoin
//   - _cat/nodes       cpu+heap.percent populated  -> LayerStatsReady
//
// Layer values are cumulative, so advancing to LayerStatsReady implicitly
// asserts every lower layer; the diagnostic still attributes the
// transition history correctly because each Advance records its own
// note.
//
// cluster.health (the only call that blocks server-side, up to 1s
// waiting for yellow status) is skipped when the target doesn't reach
// LayerClusterJoin. cat-nodes is kept so MinNodes counting still has
// per-node identity to work with.
//
// Errors that look like configuration problems (auth failure, scheme
// mismatch) are wrapped with readiness.ErrTerminal so callers fail fast
// instead of polling until the deadline.
func clusterLensFSMCheck(client *opensearchapi.Client, expected int) readiness.FSMCheck {
	return func(ctx context.Context, cluster *readiness.Cluster) error {
		needsClusterJoin := cluster.Target&readiness.LayerClusterJoin == readiness.LayerClusterJoin
		needsStatsReady := cluster.Target&readiness.LayerStatsReady == readiness.LayerStatsReady

		httpUp := false
		if _, err := client.Info(ctx, nil); err == nil {
			httpUp = true
		} else if isPermanentAuthErr(err) {
			return readiness.AsTerminal(fmt.Errorf(
				"authentication rejected (verify SECURE_INTEGRATION matches cluster scheme): %w", err))
		} else {
			cluster.RecordError(err)
		}

		clusterJoined := false
		if needsClusterJoin {
			healthReq := &opensearchapi.ClusterHealthReq{
				Params: &opensearchapi.ClusterHealthParams{
					TimeoutParams: opensearchapi.TimeoutParams{Timeout: time.Second},
					WaitForStatus: "yellow",
				},
			}
			if health, err := client.Cluster.Health(ctx, healthReq); err == nil {
				clusterJoined = health.NumberOfNodes >= expected
			} else if isPermanentAuthErr(err) {
				return readiness.AsTerminal(fmt.Errorf(
					"authentication rejected (verify SECURE_INTEGRATION matches cluster scheme): %w", err))
			} else {
				cluster.RecordError(err)
			}
		}

		catReq := &opensearchapi.CatNodesReq{
			Params: &opensearchapi.CatNodesParams{
				// ?timeout= bounds the inner NodesInfo+NodesStats RPCs that
				// cat-nodes fans out (RestNodesAction.java:128,140 in the
				// server). Without it, a slow first stats cycle on a freshly
				// joined node yields a row with cpu=null, heap.percent=null
				// indefinitely, blocking LayerStatsReady. Set generously so
				// readiness gating drives the retry cadence at the Go layer
				// rather than truncating individual server-side polls.
				TimeoutParams: opensearchapi.TimeoutParams{Timeout: 10 * time.Second},
				DebugParams:   opensearchapi.DebugParams{Format: "json"},
				FullID:        "true",
			},
		}
		cat, err := client.Cat.Nodes(ctx, catReq)
		if err != nil {
			if isPermanentAuthErr(err) {
				return readiness.AsTerminal(fmt.Errorf(
					"authentication rejected (verify SECURE_INTEGRATION matches cluster scheme): %w", err))
			}
			cluster.RecordError(err)
			return nil
		}
		if rawResp := cat.Inspect().Response; rawResp != nil {
			cluster.RecordResponse(rawResp.RawBody())
		}

		for _, rec := range cat.Records {
			id := nodeKey(rec)
			if id == "" {
				continue
			}
			node := cluster.Node(id, strPtr(rec.Name), strPtr(rec.IP))

			switch {
			case needsStatsReady && strPtr(rec.CPU) != "" && strPtr(rec.HeapPercent) != "":
				node.Advance(readiness.LayerStatsReady, "cat-nodes cpu+heap populated")
			case clusterJoined:
				node.Advance(readiness.LayerClusterJoin, "cluster_health number_of_nodes met")
			case httpUp:
				node.Advance(readiness.LayerHTTP, "GET / OK; node listed in cat-nodes")
			}
		}
		return nil
	}
}

// isPermanentAuthErr reports whether err is an authentication or
// authorization failure that won't heal under continued polling
// (HTTP 401/403). Inspects the typed opensearch.{String,Struct,
// Reason,Message}Error wrappers that the opensearchapi client emits via
// opensearch.ParseError, then falls back to readiness.IsPermanentAuthErr's
// substring check for transport-layer errors that don't carry a
// structured Status field.
//
// Lives here rather than in the readiness package because the
// typed-error import would create an import cycle through
// opensearchtransport's tests (readiness <- opensearchtransport tests,
// readiness -> opensearch -> opensearchtransport).
func isPermanentAuthErr(err error) bool {
	if err == nil {
		return false
	}
	var stringErr opensearch.StringError
	if errors.As(err, &stringErr) {
		return stringErr.Status == http.StatusUnauthorized || stringErr.Status == http.StatusForbidden
	}
	var structErr opensearch.StructError
	if errors.As(err, &structErr) {
		return structErr.Status == http.StatusUnauthorized || structErr.Status == http.StatusForbidden
	}
	var reasonErr opensearch.ReasonError
	if errors.As(err, &reasonErr) {
		return reasonErr.Status == "401" || reasonErr.Status == "403"
	}
	var messageErr opensearch.MessageError
	if errors.As(err, &messageErr) {
		return messageErr.Status == "401" || messageErr.Status == "403"
	}
	return readiness.IsPermanentAuthErr(err)
}

func strPtr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// nodeKey derives a stable, unique-per-node identifier from a cat-nodes
// record. Prefers the cluster-assigned ID (which ?full_id=true exposes
// in full); falls back to "name@ip" because Name alone can repeat in
// homogeneous deployments where multiple data nodes share node.name.
// Returns empty when no field is populated, which signals "skip this
// record".
func nodeKey(rec opensearchapi.CatNodesRecord) string {
	if id := strPtr(rec.ID); id != "" {
		return id
	}
	name, ip := strPtr(rec.Name), strPtr(rec.IP)
	switch {
	case name != "" && ip != "":
		return name + "@" + ip
	case ip != "":
		return ip
	default:
		return name
	}
}
