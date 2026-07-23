// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"encoding/json"
	"fmt"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osapilint/internal/apirev"
)

// plan.go turns the per-adjacent-hop registry (transitions.go) into an ordered
// list of self-contained hop plans for a source->target migration. Each hop is
// applied independently and in series (see main.go): apply vN->vN+1, write the
// files out, rebuild against vN+1, then apply vN+1->vN+2, and so on.
//
// This is deliberately NOT a folded/composed single pass. Folding tables across
// versions optimizes for multi-hop efficiency we don't need - this is a one-time
// utility - at the cost of being harder to reason about and get right. Running
// each hop against its own two real surfaces keeps every step reproducible and
// coherent, and makes adding a future hop a purely local change: author its
// tables, drop in its surfaces, done. The rewriter is type-aware, so between
// hops the code must actually compile against the intermediate version; the
// serial driver rebuilds to make that so.

const openSearchGoBase = "github.com/opensearch-project/opensearch-go"

// hopPlan is one adjacent transition ready to apply: the field-level delta for
// this hop (its own source surface diffed against its own target surface under
// its own tables) plus the call-site rules, import prefix, and operator
// followups. Entirely self-contained - nothing is threaded from adjacent hops.
type hopPlan struct {
	from, to       major
	delta          apirev.Delta
	renames        []apirev.TypeRename
	regroups       []methodRegroup
	removedHelpers map[string]string
	importPrefixes [][2]string
	followups      []string
}

// planChain builds the ordered per-hop plans for migrating src->dst. It requires
// a registered hop and an embedded surface for every adjacent step in [src, dst).
// The plans are meant to be applied in order, rebuilding between them.
func planChain(src, dst major) ([]hopPlan, error) {
	if src >= dst {
		return nil, fmt.Errorf("source v%d is not older than target v%d", src, dst)
	}

	var plans []hopPlan
	for v := src; v < dst; v++ {
		h, ok := hops[v]
		if !ok {
			return nil, fmt.Errorf("no registered transition for v%d -> v%d (needed to migrate v%d -> v%d); "+
				"add a hop_v%d_to_v%d.go and register it in hops", v, v+1, src, dst, v, v+1)
		}

		fromSnap, err := decodeSurface(h.From)
		if err != nil {
			return nil, err
		}
		toSnap, err := decodeSurface(h.To)
		if err != nil {
			return nil, err
		}

		plans = append(plans, hopPlan{
			from:           h.From,
			to:             h.To,
			delta:          apirev.DeriveDelta(fromSnap, toSnap, h.TypeRenames, h.FieldDispositions),
			renames:        h.TypeRenames,
			regroups:       h.MethodRegroups,
			removedHelpers: h.RemovedHelpers,
			importPrefixes: [][2]string{{modulePath(h.From), modulePath(h.To)}},
			followups:      h.SemanticFollowups,
		})
	}
	return plans, nil
}

// decodeSurface decodes the embedded exported-struct surface for a version.
func decodeSurface(m major) (*apirev.Snapshot, error) {
	data, ok := surfaces[m]
	if !ok {
		return nil, fmt.Errorf("no embedded surface for v%d (regenerate with cmd/gensurface and register it in surfaces)", m)
	}
	var s apirev.Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decode surface v%d: %w", m, err)
	}
	return &s, nil
}

// newestKnownTarget is the highest target version reachable in the registry,
// used as the default -dst.
func newestKnownTarget() major {
	var newest major
	for _, h := range hops {
		if h.To > newest {
			newest = h.To
		}
	}
	return newest
}

// modulePath returns the opensearch-go module import path for a major version.
// v1 has no "/vN" suffix (Go module major-version rules); v2+ do.
func modulePath(m major) string {
	if m <= 1 {
		return openSearchGoBase
	}
	return fmt.Sprintf("%s/v%d", openSearchGoBase, m)
}
