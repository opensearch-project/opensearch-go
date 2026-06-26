// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

// A json.RawMessage in generated output is the symptom of a type the generator
// could not resolve. Most are legitimate (freeform JSON like _source), but a
// generator bug can silently widen the raw-JSON surface of the public API by
// spawning many at once. The guard pins the permitted set in a checked-in
// allowlist and fails generation when an unlisted use appears, so a regression
// is caught at gen time rather than shipped. See [guardRawMessages].

// rawForm classifies the three json.RawMessage spellings the generator emits.
type rawForm int

const (
	rawBare  rawForm = iota // json.RawMessage
	rawSlice                // []json.RawMessage
	rawMap                  // map[string]json.RawMessage
)

// Allowlist form labels written as the trailing "# <label>" comment on each
// entry. Informational only (ignored on load), but kept as consts so the
// rawForm.String mapping has a single source of truth.
const (
	rawFormLabelBare  = "bare"
	rawFormLabelSlice = "slice"
	rawFormLabelMap   = "map"
)

func (f rawForm) String() string {
	switch f {
	case rawBare:
		return rawFormLabelBare
	case rawSlice:
		return rawFormLabelSlice
	case rawMap:
		return rawFormLabelMap
	default:
		return rawFormLabelBare
	}
}

// rawUseKind records which generation source produced a raw use, for clearer
// offender messages. It is not part of the allowlist key.
type rawUseKind int

const (
	rawKindField    rawUseKind = iota // a struct field on a generated type
	rawKindResponse                   // a whole-response raw body (RespShapeRaw)
	rawKindRespElem                   // a map/array response whose element defaulted to raw
)

// rawUse is one occurrence of json.RawMessage in generated output.
type rawUse struct {
	GoType   string  // owning Go type name (e.g. "SearchHit", "TasksTaskListRespBase")
	JSONName string  // JSON field name; sentinels for whole-response shapes (see collectRawMessageUses)
	Form     rawForm // bare/slice/map spelling
	Kind     rawUseKind
	group    string // schema/operation group, for grouped output only (not part of the key)
}

// key is the allowlist line key: "GoTypeName/jsonFieldName".
func (u rawUse) key() string { return u.GoType + "/" + u.JSONName }

// RawMessageConfig controls the json.RawMessage guard.
type RawMessageConfig struct {
	// AllowlistPath is the checked-in allowlist file (relative to cwd).
	AllowlistPath string
	// Update rewrites AllowlistPath from the current output instead of checking.
	Update bool
	// AllowUnlisted downgrades the fatal check to a warning.
	AllowUnlisted bool
}

// classifyRawForm reports the rawForm of a Go type expression, and false if the
// type does not have a json.RawMessage leaf. A raw leaf can sit under any depth
// of wrappers (e.g. [][]json.RawMessage for SQL/PPL Datarows); matching only the
// top-level spellings would let nested raw escape the guard, so this peels every
// wrapper to the leaf. The reported form is the OUTERMOST wrapper (slice/map/
// bare), which is what the allowlist's informational "# form" comment records. A
// bare field is never a pointer in practice (walkProperties forces it), but a
// leading *json.RawMessage is accepted defensively.
func classifyRawForm(goType string) (rawForm, bool) {
	// Outermost wrapper determines the reported form; a leading pointer does not.
	form := rawBare
	switch {
	case strings.HasPrefix(strings.TrimPrefix(goType, "*"), "[]"):
		form = rawSlice
	case strings.HasPrefix(strings.TrimPrefix(goType, "*"), "map[string]"):
		form = rawMap
	}

	// Peel pointer/slice/map wrappers until the leaf type remains.
	leaf := goType
	for {
		switch {
		case strings.HasPrefix(leaf, "*"):
			leaf = leaf[len("*"):]
		case strings.HasPrefix(leaf, "[]"):
			leaf = leaf[len("[]"):]
		case strings.HasPrefix(leaf, "map[string]"):
			leaf = leaf[len("map[string]"):]
		case leaf == "json.RawMessage":
			return form, true
		default:
			return 0, false
		}
	}
}

// Sentinel JSON names for whole-response raw shapes, which have no real field
// JSON tag (they render with `json:"-"`). Brackets cannot occur in a real JSON
// tag, so these never collide with a field-level key.
const (
	rawRespBodySentinel    = "-"
	rawRespEntriesSentinel = "[entries]"
	rawRespRecordsSentinel = "[records]"
)

// collectRawMessageUses walks the IR and returns every json.RawMessage use,
// deduplicated by key. It mirrors exactly what the emitter renders: struct
// fields across response, sibling, and request-body types, plus the
// whole-response map/array/raw shapes that are synthesized in convertOperation
// and never registered in spec.Types.
func collectRawMessageUses(spec *ir.Spec) []rawUse {
	seen := make(map[string]struct{})
	var uses []rawUse

	add := func(u rawUse) {
		if _, ok := seen[u.key()]; ok {
			return
		}
		seen[u.key()] = struct{}{}
		uses = append(uses, u)
	}

	addFields := func(t *ir.Type, group string) {
		if t == nil {
			return
		}
		for _, f := range t.Fields {
			// Embedded types carry no JSON field of their own; skip them and
			// any field without an exported Go name (not wire-facing).
			if f.IsEmbed || f.GoName == "" {
				continue
			}
			if form, ok := classifyRawForm(f.GoType); ok {
				add(rawUse{
					GoType:   t.Name,
					JSONName: f.JSONName,
					Form:     form,
					Kind:     rawKindField,
					group:    group,
				})
			}
		}
	}

	for _, op := range spec.Operations {
		addFields(op.Response, op.Group)
		addFields(op.RequestBody, op.Group)
		for _, st := range op.SiblingTypes {
			addFields(st, op.Group)
		}
		for _, st := range op.ReqBodySiblings {
			addFields(st, op.Group)
		}

		// Whole-response shapes that render `json:"-"` raw bodies. The map/array
		// shapes only degrade to raw when the element type is unresolved (nil).
		respName := op.TypePrefix + "Resp"
		switch op.RespShape {
		case ir.RespShapeRaw:
			add(rawUse{GoType: respName, JSONName: rawRespBodySentinel, Form: rawBare, Kind: rawKindResponse, group: op.Group})
		case ir.RespShapeMap:
			if op.RespElemType == nil {
				add(rawUse{GoType: respName, JSONName: rawRespEntriesSentinel, Form: rawMap, Kind: rawKindRespElem, group: op.Group})
			}
		case ir.RespShapeArray:
			if op.RespElemType == nil {
				add(rawUse{GoType: respName, JSONName: rawRespRecordsSentinel, Form: rawSlice, Kind: rawKindRespElem, group: op.Group})
			}
		case ir.RespShapeStruct:
			// Struct fields are handled by addFields(op.Response) above.
		}
	}

	for _, t := range spec.Types {
		addFields(t, schemaGroup(t.SchemaRef))
	}

	sortRawUses(uses)
	return uses
}

// sortRawUses orders uses by group then key for stable, grouped output.
func sortRawUses(uses []rawUse) {
	sort.Slice(uses, func(i, j int) bool {
		if uses[i].group != uses[j].group {
			return uses[i].group < uses[j].group
		}
		return uses[i].key() < uses[j].key()
	})
}

// loadRawMessageAllowlist reads the allowlist file into a set of keys. Lines are
// trimmed, '#' comments (whole-line or trailing) are stripped, and blank lines
// are ignored. The key is the first whitespace-delimited token on each line.
func loadRawMessageAllowlist(path string) (map[string]struct{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("json.RawMessage allowlist %q not found; run with -update-raw-message-allowlist to create it: %w", path, err)
		}
		return nil, fmt.Errorf("reading allowlist %q: %w", path, err)
	}

	allowed := make(map[string]struct{})
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key := strings.Fields(line)[0]
		allowed[key] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading allowlist %q: %w", path, err)
	}
	return allowed, nil
}

// writeRawMessageAllowlist rewrites the allowlist file from uses, grouped by
// group with sorted keys for minimal diffs. uses is assumed pre-sorted by
// sortRawUses (collectRawMessageUses guarantees this).
func writeRawMessageAllowlist(path string, uses []rawUse) (bool, error) {
	var b strings.Builder
	b.WriteString("# osgen json.RawMessage allowlist - DO NOT EDIT BY HAND.\n")
	b.WriteString("# Regenerate with: make gen-api ARGS=-update-raw-message-allowlist\n")
	b.WriteString("#\n")
	b.WriteString("# Each line is a permitted json.RawMessage use, keyed \"GoTypeName/jsonFieldName\".\n")
	b.WriteString("# Whole-response raw bodies use \"<Prefix>Resp/-\"; map/array responses whose\n")
	b.WriteString("# element type is unresolved use \"<Prefix>Resp/[entries]\" and \"<Prefix>Resp/[records]\".\n")
	b.WriteString("# The trailing \"# form\" comment is informational and ignored on load.\n")
	b.WriteString("#\n")
	b.WriteString("# A new entry here means the generator emitted a raw json.RawMessage where a\n")
	b.WriteString("# typed struct was expected. Confirm the degradation is intended before adding.\n")

	var group string
	first := true
	for _, u := range uses {
		if first || u.group != group {
			group = u.group
			first = false
			label := group
			if label == "" {
				label = "(ungrouped)"
			}
			fmt.Fprintf(&b, "\n# --- %s ---\n", label)
		}
		fmt.Fprintf(&b, "%s # %s\n", u.key(), u.Form)
	}

	return writeIfChanged(path, []byte(b.String()))
}

// guardRawMessages enforces the json.RawMessage allowlist against the IR.
//
// With cfg.Update it rewrites the allowlist from the current output and returns
// nil (generation continues, refreshing both code and allowlist in one pass).
// Otherwise it loads the allowlist, reports any unlisted uses to w, and returns
// a non-nil error (aborting generation) unless cfg.AllowUnlisted is set, in
// which case the offenders are a warning only. Stale entries (listed but no
// longer emitted) are always a non-fatal warning, since they permit nothing and
// failing on them would break unrelated spec edits.
func guardRawMessages(w io.Writer, spec *ir.Spec, cfg RawMessageConfig) error {
	uses := collectRawMessageUses(spec)

	if cfg.Update {
		changed, err := writeRawMessageAllowlist(cfg.AllowlistPath, uses)
		if err != nil {
			return err
		}
		if changed {
			fmt.Fprintf(w, "osgen: wrote json.RawMessage allowlist %q (%d entries)\n", cfg.AllowlistPath, len(uses))
		}
		return nil
	}

	allowed, err := loadRawMessageAllowlist(cfg.AllowlistPath)
	if err != nil {
		// Under AllowUnlisted the check is advisory, so a missing file is not
		// fatal: treat the allowlist as empty and let every use fall through to
		// the warning path below.
		if cfg.AllowUnlisted && errors.Is(err, fs.ErrNotExist) {
			allowed = map[string]struct{}{}
		} else {
			return err
		}
	}

	var offenders []rawUse
	for _, u := range uses {
		if _, ok := allowed[u.key()]; !ok {
			offenders = append(offenders, u)
		}
	}

	used := make(map[string]struct{}, len(uses))
	for _, u := range uses {
		used[u.key()] = struct{}{}
	}
	var stale []string
	for k := range allowed {
		if _, ok := used[k]; !ok {
			stale = append(stale, k)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		fmt.Fprintf(w, "NOTE: %d json.RawMessage allowlist entr%s no longer present in output; run -update-raw-message-allowlist to prune:\n",
			len(stale), plural(len(stale), "y is", "ies are"))
		for _, k := range stale {
			fmt.Fprintf(w, "  - %s\n", k)
		}
	}

	if len(offenders) == 0 {
		return nil
	}

	fmt.Fprintf(w, "WARNING: osgen emitted %d json.RawMessage use(s) not in the allowlist %q.\n", len(offenders), cfg.AllowlistPath)
	fmt.Fprintln(w, "Each is a response/request field that degraded to raw JSON instead of a typed struct.")
	for _, u := range offenders {
		fmt.Fprintf(w, "  - %s (%s, %s)\n", u.key(), u.Form, u.kindLabel())
	}
	fmt.Fprintln(w, "Investigate the degradation. If it is intended, add the key(s) via -update-raw-message-allowlist.")

	if cfg.AllowUnlisted {
		fmt.Fprintf(w, "osgen: continuing despite %d unlisted json.RawMessage use(s) (-allow-unlisted-raw-message)\n", len(offenders))
		return nil
	}
	return fmt.Errorf("%d unlisted json.RawMessage use(s); add them with -update-raw-message-allowlist or pass -allow-unlisted-raw-message",
		len(offenders))
}

// kindLabel returns a human-readable description of the raw use's source.
func (u rawUse) kindLabel() string {
	switch u.Kind {
	case rawKindField:
		return "struct field"
	case rawKindResponse:
		return "whole-response raw body"
	case rawKindRespElem:
		return "unresolved response element"
	default:
		return "struct field"
	}
}

// plural picks the singular or plural form based on n.
func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return singular
	}
	return pluralForm
}
