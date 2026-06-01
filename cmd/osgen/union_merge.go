// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/ir"
)

// classifyUnions inspects every try-each (TypeLazyUnion) union and, where it
// can, replaces runtime try-each decoding with a single-pass strategy:
//
//   - Case A (t.Merge): all-object unions where a primary branch can be embedded
//     such that every other branch carries a required key the primary lacks. The
//     primary is decoded in one pass and the other branches are detected by the
//     presence of those distinguishing keys. Covers success|error response items
//     (mget, msearch), two-shape bodies (indices-open), and object unions where
//     every branch declares required keys but they're mutually distinguishable
//     (BulkByScrollTaskStatus | ErrorCause). See [planMerge].
//
//   - Case B (t.LazyAccessors): unions referenced as a caller-keyed map value
//     that could not be merged. Their branch type is determined by the request,
//     not the wire (aggregation/suggest result families, "type"-discriminated
//     mapping/analysis values), so they retain raw bytes and expose
//     As<Branch>() accessors that decode the requested concrete type on demand.
//     A non-mergeable union that is NOT caller-keyed (e.g. a reindex body that
//     can't be told apart) is left on try-each, since As<T>() would force the
//     caller to guess.
//
// Unions that fit neither are left on the existing try-each decoder.
func classifyUnions(spec *ir.Spec) {
	reg := spec.Registry
	allTypes := collectTypes(spec)
	callerKeyed := mapValuedUnions(allTypes, reg)

	// A union can appear as several ir.Type instances (shared registry copy +
	// per-operation copies), so each is classified separately; warn at most
	// once per union name to avoid duplicate diagnostics.
	warned := map[string]struct{}{}

	for _, t := range allTypes {
		if t.Kind != ir.TypeLazyUnion { // first-byte switch unions are already cheap
			continue
		}
		if !allObjectBranches(t) {
			continue
		}

		warn := func(format string, args ...any) {
			if _, ok := warned[t.Name]; ok {
				return
			}
			warned[t.Name] = struct{}{}
			log.Printf(format, args...)
		}

		if plan := planMerge(t, reg); plan != nil {
			t.Merge = plan
			continue
		}

		// Couldn't merge. If the union is referenced as a caller-keyed map value,
		// the branch type is determined by the request, not the wire (aggregation
		// and suggest result families): retain raw and expose As<T>() accessors.
		// This holds whether or not the branches declare required keys, so it
		// survives allOf-required flattening.
		if callerKeyed[t.Name] && len(t.Branches) >= 2 {
			t.LazyAccessors = true
			continue
		}

		// Warn only for the shape that looks single-pass decodable but isn't:
		// one permissive branch plus discriminated branch(es) we couldn't tell
		// apart (a discriminator is missing or undeclared -- e.g. a free-form
		// status). Other non-merging shapes are legitimate and stay silent:
		// "type"-value-discriminated DSL unions (analyzers, mappings) have no
		// permissive branch, and fully permissive bodies (reindex) can't be told
		// apart at all.
		permissiveCount := 0
		for _, b := range t.Branches {
			if len(b.Required) == 0 {
				permissiveCount++
			}
		}
		if permissiveCount == 1 {
			warn("osgen: union %q left on try-each: one permissive branch plus discriminated "+
				"branch(es), but no required key distinguishes them by presence", t.Name)
		}
	}
}

// collectTypes returns every ir.Type instance the emit phase may render,
// deduplicated by pointer. Operation response/sibling/request types are
// converted independently of the shared registry (see convertOperation), so a
// union can appear as several distinct instances; each must be classified.
func collectTypes(spec *ir.Spec) []*ir.Type {
	seen := map[*ir.Type]bool{}
	var out []*ir.Type
	add := func(t *ir.Type) {
		if t == nil || seen[t] {
			return
		}
		seen[t] = true
		out = append(out, t)
	}
	for _, t := range spec.Types {
		add(t)
	}
	for _, op := range spec.Operations {
		add(op.Response)
		add(op.RespElemType)
		add(op.RequestBody)
		for _, t := range op.SiblingTypes {
			add(t)
		}
		for _, t := range op.ReqBodySiblings {
			add(t)
		}
	}
	return out
}

// mapValuedUnions returns the names of union types referenced as a map value
// anywhere in the spec (e.g. map[string]Agg or map[string][]Suggest). Such a
// union is keyed by a caller-supplied name, so the caller determines the branch
// type from its request -- the precondition for lazy As<T>() accessors.
func mapValuedUnions(allTypes []*ir.Type, reg *ir.TypeRegistry) map[string]bool {
	unionNames := map[string]bool{}
	for _, t := range reg.Unions() {
		unionNames[t.Name] = true
	}
	out := map[string]bool{}
	for _, t := range allTypes {
		for _, f := range t.Fields {
			if !strings.HasPrefix(f.GoType, "map[") {
				continue
			}
			if name := unwrapTypeName(f.GoType); unionNames[name] {
				out[name] = true
			}
		}
	}
	return out
}

// allObjectBranches reports whether every branch decodes from a JSON object.
func allObjectBranches(t *ir.Type) bool {
	for _, b := range t.Branches {
		if b.TokenClass != ir.TokenObject {
			return false
		}
	}
	return len(t.Branches) > 0
}

// planMerge finds a single-pass merge plan for an all-object union. It picks a
// primary branch to embed such that every other branch carries at least one
// required key the primary lacks; those keys become presence probes. A branch's
// required keys are guaranteed present in its own payload and (being absent from
// the primary's fields) never appear in the primary's, so presence cleanly
// selects the branch. Returns nil when no such primary exists (e.g. every branch
// is permissive, or the branches are not mutually distinguishable by required
// keys).
//
// This generalizes the common success|error case (one permissive primary + one
// discriminated branch) to any object union whose branches are mutually
// distinguishable, including ones where every branch declares required keys
// (e.g. BulkByScrollTaskStatus | ErrorCause).
func planMerge(t *ir.Type, reg *ir.TypeRegistry) *ir.UnionMerge {
	for _, primary := range orderedPrimaryCandidates(t.Branches, reg) {
		if plan := tryPrimary(t, primary, reg); plan != nil {
			return plan
		}
	}
	return nil
}

// orderedPrimaryCandidates returns the branches eligible to be the embedded
// primary (named structs only), preferring permissive over discriminated and
// non-error over error-like, so the common/success shape is embedded when
// possible (making its decode the zero-copy path).
func orderedPrimaryCandidates(branches []ir.UnionBranch, reg *ir.TypeRegistry) []ir.UnionBranch {
	cands := make([]ir.UnionBranch, 0, len(branches))
	for _, b := range branches {
		if embeddableStruct(b.GoType, reg) {
			cands = append(cands, b)
		}
	}
	rank := func(b ir.UnionBranch) int {
		permissive := len(b.Required) == 0
		errlike := strings.Contains(strings.ToLower(unwrapTypeName(b.GoType)), "error")
		switch {
		case permissive && !errlike:
			return 0
		case permissive && errlike:
			return 1
		case !permissive && !errlike:
			return 2
		default:
			return 3
		}
	}
	slices.SortStableFunc(cands, func(a, b ir.UnionBranch) int { return rank(a) - rank(b) })
	return cands
}

// tryPrimary builds a merge plan with the given primary branch embedded and
// every other branch selected by the presence of keys that no other branch can
// carry. For each discriminated branch it prefers a single required key that is
// absent from every other branch's full field set (a sound, minimal probe);
// failing that it probes the branch's whole distinguishing set, and if even
// that set is a subset of another branch's fields it refuses to merge (the
// branches can't be told apart by presence). Returns nil when no branch can be
// soundly discriminated.
func tryPrimary(t *ir.Type, primary ir.UnionBranch, reg *ir.TypeRegistry) *ir.UnionMerge {
	primaryTags := flattenJSONTags(primary.GoType, reg)

	// Full (required + optional) field set of every branch, so a probe key can
	// be checked for presence in OTHER branches, not just the primary.
	branchTags := make([]map[string]bool, len(t.Branches))
	for i, b := range t.Branches {
		branchTags[i] = flattenJSONTags(b.GoType, reg)
	}

	probeName := map[string]string{} // json key -> probe Go field name
	keyOwner := map[string]int{}     // json key -> discriminated branch index
	var probes []ir.MergeProbe
	var branches []ir.MergeBranch

	for bIdx, b := range t.Branches {
		if b.GoType == primary.GoType {
			continue
		}
		idx := len(branches)
		// Distinguishing keys: required by this branch, absent from the primary.
		var distinguishing []string
		for _, key := range b.Required {
			if primaryTags[key] {
				continue
			}
			if owner, ok := keyOwner[key]; ok && owner != idx {
				return nil // required key shared with another branch: ambiguous
			}
			keyOwner[key] = idx
			distinguishing = append(distinguishing, key)
		}
		if len(distinguishing) == 0 {
			return nil // indistinguishable from the primary
		}

		// inOtherBranch reports whether key appears in any branch but this one.
		inOtherBranch := func(key string) bool {
			for j, tags := range branchTags {
				if j != bIdx && tags[key] {
					return true
				}
			}
			return false
		}

		// Prefer a single key absent from every other branch (sound + minimal).
		probeKeys := distinguishing
		for _, key := range distinguishing {
			if !inOtherBranch(key) {
				probeKeys = []string{key}
				break
			}
		}
		// Fall back to the whole distinguishing set; refuse if some other branch
		// could carry all of it (its presence wouldn't prove this branch).
		if len(probeKeys) > 1 {
			for j := range branchTags {
				if j == bIdx {
					continue
				}
				subset := true
				for _, key := range probeKeys {
					if !branchTags[j][key] {
						subset = false
						break
					}
				}
				if subset {
					return nil
				}
			}
		}

		var present []string
		for _, key := range probeKeys {
			goName, ok := probeName[key]
			if !ok {
				goName = fmt.Sprintf("Disc%d", len(probes)) // exported so json populates it
				probeName[key] = goName
				probes = append(probes, ir.MergeProbe{GoName: goName, JSONKey: key})
			}
			present = append(present, goName)
		}
		branches = append(branches, ir.MergeBranch{
			GoType:        b.GoType,
			Const:         unionConst(t.Name, b.Name),
			Name:          b.Name,
			PresentProbes: present,
		})
	}
	if len(branches) == 0 {
		return nil
	}

	return &ir.UnionMerge{
		PrimaryGoType: primary.GoType,
		PrimaryConst:  unionConst(t.Name, primary.Name),
		PrimaryName:   primary.Name,
		Probes:        probes,
		Branches:      branches,
	}
}

// embeddableStruct reports whether goType is a plain named struct that can be
// embedded in the merged decode struct (not a map/slice/pointer/primitive).
func embeddableStruct(goType string, reg *ir.TypeRegistry) bool {
	if unwrapTypeName(goType) != goType {
		return false // map[...], []..., or *... wrapper
	}
	t, ok := reg.LookupByName(goType)
	return ok && t.Kind == ir.TypeStruct
}

// flattenJSONTags returns the set of top-level JSON keys a Go type decodes,
// following embedded types so promoted keys are included.
func flattenJSONTags(goType string, reg *ir.TypeRegistry) map[string]bool {
	tags := map[string]bool{}
	seen := map[string]bool{}
	var walk func(name string)
	walk = func(name string) {
		name = unwrapTypeName(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		t, ok := reg.LookupByName(name)
		if !ok {
			return
		}
		for _, f := range t.Fields {
			if f.IsEmbed {
				walk(f.GoType)
				continue
			}
			if f.JSONName != "" {
				tags[f.JSONName] = true
			}
		}
	}
	walk(goType)
	return tags
}

// unionConst mirrors the discriminant const name emitted by the union template
// (see emit.unionConstNameIR): "<UnionName><BranchName>Type".
func unionConst(unionName, branchName string) string {
	return unionName + branchName + "Type"
}
