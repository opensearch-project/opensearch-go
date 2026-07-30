// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

// A struct that embeds a type and redeclares one of the embedded type's JSON
// tags shadows it: encoding/json resolves a duplicate tag at differing depths in
// favor of the shallower field, so the embedded declaration is never populated.
// Some shadows are deliberate (an aggregate narrowing `buckets` to a concrete
// bucket type), but the same shape silently made the whole per-hit search
// envelope unreachable when the shallower field carried less than the one it hid.
// go vet's structtag analyzer does not catch this: it only compares tags declared
// within a single struct, not across an embed boundary. The guard pins the
// permitted set in a checked-in allowlist and fails generation when an unlisted
// shadow appears. See [guardTagShadows].

// tagShadowAllowlistFile is the checked-in allowlist's filename: the default
// -update write target, and the name reported in messages. The //go:embed
// directive below repeats the literal because a directive cannot reference a
// const.
const tagShadowAllowlistFile = "tagshadow_allowlist.txt"

// embeddedTagShadowAllowlist is the checked-in allowlist compiled into the
// binary, so the check enforces the same set regardless of the process working
// directory. Disk reads happen only for an explicit override (see
// TagShadowConfig.AllowlistPath).
//
//go:embed tagshadow_allowlist.txt
var embeddedTagShadowAllowlist []byte

// shadowKind classifies what the shallower declaration does to the payload of
// the embedded declaration it hides. It is not part of the allowlist key; it
// drives the informational trailing comment and the offender message, so a
// reviewer can tell a deliberate override from a silent loss of fields.
type shadowKind int

const (
	// shadowNarrowing names a more specific type than the field it hides, which
	// is the whole point of the override (e.g. a terms aggregate replacing the
	// base's erased bucket union with its concrete bucket type).
	shadowNarrowing shadowKind = iota
	// shadowErased replaces a typed payload with raw JSON, so every field of the
	// hidden type becomes unreachable. This is the shape that made the per-hit
	// search envelope unreachable, and is almost never intended.
	shadowErased
	// shadowRedundant restates the hidden field's own type. Harmless on the wire
	// but pointless, and it makes the struct read as if the embed were absent.
	shadowRedundant
)

// Allowlist kind labels written as part of the trailing "# <label>" comment on
// each entry. Informational only (ignored on load), but kept as consts so the
// shadowKind.String mapping has a single source of truth.
const (
	shadowKindLabelNarrowing = "intentional narrowing"
	shadowKindLabelErased    = "erases a typed payload"
	shadowKindLabelRedundant = "redundant redeclaration"
)

func (k shadowKind) String() string {
	switch k {
	case shadowNarrowing:
		return shadowKindLabelNarrowing
	case shadowErased:
		return shadowKindLabelErased
	case shadowRedundant:
		return shadowKindLabelRedundant
	default:
		return shadowKindLabelErased
	}
}

// tagShadow is one occurrence of a JSON tag declared on a struct that is also
// reachable through one of that struct's embedded types.
type tagShadow struct {
	// Outer is the Go type declaring the shallower (winning) field.
	Outer string
	// JSONName is the duplicated JSON tag.
	JSONName string
	// Declaring is the Go type that declares the shadowed (losing) field. It is
	// the type at the far end of Chain, which may be deeper than the embed named
	// on Outer.
	Declaring string
	// Chain is the embed path from Outer inward, ending at Declaring. Its first
	// element is the type embedded directly in Outer.
	Chain []string
	// OuterGoType and ShadowedGoType are the two field types, for the offender
	// message and the allowlist comment.
	OuterGoType    string
	ShadowedGoType string
	Kind           shadowKind
	group          string // schema group, for grouped output only (not part of the key)
}

// key is the allowlist line key: "OuterType/jsonTag/DeclaringType". The
// declaring type is part of the key so that re-pointing an embed at a different
// base invalidates the entry and forces a fresh review instead of silently
// inheriting the old approval.
func (s tagShadow) key() string { return s.Outer + "/" + s.JSONName + "/" + s.Declaring }

// TagShadowConfig controls the duplicate-JSON-tag shadowing guard.
type TagShadowConfig struct {
	// AllowlistPath overrides the embedded allowlist with a file, relative to
	// cwd. Empty (the default) checks against embeddedTagShadowAllowlist. Under
	// Update it is the write target, defaulting to tagShadowAllowlistFile in cwd
	// when empty.
	AllowlistPath string
	// Update rewrites the allowlist file from the current output instead of
	// checking against it.
	Update bool
	// AllowUnlisted downgrades the fatal check to a warning.
	AllowUnlisted bool
}

// allowlistSource names the allowlist the check consulted, for messages.
func (c TagShadowConfig) allowlistSource() string {
	if c.AllowlistPath == "" {
		return "embedded " + tagShadowAllowlistFile
	}
	return fmt.Sprintf("%q", c.AllowlistPath)
}

// shadowedField is a field reachable through an embed chain, along with the type
// that declares it and the chain that reaches it.
type shadowedField struct {
	Field     ir.Field
	Declaring string
	Chain     []string
}

// isWireField reports whether a field participates in JSON encoding under its
// own tag. Embedded fields carry no tag of their own, a field with no exported
// Go name is not rendered, and `json:"-"` is never a wire name.
func isWireField(f ir.Field) bool {
	return !f.IsEmbed && f.GoName != "" && f.JSONName != "" && f.JSONName != "-"
}

// indexTypesByName maps every Go type name the emitter renders to its IR type,
// so an embed can be resolved to the fields it contributes. Sibling types are
// converted copies of registry entries, so the same name arrives more than once;
// the first wins, which is stable because all copies carry the same fields.
func indexTypesByName(spec *ir.Spec) map[string]*ir.Type {
	byName := make(map[string]*ir.Type)
	add := func(t *ir.Type) {
		if t == nil || t.Name == "" {
			return
		}
		if _, ok := byName[t.Name]; !ok {
			byName[t.Name] = t
		}
	}

	for _, op := range spec.Operations {
		add(op.Response)
		add(op.RequestBody)
		add(op.RespElemType)
		for _, st := range op.SiblingTypes {
			add(st)
		}
		for _, st := range op.ReqBodySiblings {
			add(st)
		}
	}
	for _, t := range spec.Types {
		add(t)
	}
	return byName
}

// reachableTags returns every JSON tag reachable from the type named embedType
// through it and its own embeds, keyed by tag. The walk is breadth-first so the
// shallowest declaration of a tag wins, matching how encoding/json resolves a
// duplicate across depths. An unresolvable embed (a name the emitter renders but
// the IR does not carry, e.g. a hand-written type) contributes nothing rather
// than aborting: the guard is diagnostic, and a missing branch can only under-
// report. A visited set keeps a cyclic embed from looping forever.
func reachableTags(byName map[string]*ir.Type, embedType string) map[string]shadowedField {
	tags := make(map[string]shadowedField)
	visited := newSet(embedType)

	type queued struct {
		name  string
		chain []string
	}
	queue := []queued{{name: embedType, chain: []string{embedType}}}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		t, ok := byName[cur.name]
		if !ok {
			continue
		}
		for _, f := range t.Fields {
			if f.IsEmbed {
				child := unwrapTypeName(f.GoType)
				if child == "" || visited.has(child) {
					continue
				}
				visited.add(child)
				queue = append(queue, queued{name: child, chain: append(append([]string(nil), cur.chain...), child)})
				continue
			}
			if !isWireField(f) {
				continue
			}
			if _, dup := tags[f.JSONName]; dup {
				continue
			}
			tags[f.JSONName] = shadowedField{Field: f, Declaring: cur.name, Chain: cur.chain}
		}
	}
	return tags
}

// classifyShadow reports what the shallower declaration does to the payload of
// the field it hides: names a more specific type (the deliberate override),
// restates the same type, or replaces a typed payload with raw JSON.
func classifyShadow(outerGoType, shadowedGoType string) shadowKind {
	if outerGoType == shadowedGoType {
		return shadowRedundant
	}
	_, outerRaw := classifyRawForm(outerGoType)
	_, shadowedRaw := classifyRawForm(shadowedGoType)
	if outerRaw && !shadowedRaw {
		return shadowErased
	}
	return shadowNarrowing
}

// collectTagShadows walks the IR and returns every duplicate-tag shadow,
// deduplicated by key. It reads the IR rather than the emitted text, so a shadow
// is caught before any file is written.
//
// Only a tag declared on the outer struct itself can shadow: encoding/json
// resolves a duplicate at differing depths in favor of the shallower field, and
// the outer struct's own fields are the shallowest there are. Two embeds that
// collide with each other at equal depth are a different failure (both fields
// are dropped) and are not this guard's subject.
func collectTagShadows(spec *ir.Spec) []tagShadow {
	byName := indexTypesByName(spec)

	// Reachability is per embedded type, and one base is embedded by many
	// aggregates, so cache the walks.
	cache := make(map[string]map[string]shadowedField)
	reachable := func(embedType string) map[string]shadowedField {
		if tags, ok := cache[embedType]; ok {
			return tags
		}
		tags := reachableTags(byName, embedType)
		cache[embedType] = tags
		return tags
	}

	seen := make(set[string])
	var shadows []tagShadow

	// Iterate the index in name order: it is a map, so the walk would otherwise
	// vary from run to run, and the offender list is user-facing output.
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		t := byName[name]
		for _, f := range t.Fields {
			if !isWireField(f) {
				continue
			}
			for _, e := range t.Fields {
				if !e.IsEmbed {
					continue
				}
				embedType := unwrapTypeName(e.GoType)
				sf, ok := reachable(embedType)[f.JSONName]
				if !ok {
					continue
				}
				s := tagShadow{
					Outer:          t.Name,
					JSONName:       f.JSONName,
					Declaring:      sf.Declaring,
					Chain:          sf.Chain,
					OuterGoType:    f.GoType,
					ShadowedGoType: sf.Field.GoType,
					Kind:           classifyShadow(f.GoType, sf.Field.GoType),
					group:          schemaGroup(t.SchemaRef),
				}
				if seen.has(s.key()) {
					continue
				}
				seen.add(s.key())
				shadows = append(shadows, s)
			}
		}
	}

	sortTagShadows(shadows)
	return shadows
}

// sortTagShadows orders shadows by group then key for stable, grouped output.
func sortTagShadows(shadows []tagShadow) {
	sort.Slice(shadows, func(i, j int) bool {
		if shadows[i].group != shadows[j].group {
			return shadows[i].group < shadows[j].group
		}
		return shadows[i].key() < shadows[j].key()
	})
}

// detail renders the human-readable half of an entry: what the winning field
// declares, what it hides, and the embed path when it is deeper than one hop.
func (s tagShadow) detail() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s shadows %s.%s %s", s.Kind, s.OuterGoType, s.Declaring, s.JSONName, s.ShadowedGoType)
	if len(s.Chain) > 1 {
		fmt.Fprintf(&b, " (via %s)", strings.Join(s.Chain[:len(s.Chain)-1], " -> "))
	}
	return b.String()
}

// parseTagShadowAllowlist parses allowlist bytes into a set of keys. Lines are
// trimmed, '#' comments (whole-line or trailing) are stripped, and blank lines
// are ignored. The key is the first whitespace-delimited token on each line.
func parseTagShadowAllowlist(data []byte) set[string] {
	allowed := make(set[string])
	for line := range strings.SplitSeq(string(data), "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		allowed.add(strings.Fields(line)[0])
	}
	return allowed
}

// loadTagShadowAllowlist reads an allowlist file and parses it. Used only for an
// explicit AllowlistPath override; the default check parses
// embeddedTagShadowAllowlist and cannot fail.
func loadTagShadowAllowlist(path string) (set[string], error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("duplicate-JSON-tag allowlist %q not found; run with -update-tagshadow-allowlist to create it: %w", path, err)
		}
		return nil, fmt.Errorf("reading allowlist %q: %w", path, err)
	}
	return parseTagShadowAllowlist(data), nil
}

// writeTagShadowAllowlist rewrites the allowlist file from shadows, grouped by
// group with sorted keys for minimal diffs. shadows is assumed pre-sorted by
// sortTagShadows (collectTagShadows guarantees this).
func writeTagShadowAllowlist(path string, shadows []tagShadow) (bool, error) {
	var b strings.Builder
	b.WriteString("# osgen duplicate-JSON-tag allowlist - DO NOT EDIT BY HAND.\n")
	b.WriteString("# Regenerate by re-running `cmd/osgen` with `-update-tagshadow-allowlist`.\n")
	b.WriteString("#\n")
	b.WriteString("# Each line is a permitted tag shadow, keyed\n")
	b.WriteString("# \"OuterGoType/jsonTag/DeclaringGoType\": OuterGoType declares jsonTag, and so\n")
	b.WriteString("# does DeclaringGoType, which OuterGoType reaches through an embed. encoding/json\n")
	b.WriteString("# resolves the duplicate in favor of the shallower field, so OuterGoType's\n")
	b.WriteString("# declaration wins and DeclaringGoType's is never populated.\n")
	b.WriteString("# The trailing comment is informational and ignored on load.\n")
	b.WriteString("#\n")
	b.WriteString("# A narrowing entry is the deliberate case: the winning field names a more\n")
	b.WriteString("# specific type than the one it hides. An entry that erases a typed payload\n")
	b.WriteString("# makes every field of the hidden type unreachable; confirm that is intended\n")
	b.WriteString("# before adding one.\n")

	var group string
	first := true
	for _, s := range shadows {
		if first || s.group != group {
			group = s.group
			first = false
			label := group
			if label == "" {
				label = "(ungrouped)"
			}
			fmt.Fprintf(&b, "\n# --- %s ---\n", label)
		}
		fmt.Fprintf(&b, "%s # %s\n", s.key(), s.detail())
	}

	return writeIfChanged(path, []byte(b.String()))
}

// guardTagShadows enforces the duplicate-JSON-tag allowlist against the IR.
//
// With cfg.Update it rewrites the allowlist from the current output and returns
// nil (generation continues, refreshing both code and allowlist in one pass).
// Otherwise it checks against the embedded allowlist - or the file named by
// cfg.AllowlistPath, when set - reports any unlisted shadows to w, and returns a
// non-nil error (aborting generation) unless cfg.AllowUnlisted is set, in which
// case the offenders are a warning only. Stale entries (listed but no longer
// emitted) are always a non-fatal warning, since they permit nothing and failing
// on them would break unrelated spec edits.
func guardTagShadows(w io.Writer, spec *ir.Spec, cfg TagShadowConfig) error {
	shadows := collectTagShadows(spec)

	if cfg.Update {
		path := cfg.AllowlistPath
		if path == "" {
			path = tagShadowAllowlistFile
		}
		changed, err := writeTagShadowAllowlist(path, shadows)
		if err != nil {
			return err
		}
		if changed {
			fmt.Fprintf(w, "osgen: wrote duplicate-JSON-tag allowlist %q (%d entries)\n", path, len(shadows))
		}
		return nil
	}

	var allowed set[string]
	if cfg.AllowlistPath == "" {
		allowed = parseTagShadowAllowlist(embeddedTagShadowAllowlist)
	} else {
		loaded, err := loadTagShadowAllowlist(cfg.AllowlistPath)
		switch {
		// Under AllowUnlisted the check is advisory, so a missing file is not
		// fatal: treat the allowlist as empty and let every shadow fall through to
		// the warning path below.
		case cfg.AllowUnlisted && errors.Is(err, fs.ErrNotExist):
			allowed = set[string]{}
		case err != nil:
			return err
		default:
			allowed = loaded
		}
	}

	var offenders []tagShadow
	for _, s := range shadows {
		if !allowed.has(s.key()) {
			offenders = append(offenders, s)
		}
	}

	found := make(set[string], len(shadows))
	for _, s := range shadows {
		found.add(s.key())
	}
	var stale []string
	for k := range allowed {
		if !found.has(k) {
			stale = append(stale, k)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		fmt.Fprintf(w, "NOTE: %d duplicate-JSON-tag allowlist entr%s no longer present in output; run -update-tagshadow-allowlist to prune:\n",
			len(stale), plural(len(stale), "y is", "ies are"))
		for _, k := range stale {
			fmt.Fprintf(w, "  - %s\n", k)
		}
	}

	if len(offenders) == 0 {
		return nil
	}

	fmt.Fprintf(w, "WARNING: osgen emitted %d duplicate JSON tag(s) across an embed boundary, not in the allowlist %s.\n",
		len(offenders), cfg.allowlistSource())
	fmt.Fprintln(w, "In each case the outer struct's field wins and the embedded declaration is never populated.")
	for _, s := range offenders {
		fmt.Fprintf(w, "  - %s (%s)\n", s.key(), s.detail())
	}
	fmt.Fprintln(w, "Investigate the shadow. If it is intended, add the key(s) via -update-tagshadow-allowlist.")

	if cfg.AllowUnlisted {
		fmt.Fprintf(w, "osgen: continuing despite %d unlisted duplicate JSON tag(s) (-allow-unlisted-tagshadow)\n", len(offenders))
		return nil
	}
	return fmt.Errorf(
		"%d unlisted duplicate JSON tag(s) against %s; add them with -update-tagshadow-allowlist "+
			"or pass -allow-unlisted-tagshadow",
		len(offenders), cfg.allowlistSource())
}
