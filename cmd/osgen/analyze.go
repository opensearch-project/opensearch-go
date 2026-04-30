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
	Group              string
	Deprecated         bool
	DeprecationMessage string
	Fields             []builderField
	Ops                []emitOp
}

// builderField represents a field in the generated struct.
type builderField struct {
	Name     string // Go field name (unexported)
	Param    string // original parameter name from spec
	Required bool   // present in ALL path variants for this group
	IsList   bool   // comma-separated list (indices)
}

// opKind identifies the type of instruction in a generated Build() method body.
type opKind uint8

const (
	opLit        opKind = iota // literal path segment
	opField                   // required scalar field
	opList                    // required list field (comma-joined)
	opIfList                  // guard: if len(field) > 0
	opIfStr                   // guard: if field != ""
	opElseIfList              // else-if guard for list
	opElseIfStr               // else-if guard for string
	opElse                    // else branch
	opEnd                     // close guard block
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

// emitOp is one instruction in the generated build() method body.
// The template iterates these linearly to produce Go code.
type emitOp struct {
	Kind  opKind
	Value string // literal value or Go field name
}

var pathParamRE = regexp.MustCompile(`\{([^}]+)\}`)

// analyzeGroup produces a builder from an operation group.
func analyzeGroup(g opGroup) (builder, error) {
	if len(g.pathSpecs) == 0 {
		return builder{}, fmt.Errorf("no path variants")
	}

	fields := deriveFields(g.pathSpecs)
	ops := buildOps(g.pathSpecs, fields)
	structName := deriveStructName(g.name)

	deprecated, msg := groupDeprecation(g.pathSpecs)

	return builder{
		StructName:         structName,
		Comment:            fmt.Sprintf("%s builds paths for the %s operation group.", structName, g.name),
		Group:              g.name,
		Deprecated:         deprecated,
		DeprecationMessage: msg,
		Fields:             fields,
		Ops:                ops,
	}, nil
}

// groupDeprecation returns true if ALL path variants for a group are
// deprecated, along with the first non-empty deprecation message.
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

// export converts the builder to use exported identifiers: struct name, field
// names, and all op Value references that point at fields.
func (b *builder) export() {
	// Build a rename map: old unexported field name -> new exported field name.
	rename := make(map[string]string, len(b.Fields))
	for i, f := range b.Fields {
		exported := exportName(f.Name)
		rename[f.Name] = exported
		b.Fields[i].Name = exported
	}

	// Export struct name.
	b.StructName = strings.ToUpper(b.StructName[:1]) + b.StructName[1:]
	b.Comment = b.StructName + b.Comment[strings.Index(b.Comment, " builds"):]

	// Rename field references in ops.
	for i, op := range b.Ops {
		switch op.Kind {
		case opField, opList, opIfList, opIfStr, opElseIfList, opElseIfStr:
			if newName, ok := rename[op.Value]; ok {
				b.Ops[i].Value = newName
			}
		}
	}
}

// exportName converts an unexported Go identifier to exported, respecting
// common Go initialisms (ID, URL, HTTP, etc.).
func exportName(name string) string {
	if strings.EqualFold(name, "id") {
		return "ID"
	}
	if strings.HasSuffix(name, "ID") {
		return strings.ToUpper(name[:1]) + name[1:]
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// ---------------------------------------------------------------------------
// Trie construction and DFS
// ---------------------------------------------------------------------------

// pathTrie is a trie over URL path segments for one operation group.
// Literal children are tried before wildcard (param) children during DFS so
// that the generated code prefers the most constrained branch.
type pathTrie struct {
	children   map[string]*pathTrie // literal segment -> child
	wilds      []*wildChild         // param segment children (ordered for DFS)
	terminal   bool                 // a complete path ends at this node
	deprecated bool                 // all paths through this node are deprecated
}

type wildChild struct {
	param string // spec parameter name (e.g. "index")
	node  *pathTrie
}

// insert adds a path variant to the trie. If the path is deprecated,
// all newly-created nodes are marked deprecated. Existing nodes that gain
// a non-deprecated path have their deprecated flag cleared.
func (t *pathTrie) insert(segments []string, deprecated bool) {
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
		} else {
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
	}
	node.terminal = true
}

// buildOps constructs a trie from path variants and DFS-walks it to produce
// the emit instruction sequence for build().
func buildOps(paths []pathVariant, fields []builderField) []emitOp {
	fieldByParam := make(map[string]builderField)
	for _, f := range fields {
		fieldByParam[f.Param] = f
	}

	root := &pathTrie{}
	for _, pv := range paths {
		segments := splitPath(pv.path)
		root.insert(segments, pv.deprecated)
	}

	var ops []emitOp
	emitTrie(root, fieldByParam, &ops)
	return ops
}

// routeScore ranks path prefixes when multiple literal children exist at
// the same trie level (e.g., _plugins vs _opendistro). Higher score wins.
type routeScore int

const (
	scoreDeprecated routeScore = 1
	scorePreferred  routeScore = 100
)

// segmentAlias maps path segment synonyms to their canonical form.
// These collapse into the same trie node during insertion.
var segmentAlias = map[string]string{
	"_aliases":   "_alias",
	"hotthreads": "hot_threads",
}

func canonicalSegment(seg string) string {
	if canon, ok := segmentAlias[seg]; ok {
		return canon
	}
	return seg
}

// preferredLiteral picks the best literal child from a set of alternatives
// at the same trie level. Non-deprecated children are preferred over
// deprecated ones; ties are broken alphabetically for determinism.
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

// emitTrie performs DFS on the trie, producing emit ops.
// It recognizes when a wildcard branch subsumes the literal sibling
// (list params are no-ops when empty) and avoids unnecessary branching.
//
// A "wild child" is a trie edge representing a path parameter (e.g., {index}).
// At any trie node, literal children are fixed segments like "_alias" or "_search",
// while wild children are parameterized segments.
//
// Subsumption example - indices.get_alias has paths:
//
//	/_alias                    (no params)
//	/_alias/{name}             (name only)
//	/{index}/_alias            (index only)
//	/{index}/_alias/{name}     (both)
//
// The trie at root has:
//
//	root
//	+-- {index} [wild, list]  ->  _alias  ->  {name} [wild, list]
//	+-- _alias [literal]      ->  {name} [wild, list]
//
// The wild child {index} is list-typed, and its subtree (_alias -> {name})
// contains the same literal "_alias" that appears as a direct child of root.
// Since writeIndices is a no-op for an empty slice, the wild path with
// index=[] produces identical output to the literal-only path. We call this
// "subsumption" and emit a single linear sequence instead of branching:
//
//	writeIndices(pb, p.index)  // no-op when empty
//	pb.writeReq("_alias")
//	writeIndices(pb, p.name)   // no-op when empty
//
// Contrast with nodes.info (genuine divergence, no subsumption):
//
//	/_nodes                        (no params)
//	/_nodes/{node_id_or_metric}    (1 scalar param)
//	/_nodes/{node_id}/{metric}     (2 scalar params)
//
// After the shared prefix "_nodes", the trie has two wild children with
// different scalar params. Neither is list-typed, so neither subsumes the
// other. Here we must branch:
//
//	pb.writeReq("_nodes")
//	if p.nodeID != "" {
//	    pb.writeReq(p.nodeID)
//	    if p.metric != "" { pb.writeReq(p.metric) }
//	} else if p.nodeIDOrMetric != "" {
//	    pb.writeReq(p.nodeIDOrMetric)
//	}
func emitTrie(node *pathTrie, fields map[string]builderField, ops *[]emitOp) {
	// Single literal child, no wilds: shared segment, emit unconditionally.
	if len(node.children) == 1 && len(node.wilds) == 0 {
		for seg, child := range node.children {
			*ops = append(*ops, emitOp{Kind: opLit, Value: seg})
			emitTrie(child, fields, ops)
		}
		return
	}

	// Multiple literal children, no wilds: pick preferred (synonym resolution).
	if len(node.children) > 1 && len(node.wilds) == 0 {
		seg, child := preferredLiteral(node.children)
		*ops = append(*ops, emitOp{Kind: opLit, Value: seg})
		emitTrie(child, fields, ops)
		return
	}

	// Single wild child, no literal siblings.
	if len(node.children) == 0 && len(node.wilds) == 1 {
		wc := node.wilds[0]
		f := fields[wc.param]
		if f.Required {
			emitField(f, ops)
			emitTrie(wc.node, fields, ops)
		} else if f.IsList {
			// List params are no-ops when empty - no guard needed.
			emitField(f, ops)
			emitTrie(wc.node, fields, ops)
		} else {
			emitGuardOpen(f, ops, "if")
			emitField(f, ops)
			emitTrie(wc.node, fields, ops)
			*ops = append(*ops, emitOp{Kind: opEnd})
		}
		return
	}

	// Mixed node: wild child(ren) + literal child(ren).
	// Check for subsumption: if one wild child's subtree contains ALL the
	// literal children of this node, the wild path subsumes the literal path.
	if len(node.wilds) == 1 {
		wc := node.wilds[0]
		f := fields[wc.param]
		if literalsSubsumed(node.children, wc.node) {
			if f.IsList {
				// writeIndices is a no-op for empty slices - no guard needed.
				emitField(f, ops)
			} else {
				// Scalar param: guard the field emission, then fall through
				// to the shared suffix unconditionally.
				emitGuardOpen(f, ops, "if")
				emitField(f, ops)
				*ops = append(*ops, emitOp{Kind: opEnd})
			}
			emitTrie(wc.node, fields, ops)
			return
		}
	}

	// Genuine divergence: sort branches by depth (most specific first),
	// emit if/else chain.
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

	// Close the if chain if the last branch was a wild (not closed by literal else).
	if len(branches) > 0 && !branches[len(branches)-1].isLiteral {
		*ops = append(*ops, emitOp{Kind: opEnd})
	}
}

// literalsSubsumed returns true if every literal child of the current node
// also exists as a literal child of the wild child's subtree node.
// This means the wild path (with empty value) produces the same result
// as the literal-only path.
func literalsSubsumed(currentLiterals map[string]*pathTrie, wildChildNode *pathTrie) bool {
	for seg := range currentLiterals {
		if _, ok := wildChildNode.children[seg]; !ok {
			return false
		}
	}
	return true
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

// trieDepth returns the maximum depth of the trie from this node.
func trieDepth(node *pathTrie) int {
	max := 0
	for _, child := range node.children {
		d := trieDepth(child)
		if d > max {
			max = d
		}
	}
	for _, wc := range node.wilds {
		d := trieDepth(wc.node)
		if d > max {
			max = d
		}
	}
	return max + 1
}

// splitPath splits a URL path into non-empty segments.
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

// deriveFields determines struct fields from path variants.
// A field is required if its parameter appears in ALL variants; otherwise optional.
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
			Name:     goFieldName(name),
			Param:    name,
			Required: paramCount[name] == total,
			IsList:   arrayParam[name],
		})
	}
	return fields
}

// sortParamsByLongestPath returns params ordered by their position in the longest path.
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
	ordered = append(ordered, leftovers...)
	return ordered
}

// longestPath returns the longest path string among variants.
func longestPath(paths []pathVariant) string {
	longest := ""
	for _, ps := range paths {
		if len(ps.path) > len(longest) {
			longest = ps.path
		}
	}
	return longest
}

// ---------------------------------------------------------------------------
// Naming
// ---------------------------------------------------------------------------

// deriveStructName converts an x-operation-group to an unexported Go struct name.
// Core operations (search, get, index, bulk, etc.) are unnamespaced in the spec,
// while feature areas use a dotted prefix (indices.create, cat.health, nodes.info).
// There is no "_core." prefix to strip; the spec simply omits the namespace for core ops.
func deriveStructName(group string) string {
	parts := strings.Split(group, ".")
	var result strings.Builder
	for i, part := range parts {
		words := strings.Split(part, "_")
		for j, w := range words {
			if len(w) == 0 {
				continue
			}
			if i == 0 && j == 0 {
				result.WriteString(w)
			} else {
				result.WriteString(strings.ToUpper(w[:1]) + w[1:])
			}
		}
	}
	result.WriteString("Path")
	return result.String()
}

// goFieldName converts a spec parameter name to an unexported Go field name.
func goFieldName(param string) string {
	parts := strings.Split(param, "_")
	var result strings.Builder
	for i, p := range parts {
		if len(p) == 0 {
			continue
		}
		if strings.EqualFold(p, "id") {
			if i == 0 {
				result.WriteString("id")
			} else {
				result.WriteString("ID")
			}
		} else if i == 0 {
			result.WriteString(p)
		} else {
			result.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	name := result.String()
	if isGoKeyword(name) {
		return name + "Val"
	}
	return name
}

func isGoKeyword(s string) bool {
	switch s {
	case "break", "case", "chan", "const", "continue",
		"default", "defer", "else", "fallthrough", "for",
		"func", "go", "goto", "if", "import",
		"interface", "map", "package", "range", "return",
		"select", "struct", "switch", "type", "var":
		return true
	}
	return false
}
