// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"encoding/json"
	"fmt"

	"github.com/opensearch-project/opensearch-go/cmd/osapilint/v5/internal/apirev"
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
	from, to       version
	delta          apirev.Delta
	renames        []apirev.TypeRename
	regroups       []methodRegroup
	removedHelpers map[string]string
	importPrefixes [][2]string
	followups      []string
}

// planChain builds the ordered per-hop plans for migrating src->dst. It requires
// a registered hop and an embedded surface for every adjacent step from src to
// dst. The plans are meant to be applied in order, rebuilding between them.
func planChain(src, dst version) ([]hopPlan, error) {
	if !src.less(dst) {
		return nil, fmt.Errorf("source %s is not older than target %s", src, dst)
	}

	var plans []hopPlan
	for v := src; v.less(dst); {
		h, ok := hops[v]
		if !ok {
			return nil, fmt.Errorf("no registered transition from %s (needed to migrate %s -> %s); "+
				"add a hop file for it and register it in hops", v, src, dst)
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
			importPrefixes: [][2]string{{modulePath(h.From.major), modulePath(h.To.major)}},
			followups:      h.SemanticFollowups,
		})
		v = h.To
	}
	return plans, nil
}

// decodeSurface decodes the embedded exported-struct surface for a line,
// including its plugin companion when the line has one.
func decodeSurface(v version) (*apirev.Snapshot, error) {
	data, ok := surfaces[v]
	if !ok {
		return nil, fmt.Errorf("no embedded surface for %s (regenerate with cmd/gensurface and register it in surfaces)", v)
	}
	var s apirev.Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decode surface %s: %w", v, err)
	}
	if pdata, ok := pluginSurfaces[v]; ok {
		var ps apirev.Snapshot
		if err := json.Unmarshal(pdata, &ps); err != nil {
			return nil, fmt.Errorf("decode plugin surface %s: %w", v, err)
		}
		s.Structs = append(s.Structs, ps.Structs...)
	}
	return &s, nil
}

// newestKnownTarget is the newest target line reachable in the registry, used as
// the default -dst.
func newestKnownTarget() version {
	var newest version
	for _, h := range hops {
		if newest.less(h.To) {
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
