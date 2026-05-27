// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// builder is the complete data for one generated path builder struct.
type builder struct {
	StructName         string
	Comment            string
	Description        string
	Group              string
	Deprecated         bool
	DeprecationMessage string
	VersionAdded       string
	VersionDeprecated  string
	DocsURL            string
	ExcludedDistros    []string
	Fields             []builderField
	Ops                []emitOp
	PositionalDeps     []positionalDep
}

// builderField represents a field in the generated struct.
type builderField struct {
	Name     string
	Param    string
	Required bool
	IsList   bool
}

// positionalDep records that field Dependent may only be set when field
// Predecessor is also set: every spec path that contains Dependent also
// contains Predecessor. Setting Dependent without Predecessor would shift
// the dependent value into the predecessor's positional slot on the wire,
// silently corrupting the request. Build() rejects such combinations
// with errRequired.
type positionalDep struct {
	Dependent   builderField
	Predecessor builderField
}

type opKind uint8

const (
	opLit opKind = iota
	opField
	opList
	// opSwitch opens a switch{} block that dispatches across path variants.
	// The Value field is unused; conditions live on subsequent opCase ops.
	opSwitch
	// opCase opens one case label of the surrounding switch. Conditions
	// are carried in the .Conditions slice (ANDed together) and rendered
	// as a hasNonEmpty(...)/!= "" expression chain.
	opCase
	// opDefault opens the default: branch of the surrounding switch.
	// Used for the empty-fields variant when its body is non-empty (no
	// shared prefix factored its writes away).
	opDefault
	// opSwitchEnd closes a switch{} block opened by opSwitch.
	opSwitchEnd
	// opIf opens a single-condition if{} block. Used when a path has
	// exactly one optional variant body and a switch would be overkill.
	// Condition lives in the single-element .Conditions slice.
	opIf
	// opIfEnd closes an if{} block opened by opIf.
	opIfEnd
	// opExplainCheck emits inside a default: case the
	// "if any-optional-set { return explainErr }" guard that
	// distinguishes the valid empty-fields combination from invalid
	// field combinations the spec doesn't support. Conditions are the
	// OR'd presence checks for every optional field on the builder.
	opExplainCheck
)

func (k opKind) String() string {
	switch k {
	case opLit:
		return "lit"
	case opField:
		return "field"
	case opList:
		return "list"
	case opSwitch:
		return "switch"
	case opCase:
		return "case"
	case opDefault:
		return "default"
	case opSwitchEnd:
		return "switchEnd"
	case opIf:
		return "if"
	case opIfEnd:
		return "ifEnd"
	case opExplainCheck:
		return "explainCheck"
	default:
		return "unknown"
	}
}

// caseCondition is a single field-presence test inside an opCase. ANDed
// with sibling conditions on the same case to form the case label.
type caseCondition struct {
	Field  string // exported Go identifier
	IsList bool   // true => hasNonEmpty(p.Field); false => p.Field != ""
}

// emitOp is one instruction in the generated Build() method body.
type emitOp struct {
	Kind       opKind
	Value      string
	Conditions []caseCondition // populated for opCase and opIf
}

func (op emitOp) IsLit() bool       { return op.Kind == opLit }
func (op emitOp) IsField() bool     { return op.Kind == opField }
func (op emitOp) IsList() bool      { return op.Kind == opList }
func (op emitOp) IsSwitch() bool    { return op.Kind == opSwitch }
func (op emitOp) IsCase() bool      { return op.Kind == opCase }
func (op emitOp) IsDefault() bool   { return op.Kind == opDefault }
func (op emitOp) IsSwitchEnd() bool { return op.Kind == opSwitchEnd }
func (op emitOp) IsIf() bool           { return op.Kind == opIf }
func (op emitOp) IsIfEnd() bool        { return op.Kind == opIfEnd }
func (op emitOp) IsExplainCheck() bool { return op.Kind == opExplainCheck }

var pathParamRE = regexp.MustCompile(`\{([^}]+)\}`)

// analyzeGroup produces a builder from an operation group.
func analyzeGroup(g opGroup) (builder, error) {
	if len(g.pathSpecs) == 0 {
		return builder{}, fmt.Errorf("no path variants")
	}

	expandUnionPaths(&g)

	fields := deriveFields(g.pathSpecs)
	deps := derivePositionalDeps(g.pathSpecs, fields)
	ops, err := buildOps(g.pathSpecs, fields, deps)
	if err != nil {
		return builder{}, fmt.Errorf("operation group %q: %w", g.name, err)
	}
	name := pathBuilderName(g.name)

	deprecated, msg := groupDeprecation(g.pathSpecs)
	desc, docsURL, versionAdded, versionDeprecated, distros := groupMetadata(g.pathSpecs)

	return builder{
		StructName:         name,
		Comment:            fmt.Sprintf("%s builds URL paths for the %s operation.", name, g.name),
		Description:        desc,
		Group:              g.name,
		Deprecated:         deprecated,
		DeprecationMessage: msg,
		VersionAdded:       versionAdded,
		VersionDeprecated:  versionDeprecated,
		DocsURL:            docsURL,
		ExcludedDistros:    distros,
		Fields:             fields,
		Ops:                ops,
		PositionalDeps:     deps,
	}, nil
}

func groupDeprecation(paths []pathVariant) (bool, string) {
	allDeprecated := true
	var msg string
	for _, pv := range paths {
		if !pv.deprecated {
			allDeprecated = false
		}
		if pv.deprecationMessage != "" && msg == "" {
			msg = pv.deprecationMessage
		}
	}
	return allDeprecated, msg
}

func groupMetadata(paths []pathVariant) (desc, docsURL, versionAdded, versionDeprecated string, distros []string) {
	for _, pv := range paths {
		if pv.deprecated {
			if versionDeprecated == "" {
				versionDeprecated = pv.versionDeprecated
			}
			continue
		}
		if desc == "" && pv.description != "" {
			desc = pv.description
		}
		if docsURL == "" && pv.docsURL != "" {
			docsURL = pv.docsURL
		}
		if versionAdded == "" && pv.versionAdded != "" {
			versionAdded = pv.versionAdded
		}
	}

	// Fallback to deprecated variants if no non-deprecated ones exist.
	if desc == "" || versionAdded == "" {
		for _, pv := range paths {
			if desc == "" && pv.description != "" {
				desc = pv.description
			}
			if versionAdded == "" && pv.versionAdded != "" {
				versionAdded = pv.versionAdded
			}
			if versionDeprecated == "" && pv.versionDeprecated != "" {
				versionDeprecated = pv.versionDeprecated
			}
		}
	}

	// ExcludedDistros: only include distros excluded by ALL variants.
	if len(paths) > 0 && len(paths[0].excludedDistros) > 0 {
		candidates := make(map[string]int)
		for _, pv := range paths {
			for _, d := range pv.excludedDistros {
				candidates[d]++
			}
		}
		for d, count := range candidates {
			if count == len(paths) {
				distros = append(distros, d)
			}
		}
		sort.Strings(distros)
	}
	return
}

// export converts builder fields to exported identifiers.
func (b *builder) export() {
	rename := make(map[string]string, len(b.Fields))
	for i, f := range b.Fields {
		exported := pathFieldName(f.Param)
		rename[f.Name] = exported
		b.Fields[i].Name = exported
	}

	for i, op := range b.Ops {
		switch op.Kind {
		case opField, opList:
			if newName, ok := rename[op.Value]; ok {
				b.Ops[i].Value = newName
			}
		case opCase, opIf, opExplainCheck:
			for j, c := range op.Conditions {
				if newName, ok := rename[c.Field]; ok {
					b.Ops[i].Conditions[j].Field = newName
				}
			}
		}
	}

	for i, dep := range b.PositionalDeps {
		if newName, ok := rename[dep.Dependent.Name]; ok {
			b.PositionalDeps[i].Dependent.Name = newName
		}
		if newName, ok := rename[dep.Predecessor.Name]; ok {
			b.PositionalDeps[i].Predecessor.Name = newName
		}
	}
}

// derivePositionalDeps records pairs (dependent, predecessor) where every
// spec path containing dependent also contains predecessor. The check
// considers all field pairs in both directions because a dependency can
// run in either positional direction: cluster.state's Index requires the
// earlier-positioned Metric (forward dep), while cluster.stats's Metric
// requires the later-positioned NodeID (backward dep, since the spec has
// no /_cluster/stats/{metric} variant without /nodes/{node_id}).
// Required fields are skipped because the existing required-empty guard
// already covers them. Dependents emit one predecessor; transitively the
// chain still rejects every invalid combination.
//
// Example: cluster.state has paths /_cluster/state, /_cluster/state/{metric},
// and /_cluster/state/{metric}/{index}. No spec path contains {index} without
// {metric}, so derivePositionalDeps yields {Dependent: index, Predecessor:
// metric} and Build() rejects ClusterStatePath{Index: []string{"myidx"}} with
// errRequired. Without this guard, writeSegments would shift "myidx" into the
// {metric} slot and the server would silently match against /_cluster/state/myidx.
func derivePositionalDeps(paths []pathVariant, fields []builderField) []positionalDep {
	if len(fields) < 2 {
		return nil
	}
	var deps []positionalDep
	for j := 0; j < len(fields); j++ {
		fj := fields[j]
		if fj.Required {
			continue
		}
		for i := 0; i < len(fields); i++ {
			if i == j {
				continue
			}
			fi := fields[i]
			if fi.Required {
				continue
			}
			if !alwaysImplies(paths, fj.Param, fi.Param) {
				continue
			}
			deps = append(deps, positionalDep{Dependent: fj, Predecessor: fi})
			break // one predecessor per dependent is enough; further deps chain through it
		}
	}
	return deps
}

// alwaysImplies reports whether every spec path containing dependent also
// contains predecessor. Returns false in the vacuous case (no path contains
// dependent at all) so callers do not record a positional dependency for a
// field that never appears in practice; treat the relation as undefined
// rather than universally true.
func alwaysImplies(paths []pathVariant, dependent, predecessor string) bool {
	any := false
	for _, pv := range paths {
		hasDep := false
		hasPred := false
		for _, p := range pv.pathParams {
			if p == dependent {
				hasDep = true
			}
			if p == predecessor {
				hasPred = true
			}
		}
		if hasDep {
			any = true
			if !hasPred {
				return false
			}
		}
	}
	return any
}

// ---------------------------------------------------------------------------
// Variant-enumeration emit
// ---------------------------------------------------------------------------

// segmentAlias normalizes spec path segments that the documentation
// website renders without their underscores (e.g. "hotthreads" appears
// in some doc URLs but the OpenSearch endpoint is "/_nodes/{node_id}/hot_threads").
//
//nolint:gochecknoglobals // small immutable lookup table
var segmentAlias = map[string]string{
	"hotthreads": "hot_threads",
}

func canonicalSegment(seg string) string {
	if canon, ok := segmentAlias[seg]; ok {
		return canon
	}
	return seg
}

// buildOps converts the path variants of an operation group into a flat
// stream of emitOps that the template renders as a Build() body. The
// output has up to three regions:
//
//  1. Shared prefix: ops common to every variant in positional order.
//  2. Dispatch: a switch{} with one case per variant body, OR a single
//     if{} when only one variant has a non-empty body, OR nothing when
//     every variant collapses to prefix+suffix (e.g. a single-variant
//     path or all-required paths).
//  3. Shared suffix: ops common to every variant after the dispatch.
//
// Each switch case label expresses the exact field combination required
// by one spec path variant: every valid URL the spec defines becomes a
// case whose conditions match nothing else. Required fields and
// positional dependencies (e.g. ClusterStatePath.Index requires Metric)
// are guarded upfront, outside this function, so the switch only sees
// valid combinations.
func buildOps(paths []pathVariant, fields []builderField, deps []positionalDep) ([]emitOp, error) {
	fieldByParam := make(map[string]builderField, len(fields))
	for _, f := range fields {
		fieldByParam[f.Param] = f
	}

	// Multiple spec paths can map to the same variant fingerprint and
	// differ only in literal segment text (e.g. /{index}/_alias/{name}
	// and /{index}/_aliases/{name} are interchangeable URL forms of
	// indices.delete_alias). Defer canonical-form selection to the
	// spec via deprecated:true; reject ambiguous specs.
	paths, err := dedupeEquivalentVariants(paths)
	if err != nil {
		return nil, err
	}

	variantOps := make([][]emitOp, len(paths))
	variantOpts := make([][]builderField, len(paths))
	for i, pv := range paths {
		variantOps[i], variantOpts[i] = opsForVariant(pv, fieldByParam)
	}

	prefixLen, suffixLen := commonPrefixSuffix(variantOps)

	type body struct {
		ops    []emitOp
		fields []builderField
	}
	bodies := make([]body, len(paths))
	for i, ops := range variantOps {
		bodies[i] = body{
			ops:    append([]emitOp(nil), ops[prefixLen:len(ops)-suffixLen]...),
			fields: variantOpts[i],
		}
	}

	out := make([]emitOp, 0, 8)
	if prefixLen > 0 {
		out = append(out, variantOps[0][:prefixLen]...)
	}

	type liveCase struct {
		ops    []emitOp
		fields []builderField
	}
	var cases []liveCase
	var defaultBody []emitOp
	for _, b := range bodies {
		if len(b.ops) == 0 {
			continue
		}
		if len(b.fields) == 0 {
			defaultBody = b.ops
			continue
		}
		cases = append(cases, liveCase{ops: b.ops, fields: b.fields})
	}

	switch {
	case len(cases) == 0:
		// No optional bodies; either everything is shared, or only a
		// default body remains (a no-fields variant whose body wasn't
		// captured by the prefix/suffix). Emit defaultBody directly.
		out = append(out, defaultBody...)

	case len(cases) == 1 && len(deps) == 0:
		// writeSegments self-skips empty input, so an if guard
		// around `writeSegments(p.X)` for the same list field X is
		// redundant. Drop it; emit the body directly.
		body := cases[0].ops
		if len(body) == 1 && body[0].Kind == opList &&
			len(cases[0].fields) == 1 &&
			cases[0].fields[0].IsList &&
			cases[0].fields[0].Name == body[0].Value {
			out = append(out, body...)
		} else {
			out = append(out, emitOp{Kind: opIf, Conditions: conditionsFor(cases[0].fields)})
			out = append(out, body...)
			out = append(out, emitOp{Kind: opIfEnd})
		}
		out = append(out, defaultBody...)

	default:
		sort.SliceStable(cases, func(a, b int) bool {
			if len(cases[a].fields) != len(cases[b].fields) {
				return len(cases[a].fields) > len(cases[b].fields)
			}
			return joinFieldNames(cases[a].fields) < joinFieldNames(cases[b].fields)
		})
		out = append(out, emitOp{Kind: opSwitch})
		for _, c := range cases {
			out = append(out, emitOp{Kind: opCase, Conditions: conditionsFor(c.fields)})
			out = append(out, c.ops...)
		}
		needDefault := len(defaultBody) > 0 || len(deps) > 0
		if needDefault {
			out = append(out, emitOp{Kind: opDefault})
			// When deps exist, distinguish "valid empty combo" from
			// "invalid combo" before writing the empty-variant body:
			// the explain helper fires only when at least one optional
			// field is set, so the empty path falls through to the
			// body (or to nothing) cleanly.
			if len(deps) > 0 {
				out = append(out, emitOp{Kind: opExplainCheck, Conditions: optionalConditions(fields)})
			}
			out = append(out, defaultBody...)
		}
		out = append(out, emitOp{Kind: opSwitchEnd})
	}

	if suffixLen > 0 {
		out = append(out, variantOps[0][len(variantOps[0])-suffixLen:]...)
	}
	return out, nil
}

// opsForVariant produces the linear op stream for one path variant and
// returns the optional fields it consumes (in path-segment order).
// Required fields are emitted but excluded from the optional list since
// they're guarded upfront and never appear in a case condition.
func opsForVariant(pv pathVariant, fields map[string]builderField) ([]emitOp, []builderField) {
	segs := splitPath(pv.path)
	ops := make([]emitOp, 0, len(segs))
	var optFields []builderField
	for _, seg := range segs {
		if m := pathParamRE.FindStringSubmatch(seg); m != nil {
			f, ok := fields[m[1]]
			if !ok {
				continue
			}
			if f.IsList {
				ops = append(ops, emitOp{Kind: opList, Value: f.Name})
			} else {
				ops = append(ops, emitOp{Kind: opField, Value: f.Name})
			}
			if !f.Required {
				optFields = append(optFields, f)
			}
			continue
		}
		ops = append(ops, emitOp{Kind: opLit, Value: canonicalSegment(seg)})
	}
	return ops, optFields
}

// commonPrefixSuffix returns the length of the longest common prefix
// and the longest common suffix shared by every variant op stream. The
// two regions never overlap: prefixLen + suffixLen <= min(len(variant)).
func commonPrefixSuffix(variantOps [][]emitOp) (prefixLen, suffixLen int) {
	if len(variantOps) == 0 {
		return 0, 0
	}
	minLen := len(variantOps[0])
	for _, v := range variantOps[1:] {
		if len(v) < minLen {
			minLen = len(v)
		}
	}
	for prefixLen < minLen {
		ref := variantOps[0][prefixLen]
		match := true
		for _, v := range variantOps[1:] {
			if !opsEqual(v[prefixLen], ref) {
				match = false
				break
			}
		}
		if !match {
			break
		}
		prefixLen++
	}
	for prefixLen+suffixLen < minLen {
		ref := variantOps[0][len(variantOps[0])-1-suffixLen]
		match := true
		for _, v := range variantOps[1:] {
			if !opsEqual(v[len(v)-1-suffixLen], ref) {
				match = false
				break
			}
		}
		if !match {
			break
		}
		suffixLen++
	}
	return prefixLen, suffixLen
}

func opsEqual(a, b emitOp) bool {
	return a.Kind == b.Kind && a.Value == b.Value
}

func conditionsFor(fs []builderField) []caseCondition {
	out := make([]caseCondition, 0, len(fs))
	for _, f := range fs {
		out = append(out, caseCondition{Field: f.Name, IsList: f.IsList})
	}
	return out
}

// optionalConditions returns one case condition per non-required field
// in the builder. The opExplainCheck op renders these as an OR-chain to
// distinguish "user set no fields" (valid) from "user set fields but no
// case matched" (invalid combination).
func optionalConditions(fields []builderField) []caseCondition {
	out := make([]caseCondition, 0, len(fields))
	for _, f := range fields {
		if f.Required {
			continue
		}
		out = append(out, caseCondition{Field: f.Name, IsList: f.IsList})
	}
	return out
}

// joinFieldNames returns a sorted, comma-separated list of field names
// for stable case-ordering tie-breaks.
func joinFieldNames(fs []builderField) string {
	names := make([]string, 0, len(fs))
	for _, f := range fs {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// dedupeEquivalentVariants collapses spec path variants that share a
// structural fingerprint -- the same ordered sequence of (literal,
// param-name, array-ness) -- to a single representative. The selection
// rule defers to the spec: when a fingerprint group has exactly one
// non-deprecated variant, that variant is the canonical form. The
// generator does not invent canonicalness; the spec must declare it
// via deprecated:true on the redundant URL forms.
//
// When a fingerprint group has multiple non-deprecated equivalents,
// dedupeEquivalentVariants returns an error naming the offending
// paths. The fix is upstream: mark all but one variant as
// deprecated:true so the canonical URL is explicit.
//
// When every variant in a fingerprint group is deprecated, the whole
// builder will be flagged deprecated by groupDeprecation; emitting any
// one (alphabetically first for determinism) is sufficient since users
// already get the deprecation signal.
//
// Fingerprint encoding: ordered sequence of "L" for literal segments
// and "p:<name>" or "p:<name>[]" for param segments. Two variants
// agreeing on this fingerprint differ only in literal text and are
// interchangeable URLs of the same operation.
func dedupeEquivalentVariants(paths []pathVariant) ([]pathVariant, error) {
	if len(paths) <= 1 {
		return paths, nil
	}

	type key struct{ s string }
	fingerprint := func(pv pathVariant) key {
		var b strings.Builder
		for _, seg := range splitPath(pv.path) {
			if m := pathParamRE.FindStringSubmatch(seg); m != nil {
				b.WriteString("p:")
				b.WriteString(m[1])
				if pv.arrayParams[m[1]] {
					b.WriteString("[]")
				}
				b.WriteByte('|')
				continue
			}
			b.WriteString("L|")
		}
		return key{b.String()}
	}

	groups := make(map[key][]int)
	order := make([]key, 0, len(paths))
	for i, pv := range paths {
		k := fingerprint(pv)
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], i)
	}

	out := make([]pathVariant, 0, len(order))
	for _, k := range order {
		idxs := groups[k]
		if len(idxs) == 1 {
			out = append(out, paths[idxs[0]])
			continue
		}
		var live []int
		var deprecatedPaths []string
		for _, i := range idxs {
			if paths[i].deprecated {
				deprecatedPaths = append(deprecatedPaths, paths[i].path)
				continue
			}
			live = append(live, i)
		}
		switch len(live) {
		case 1:
			out = append(out, paths[live[0]])
		case 0:
			// Whole-group deprecation: groupDeprecation will flag the
			// builder, so users see the deprecation. Pick the
			// alphabetically first path for determinism.
			sort.SliceStable(idxs, func(a, b int) bool {
				return paths[idxs[a]].path < paths[idxs[b]].path
			})
			out = append(out, paths[idxs[0]])
		default:
			conflictPaths := make([]string, 0, len(live))
			for _, i := range live {
				conflictPaths = append(conflictPaths, paths[i].path)
			}
			return nil, fmt.Errorf(
				"spec has %d non-deprecated structurally-equivalent path variants in the same operation group; "+
					"mark all but one as deprecated:true so the canonical URL form is explicit "+
					"(non-deprecated: %v; deprecated: %v)",
				len(live), conflictPaths, deprecatedPaths,
			)
		}
	}
	return out, nil
}

func splitPath(path string) []string {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Field analysis
// ---------------------------------------------------------------------------

func deriveFields(paths []pathVariant) []builderField {
	paramCount := make(map[string]int)
	arrayParam := make(map[string]bool)
	for _, ps := range paths {
		for _, name := range ps.pathParams {
			paramCount[name]++
		}
		for name := range ps.arrayParams {
			arrayParam[name] = true
		}
	}

	total := len(paths)
	ordered := sortParamsByLongestPath(paths, paramCount)

	fields := make([]builderField, 0, len(ordered))
	for _, name := range ordered {
		fields = append(fields, builderField{
			Name:     unexportedFieldName(name),
			Param:    name,
			Required: paramCount[name] == total,
			IsList:   arrayParam[name],
		})
	}
	return fields
}

func sortParamsByLongestPath(paths []pathVariant, params map[string]int) []string {
	longest := longestPath(paths)
	matches := pathParamRE.FindAllStringSubmatch(longest, -1)

	var ordered []string
	seen := make(map[string]bool)
	for _, m := range matches {
		if !seen[m[1]] && params[m[1]] > 0 {
			ordered = append(ordered, m[1])
			seen[m[1]] = true
		}
	}
	var leftovers []string
	for name := range params {
		if !seen[name] {
			leftovers = append(leftovers, name)
		}
	}
	sort.Strings(leftovers)
	return append(ordered, leftovers...)
}

func longestPath(paths []pathVariant) string {
	longest := ""
	for _, ps := range paths {
		if len(ps.path) > len(longest) {
			longest = ps.path
		}
	}
	return longest
}
