// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	_ "embed"
	"fmt"
	"io"
	"strings"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

// A json.RawMessage in generated output is the symptom of a type the generator
// could not resolve. Most are legitimate (freeform JSON like _source), but a
// generator bug can silently widen the raw-JSON surface of the public API by
// spawning many at once. The guard pins the permitted set in a checked-in
// allowlist and fails generation when an unlisted use appears, so a regression
// is caught at gen time rather than shipped. See [guardRawMessages], and
// [allowlistEntry] for the file format it shares with the duplicate-JSON-tag
// guard.

// rawMessageAllowlistFile is the checked-in allowlist's filename: the default
// -update write target, and the name reported in messages. The //go:embed
// directive below repeats the literal because a directive cannot reference a
// const.
const rawMessageAllowlistFile = "rawmessage_allowlist.txt"

// The noun this guard reports itself by, and the flags that update or bypass it.
const (
	rawMessageNoun       = "json.RawMessage"
	rawMessageUpdateFlag = "-update-raw-message-allowlist"
	rawMessageAllowFlag  = "-allow-unlisted-raw-message"
)

// embeddedRawMessageAllowlist is the checked-in allowlist compiled into the
// binary, so the check enforces the same set regardless of the process working
// directory. Disk reads happen only for an explicit override (see
// AllowlistConfig.AllowlistPath).
//
//go:embed rawmessage_allowlist.txt
var embeddedRawMessageAllowlist []byte

// rawMessageAllowlistHeader is the comment block [writeAllowlistFile] writes
// above the entries.
const rawMessageAllowlistHeader = "# osgen json.RawMessage allowlist - DO NOT EDIT BY HAND.\n" +
	"# Regenerate by re-running `cmd/osgen` with `-update-raw-message-allowlist`.\n" +
	"#\n" +
	"# Each line is a permitted json.RawMessage use, keyed \"GoTypeName/jsonFieldName\".\n" +
	"# Whole-response raw bodies use \"<Prefix>Resp/-\"; map/array responses whose\n" +
	"# element type is unresolved use \"<Prefix>Resp/[entries]\" and \"<Prefix>Resp/[records]\".\n" +
	"# The trailing \"# form\" comment is informational and ignored on load.\n" +
	"#\n" +
	"# A new entry here means the generator emitted a raw json.RawMessage where a\n" +
	"# typed struct was expected. Confirm the degradation is intended before adding.\n"

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

// rawUse is one occurrence of json.RawMessage in generated output. It satisfies
// [allowlistEntry].
type rawUse struct {
	GoType   string  // owning Go type name (e.g. "SearchHit", "TasksTaskListRespBase")
	JSONName string  // JSON field name; sentinels for whole-response shapes (see collectRawMessageUses)
	Form     rawForm // bare/slice/map spelling
	Kind     rawUseKind
	group    string // schema/operation group, for grouped output only (not part of the key)
}

// key is the allowlist line key: "GoTypeName/jsonFieldName".
func (u rawUse) key() string { return u.GoType + "/" + u.JSONName }

// groupName is the schema/operation group the entry is banner-grouped under.
func (u rawUse) groupName() string { return u.group }

// comment records the raw spelling beside the key. Informational: the form does
// not affect whether the use is permitted.
func (u rawUse) comment() string { return u.Form.String() }

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
		case leaf == goTypeRawMessage:
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
// deduplicated by key and sorted for grouped output. It mirrors exactly what the
// emitter renders: struct fields across response, sibling, and request-body
// types, plus the whole-response map/array/raw shapes that are synthesized in
// convertOperation and never registered in spec.Types.
func collectRawMessageUses(spec *ir.Spec) []rawUse {
	seen := make(set[string])
	var uses []rawUse

	add := func(u rawUse) {
		if seen.has(u.key()) {
			return
		}
		seen.add(u.key())
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

	sortAllowlistEntries(uses)
	return uses
}

// guardRawMessages enforces the json.RawMessage allowlist against the IR.
//
// With cfg.Update it rewrites the allowlist from the current output and returns
// nil (generation continues, refreshing both code and allowlist in one pass).
// Otherwise it checks against the embedded allowlist - or the file named by
// cfg.AllowlistPath, when set - reports any unlisted uses to w, and returns a
// non-nil error (aborting generation) unless cfg.AllowUnlisted is set, in which
// case the offenders are a warning only. Stale entries (listed but no longer
// emitted) are always a non-fatal warning; see [reportStaleAllowlist].
func guardRawMessages(w io.Writer, spec *ir.Spec, cfg AllowlistConfig) error {
	uses := collectRawMessageUses(spec)

	if cfg.Update {
		path := cfg.AllowlistPath
		if path == "" {
			path = rawMessageAllowlistFile
		}
		changed, err := writeAllowlistFile(path, rawMessageAllowlistHeader, uses)
		if err != nil {
			return err
		}
		if changed {
			fmt.Fprintf(w, "osgen: wrote %s allowlist %q (%d entries)\n", rawMessageNoun, path, len(uses))
		}
		return nil
	}

	allowed, err := resolveAllowlist(cfg, embeddedRawMessageAllowlist, rawMessageNoun, rawMessageUpdateFlag)
	if err != nil {
		return err
	}

	reportStaleAllowlist(w, allowed, uses, rawMessageNoun, rawMessageUpdateFlag)

	offenders := unlistedEntries(uses, allowed)
	if len(offenders) == 0 {
		return nil
	}

	source := cfg.allowlistSource(rawMessageAllowlistFile)
	fmt.Fprintf(w, "WARNING: osgen emitted %d json.RawMessage use(s) not in the allowlist %s.\n", len(offenders), source)
	fmt.Fprintln(w, "Each is a response/request field that degraded to raw JSON instead of a typed struct.")
	for _, u := range offenders {
		fmt.Fprintf(w, "  - %s (%s, %s)\n", u.key(), u.Form, u.kindLabel())
	}
	fmt.Fprintf(w, "Investigate the degradation. If it is intended, add the key(s) via %s.\n", rawMessageUpdateFlag)

	if cfg.AllowUnlisted {
		fmt.Fprintf(w, "osgen: continuing despite %d unlisted json.RawMessage use(s) (%s)\n", len(offenders), rawMessageAllowFlag)
		return nil
	}
	return fmt.Errorf(
		"%d unlisted json.RawMessage use(s) against %s; add them with %s or pass %s",
		len(offenders), source, rawMessageUpdateFlag, rawMessageAllowFlag)
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
