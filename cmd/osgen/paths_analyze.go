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
}

// builderField represents a field in the generated struct.
type builderField struct {
	Name     string
	Param    string
	Required bool
	IsList   bool
}

type opKind uint8

const (
	opLit opKind = iota
	opField
	opList
	opIfList
	opIfStr
	opElseIfList
	opElseIfStr
	opElse
	opEnd
)

func (k opKind) String() string {
	switch k {
	case opLit:
		return "lit"
	case opField:
		return "field"
	case opList:
		return "list"
	case opIfList:
		return "ifList"
	case opIfStr:
		return "ifStr"
	case opElseIfList:
		return "elseIfList"
	case opElseIfStr:
		return "elseIfStr"
	case opElse:
		return "else"
	case opEnd:
		return "end"
	default:
		return "unknown"
	}
}

// emitOp is one instruction in the generated Build() method body.
type emitOp struct {
	Kind  opKind
	Value string
}

func (op emitOp) IsLit() bool        { return op.Kind == opLit }
func (op emitOp) IsField() bool      { return op.Kind == opField }
func (op emitOp) IsList() bool       { return op.Kind == opList }
func (op emitOp) IsIfList() bool     { return op.Kind == opIfList }
func (op emitOp) IsIfStr() bool      { return op.Kind == opIfStr }
func (op emitOp) IsElseIfList() bool { return op.Kind == opElseIfList }
func (op emitOp) IsElseIfStr() bool  { return op.Kind == opElseIfStr }
func (op emitOp) IsElse() bool       { return op.Kind == opElse }
func (op emitOp) IsEnd() bool        { return op.Kind == opEnd }

var pathParamRE = regexp.MustCompile(`\{([^}]+)\}`)

// analyzeGroup produces a builder from an operation group.
func analyzeGroup(g opGroup) (builder, error) {
	if len(g.pathSpecs) == 0 {
		return builder{}, fmt.Errorf("no path variants")
	}

	fields := deriveFields(g.pathSpecs)
	ops := buildOps(g.pathSpecs, fields)
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
		case opField, opList, opIfList, opIfStr, opElseIfList, opElseIfStr:
			if newName, ok := rename[op.Value]; ok {
				b.Ops[i].Value = newName
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Path trie construction and DFS
// ---------------------------------------------------------------------------

type pathTrie struct {
	children   map[string]*pathTrie
	wilds      []*wildChild
	terminal   bool
	deprecated bool
	methods    map[string]struct{} // HTTP methods at this terminal (nil for non-terminals)
}

type wildChild struct {
	param string
	node  *pathTrie
}

func (t *pathTrie) insert(segments []string, deprecated bool, methods map[string]struct{}) {
	node := t
	for _, seg := range segments {
		if m := pathParamRE.FindStringSubmatch(seg); m != nil {
			paramName := m[1]
			var child *pathTrie
			for _, wc := range node.wilds {
				if wc.param == paramName {
					child = wc.node
					break
				}
			}
			if child == nil {
				child = &pathTrie{deprecated: deprecated}
				node.wilds = append(node.wilds, &wildChild{param: paramName, node: child})
			} else if !deprecated {
				child.deprecated = false
			}
			node = child
			continue
		}

		seg = canonicalSegment(seg)
		if node.children == nil {
			node.children = make(map[string]*pathTrie)
		}
		child, ok := node.children[seg]
		if !ok {
			child = &pathTrie{deprecated: deprecated}
			node.children[seg] = child
		} else if !deprecated {
			child.deprecated = false
		}
		node = child
	}
	node.terminal = true
	if node.methods == nil {
		node.methods = make(map[string]struct{}, len(methods))
	}
	for m := range methods {
		node.methods[m] = struct{}{}
	}
}

func buildOps(paths []pathVariant, fields []builderField) []emitOp {
	fieldByParam := make(map[string]builderField)
	for _, f := range fields {
		fieldByParam[f.Param] = f
	}

	root := &pathTrie{}
	for _, pv := range paths {
		segments := splitPath(pv.path)
		root.insert(segments, pv.deprecated, pv.methods)
	}

	var ops []emitOp
	emitTrie(root, fieldByParam, &ops)
	return ops
}

type routeScore int

const (
	scoreDeprecated routeScore = 1
	scorePreferred  routeScore = 100
)

var segmentAlias = map[string]string{
	"hotthreads": "hot_threads",
}

func canonicalSegment(seg string) string {
	if canon, ok := segmentAlias[seg]; ok {
		return canon
	}
	return seg
}

func preferredLiteral(children map[string]*pathTrie) (string, *pathTrie) {
	type entry struct {
		seg   string
		node  *pathTrie
		score routeScore
	}
	entries := make([]entry, 0, len(children))
	for seg, child := range children {
		s := scorePreferred
		if child.deprecated {
			s = scoreDeprecated
		}
		entries = append(entries, entry{seg: seg, node: child, score: s})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].score != entries[j].score {
			return entries[i].score > entries[j].score
		}
		return entries[i].seg < entries[j].seg
	})
	return entries[0].seg, entries[0].node
}

func emitTrie(node *pathTrie, fields map[string]builderField, ops *[]emitOp) {
	if len(node.children) == 1 && len(node.wilds) == 0 {
		for seg, child := range node.children {
			// If the literal's subtree only leads to terminals through wild
			// params (no terminal reachable via literals alone), guard the
			// literal on the first wild param it leads to. Otherwise the
			// literal is always emitted even when all fields are empty.
			if !terminalViaLiterals(child) && !terminalViaRequired(child, fields) {
				if guardField, ok := firstWildField(child, fields); ok && !guardField.Required {
					emitGuardOpen(guardField, ops, "if")
					*ops = append(*ops, emitOp{Kind: opLit, Value: seg})
					emitTrie(child, fields, ops)
					*ops = append(*ops, emitOp{Kind: opEnd})
					return
				}
			}
			*ops = append(*ops, emitOp{Kind: opLit, Value: seg})
			emitTrie(child, fields, ops)
		}
		return
	}

	if len(node.children) > 1 && len(node.wilds) == 0 {
		seg, child := preferredLiteral(node.children)
		*ops = append(*ops, emitOp{Kind: opLit, Value: seg})
		emitTrie(child, fields, ops)
		return
	}

	if len(node.children) == 0 && len(node.wilds) == 1 {
		wc := node.wilds[0]
		f := fields[wc.param]
		if f.Required {
			emitField(f, ops)
			emitTrie(wc.node, fields, ops)
			return
		}
		if f.IsList {
			emitField(f, ops)
			emitTrie(wc.node, fields, ops)
			return
		}
		emitGuardOpen(f, ops, "if")
		emitField(f, ops)
		emitTrie(wc.node, fields, ops)
		*ops = append(*ops, emitOp{Kind: opEnd})
		return
	}

	if len(node.wilds) == 1 {
		wc := node.wilds[0]
		f := fields[wc.param]
		if literalsSubsumed(node.children, wc.node) {
			if f.IsList {
				emitField(f, ops)
			} else {
				emitGuardOpen(f, ops, "if")
				emitField(f, ops)
				*ops = append(*ops, emitOp{Kind: opEnd})
			}
			emitTrie(wc.node, fields, ops)
			return
		}
	}

	type branch struct {
		isLiteral bool
		litSeg    string
		litNode   *pathTrie
		wc        *wildChild
		depth     int
	}

	var branches []branch
	for seg, child := range node.children {
		branches = append(branches, branch{isLiteral: true, litSeg: seg, litNode: child, depth: trieDepth(child)})
	}
	for _, wc := range node.wilds {
		branches = append(branches, branch{wc: wc, depth: trieDepth(wc.node)})
	}
	sort.Slice(branches, func(i, j int) bool {
		return branches[i].depth > branches[j].depth
	})

	first := true
	for _, br := range branches {
		if br.isLiteral {
			if !first {
				*ops = append(*ops, emitOp{Kind: opElse})
			}
			*ops = append(*ops, emitOp{Kind: opLit, Value: br.litSeg})
			emitTrie(br.litNode, fields, ops)
			if !first {
				*ops = append(*ops, emitOp{Kind: opEnd})
			}
		} else {
			f := fields[br.wc.param]
			kind := "if"
			if !first {
				kind = "elseIf"
			}
			emitGuardOpen(f, ops, kind)
			emitField(f, ops)
			emitTrie(br.wc.node, fields, ops)
		}
		first = false
	}

	if len(branches) > 0 && !branches[len(branches)-1].isLiteral {
		*ops = append(*ops, emitOp{Kind: opEnd})
	}
}

func literalsSubsumed(currentLiterals map[string]*pathTrie, wildChildNode *pathTrie) bool {
	for seg := range currentLiterals {
		if _, ok := wildChildNode.children[seg]; !ok {
			return false
		}
	}
	return true
}

// terminalViaLiterals returns true if node (or a descendant reachable only
// through literal children) is terminal. In other words, there exists a valid
// path that ends at or below this node without needing any wild parameters.
func terminalViaLiterals(node *pathTrie) bool {
	if node.terminal {
		return true
	}
	for _, child := range node.children {
		if terminalViaLiterals(child) {
			return true
		}
	}
	return false
}

// terminalViaRequired reports whether a terminal is reachable from node
// by traversing literals and required wild parameters (but not optional
// ones). This prevents wrapping shared literal prefixes inside an
// optional-field guard when the subtree has a valid path that doesn't
// depend on the optional field.
func terminalViaRequired(node *pathTrie, fields map[string]builderField) bool {
	if node.terminal {
		return true
	}
	for _, child := range node.children {
		if terminalViaRequired(child, fields) {
			return true
		}
	}
	for _, wc := range node.wilds {
		if f, ok := fields[wc.param]; ok && f.Required {
			if terminalViaRequired(wc.node, fields) {
				return true
			}
		}
	}
	return false
}

// firstWildField finds the first wild parameter reachable from node by
// traversing only literal children and single wilds. Returns the corresponding
// builderField for use as a guard condition.
func firstWildField(node *pathTrie, fields map[string]builderField) (builderField, bool) {
	for {
		if len(node.wilds) > 0 {
			f, ok := fields[node.wilds[0].param]
			return f, ok
		}
		if len(node.children) == 1 {
			for _, child := range node.children {
				node = child
			}
			continue
		}
		return builderField{}, false
	}
}

func emitField(f builderField, ops *[]emitOp) {
	if f.IsList {
		*ops = append(*ops, emitOp{Kind: opList, Value: f.Name})
	} else {
		*ops = append(*ops, emitOp{Kind: opField, Value: f.Name})
	}
}

func emitGuardOpen(f builderField, ops *[]emitOp, kind string) {
	if f.IsList {
		switch kind {
		case "if":
			*ops = append(*ops, emitOp{Kind: opIfList, Value: f.Name})
		case "elseIf":
			*ops = append(*ops, emitOp{Kind: opElseIfList, Value: f.Name})
		}
	} else {
		switch kind {
		case "if":
			*ops = append(*ops, emitOp{Kind: opIfStr, Value: f.Name})
		case "elseIf":
			*ops = append(*ops, emitOp{Kind: opElseIfStr, Value: f.Name})
		}
	}
}

func trieDepth(node *pathTrie) int {
	max := 0
	for _, child := range node.children {
		if d := trieDepth(child); d > max {
			max = d
		}
	}
	for _, wc := range node.wilds {
		if d := trieDepth(wc.node); d > max {
			max = d
		}
	}
	return max + 1
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
