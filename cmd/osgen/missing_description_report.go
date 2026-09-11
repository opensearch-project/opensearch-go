// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/opensearch-project/opensearch-go/cmd/osgen/v5/ir"
)

// A generated identifier with no doc comment is almost always an upstream spec
// gap: the OpenAPI schema, property, or oneOf branch it came from has no
// `description`. The report below lists those identifiers so they can be filed
// against opensearch-api-specification. It is a contributor aid, never a build
// gate: it runs only under -report-missing-descriptions, writes to stderr, and
// does not affect the emitted code. See [reportMissingDescriptions].

// DescriptionReportConfig controls the missing-description report.
type DescriptionReportConfig struct {
	// Report enables the report after generation. Default off, since the gaps are
	// upstream and a warning on every run would be noise.
	Report bool
}

// missingDescKind classifies which sort of identifier is missing a description.
// The three kinds map to the three report sections.
type missingDescKind int

const (
	missingDescType       missingDescKind = iota // a generated Go type
	missingDescField                             // a struct field on a generated type
	missingDescEnumMember                        // a const in a string-backed enum
)

// missingDesc is one generated identifier whose spec source had no description.
type missingDesc struct {
	Kind missingDescKind
	// TypeName is the owning (or, for missingDescType, the subject) Go type.
	TypeName string
	// Ident is the field's Go name or the enum const name. Empty for a type.
	Ident string
	// Wire is the JSON tag for a field, or the wire value for an enum member.
	// Upstream contributors search the spec by wire name, not by Go name.
	Wire string
	// SchemaRef is the spec component key the type came from, e.g.
	// "_core.search___ProcessorExecutionDetail". Empty for synthesized types
	// (a Resp struct for an operation with no component schema).
	SchemaRef string
}

// name returns the qualified identifier as it appears in the report.
func (m missingDesc) name() string {
	if m.Ident == "" {
		return m.TypeName
	}
	return m.TypeName + "." + m.Ident
}

// collectMissingDescriptions walks the IR and returns every generated identifier
// whose description did not survive into the IR, deduplicated by owning type.
//
// It considers only what the emitter actually renders: each operation's Resp,
// typed request body, and sibling types, plus the shared types written to
// types_gen.go / unions_gen.go / enums_gen.go. Registry entries that no
// operation claims (request-body and aggregation schemas that never become a Go
// type) are deliberately skipped - an upstream contributor cannot act on a
// description gap in a schema this client does not surface.
func collectMissingDescriptions(spec *ir.Spec) []missingDesc {
	var out []missingDesc
	seen := make(set[string])

	// add records the gaps on one type. desc is the description that renders as
	// the type's doc comment, which is not always t.Comment (see the Resp case
	// below). Types are deduplicated by Go name: sibling types are converted
	// copies of registry entries, so the same type reaches this walk more than
	// once.
	add := func(t *ir.Type, desc string) {
		if t == nil || t.Name == "" || seen.has(t.Name) {
			return
		}
		seen.add(t.Name)

		if strings.TrimSpace(desc) == "" {
			out = append(out, missingDesc{Kind: missingDescType, TypeName: t.Name, SchemaRef: t.SchemaRef})
		}

		for _, f := range t.Fields {
			// An embedded type carries no JSON property of its own, and a field
			// without an exported Go name is not rendered.
			if f.IsEmbed || f.GoName == "" {
				continue
			}
			if strings.TrimSpace(f.Comment) != "" {
				continue
			}
			out = append(out, missingDesc{
				Kind:      missingDescField,
				TypeName:  t.Name,
				Ident:     f.GoName,
				Wire:      f.JSONName,
				SchemaRef: t.SchemaRef,
			})
		}

		// Only string-backed enums render a per-member doc comment; the
		// int-backed path carries wire values alone (see convertEnumMembers), so
		// every one of its members would report as missing and tell a
		// contributor nothing.
		if t.Kind == ir.TypeStringEnum {
			for _, m := range t.EnumMembers {
				if strings.TrimSpace(m.Comment) != "" {
					continue
				}
				out = append(out, missingDesc{
					Kind:      missingDescEnumMember,
					TypeName:  t.Name,
					Ident:     m.ConstName,
					Wire:      m.Value,
					SchemaRef: t.SchemaRef,
				})
			}
		}
	}

	addSchemaType := func(t *ir.Type) {
		if t != nil {
			add(t, t.Comment)
		}
	}

	for _, op := range spec.Operations {
		// A Resp struct's doc comment is rendered from the operation description,
		// not from a schema description, so that is the text a reader finds
		// missing and the spec field an upstream contributor would fill in.
		if op.Response != nil {
			add(op.Response, op.Description)
		}
		addSchemaType(op.RequestBody)
		for _, st := range op.SiblingTypes {
			addSchemaType(st)
		}
		for _, st := range op.ReqBodySiblings {
			addSchemaType(st)
		}
	}

	// Shared types are the only registry entries emitted on their own; the rest
	// reach output through the per-operation lists above.
	for _, t := range spec.Types {
		if t.Scope != ir.ScopeShared {
			continue
		}
		addSchemaType(t)
	}

	sortMissingDescs(out)
	return out
}

// sortMissingDescs orders entries by kind, then owning type, then identifier, so
// the report is byte-stable across runs. The OpenSearch spec is parsed into Go
// maps, so without this the walk order varies from run to run.
func sortMissingDescs(entries []missingDesc) {
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.TypeName != b.TypeName {
			return a.TypeName < b.TypeName
		}
		if a.Ident != b.Ident {
			return a.Ident < b.Ident
		}
		return a.Wire < b.Wire
	})
}

// sectionTitle names the report section for a kind.
func (k missingDescKind) sectionTitle() string {
	switch k {
	case missingDescType:
		return "types with no description"
	case missingDescField:
		return "struct fields with no description"
	case missingDescEnumMember:
		return "enum members with no description"
	default:
		return "identifiers with no description"
	}
}

// countLabel names the kind in the summary line, in singular form.
func (k missingDescKind) countLabel() string {
	switch k {
	case missingDescType:
		return "type"
	case missingDescField:
		return "field"
	case missingDescEnumMember:
		return "enum member"
	default:
		return "identifier"
	}
}

// reportMissingDescriptions writes the missing-description report to w when
// cfg.Report is set, and otherwise does nothing. It never returns an error and
// never influences generation: the gaps it names live in the upstream spec, so
// failing here would block this client on someone else's repository.
func reportMissingDescriptions(w io.Writer, spec *ir.Spec, cfg DescriptionReportConfig) {
	if !cfg.Report {
		return
	}

	entries := collectMissingDescriptions(spec)

	fmt.Fprintln(w, "osgen: missing OpenAPI descriptions (-report-missing-descriptions)")
	if len(entries) == 0 {
		fmt.Fprintln(w, "  every generated type, field, and enum member has a description.")
		return
	}
	fmt.Fprintln(w, "Each entry is a generated identifier whose spec source has no `description`.")
	fmt.Fprintln(w, "These are upstream gaps; file them against opensearch-api-specification.")

	kinds := []missingDescKind{missingDescType, missingDescField, missingDescEnumMember}
	counts := make(map[missingDescKind]int, len(kinds))
	for _, e := range entries {
		counts[e.Kind]++
	}

	for _, kind := range kinds {
		if counts[kind] == 0 {
			continue
		}
		fmt.Fprintf(w, "\n--- %s (%d) ---\n", kind.sectionTitle(), counts[kind])
		for _, e := range entries {
			if e.Kind != kind {
				continue
			}
			fmt.Fprintf(w, "  - %s%s%s\n", e.name(), wireSuffix(e), schemaRefSuffix(e))
		}
	}

	summary := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		n := counts[kind]
		summary = append(summary, fmt.Sprintf("%d %s", n, plural(n, kind.countLabel(), kind.countLabel()+"s")))
	}
	fmt.Fprintf(w, "\nSUMMARY: %s; %d total\n", strings.Join(summary, ", "), len(entries))
}

// wireSuffix renders the wire-name column: the JSON tag for a field, the wire
// value for an enum member. Types have no wire name of their own.
func wireSuffix(e missingDesc) string {
	switch e.Kind {
	case missingDescField:
		return fmt.Sprintf(" json:%q", e.Wire)
	case missingDescEnumMember:
		return fmt.Sprintf(" value:%q", e.Wire)
	case missingDescType:
		return ""
	default:
		return ""
	}
}

// schemaRefSuffix renders the spec component key, which is where an upstream
// contributor adds the description.
func schemaRefSuffix(e missingDesc) string {
	if e.SchemaRef == "" {
		return ""
	}
	return " [" + e.SchemaRef + "]"
}
