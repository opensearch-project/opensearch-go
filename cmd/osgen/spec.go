// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"encoding/json"
	"log"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/opensearch-project/opensearch-go/cmd/osgen/v5/errwrap"
	"github.com/opensearch-project/opensearch-go/cmd/osgen/v5/ir"
)

// OpenAPI spec extension keys used by the OpenSearch spec to annotate operations
// and parameters beyond what standard OpenAPI provides.
const (
	// extOperationGroup identifies the logical operation group for an endpoint
	// (e.g. "indices.create", "cluster.health"). Multiple HTTP paths may share
	// the same group and are combined into a single generated API type.
	extOperationGroup = "x-operation-group"

	// extDeprecationMessage provides human-readable deprecation guidance.
	extDeprecationMessage = "x-deprecation-message"

	// extIgnorable marks operations that should be skipped during generation
	// (typically internal or unsupported endpoints).
	extIgnorable = "x-ignorable"

	// extVersionAdded records the OpenSearch release that introduced the operation.
	extVersionAdded = "x-version-added"

	// extVersionDeprecated records the OpenSearch release that deprecated the operation.
	extVersionDeprecated = "x-version-deprecated"

	// extVersionRemoved records the OpenSearch release that removed the operation.
	extVersionRemoved = "x-version-removed"

	// extDistributionsExcluded lists distributions (e.g. "amazon-managed") where
	// the operation is unavailable.
	extDistributionsExcluded = "x-distributions-excluded"

	// extGenericTypeParam marks a schema as a generic type parameter placeholder
	// (e.g. _core.search___T). These have no concrete type and should be treated
	// as json.RawMessage in generated code.
	extGenericTypeParam = "x-is-generic-type-parameter"

	// extEnumName opts a string schema into typed-enum generation and names the
	// generated Go type. When present alongside a non-empty enum: constraint, the
	// walker emits an int-backed iota enum type (type <name> int + a const block
	// of the allowed values) instead of a plain string. Used to type fields whose
	// wire value is a closed set of names (e.g. security status -> RestStatus).
	extEnumName = "x-enum-name"

	// extTypeName opts a string schema without an enum into an opaque Go token
	// type and names it, so every field referencing the schema shares one type
	// that tools can follow (e.g. PITID for PIT IDs). The type wraps an
	// unexported string, is built with Parse<Name>, and encodes as that string.
	extTypeName = "x-type-name"

	// extErrorResponses lists wrapper-schema $refs for partial-failure
	// shapes an operation may surface alongside its primary 2xx response
	// (per the proposed x-error-responses OpenAPI extension). Each entry
	// is an object {$ref: "#/components/schemas/_common.errors___WrapperName"};
	// the codegen extracts the terminal segment after the last underscore-
	// triple as the wrapper name (e.g. "BulkItems") and feeds it into the
	// emit phase.
	extErrorResponses = "x-error-responses"

	// extErrorTypes lists wrapper-schema $refs for non-2xx errors an
	// operation may return that callers should be able to tell apart (per
	// the proposed x-error-types OpenAPI extension). Each entry has the
	// x-error-responses shape; the referenced wrapper schema carries
	// extErrorStatus and extErrorRootCauseType, which say how to recognize
	// the error.
	extErrorTypes = "x-error-types"

	// extErrorStatus is the HTTP status of an x-error-types wrapper's error.
	extErrorStatus = "x-error-status"

	// extErrorRootCauseType is the error.root_cause[].type an x-error-types
	// wrapper's error carries.
	extErrorRootCauseType = "x-error-root-cause-type"
)

// operationGroup reads the logical group name from an operation's extensions.
// Operations sharing a group are combined into a single generated API type.
func operationGroup(op *openapi3.Operation) string {
	if op == nil || op.Extensions == nil {
		return ""
	}
	raw, ok := op.Extensions[extOperationGroup]
	if !ok {
		return ""
	}
	switch v := raw.(type) {
	case json.RawMessage:
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return ""
		}
		return s
	case string:
		return v
	default:
		return ""
	}
}

// deprecationMessage reads the human-readable deprecation notice from an operation.
func deprecationMessage(op *openapi3.Operation) string {
	if op == nil || op.Extensions == nil {
		return ""
	}
	raw, ok := op.Extensions[extDeprecationMessage]
	if !ok {
		return ""
	}
	switch v := raw.(type) {
	case json.RawMessage:
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return ""
		}
		return s
	case string:
		return v
	default:
		return ""
	}
}

// extensionString reads a string-valued extension from a map.
func extensionString(extensions map[string]any, key string) string {
	if extensions == nil {
		return ""
	}
	raw, ok := extensions[key]
	if !ok {
		return ""
	}
	switch v := raw.(type) {
	case json.RawMessage:
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return ""
		}
		return s
	case string:
		return v
	default:
		return ""
	}
}

// refExtensionString reads a string-valued extension from a schema reference,
// preferring an extension written alongside a $ref over one on the referenced
// schema.
//
// kin-openapi splits a $ref's siblings across two places. Standard fields such
// as description are overlaid onto the resolved schema, so Value.Description
// sees them, but x-* keys stay on the SchemaRef and never reach Value.Extensions.
// Reading only the resolved schema therefore drops every extension the spec
// attaches next to a $ref, which is where the OpenSearch spec records most of
// its version annotations.
//
// The sibling wins because it describes the property that carries it, not the
// shared type it points at: two properties may reference one schema and have
// been added in different versions.
func refExtensionString(ref *openapi3.SchemaRef, key string) string {
	if ref == nil {
		return ""
	}
	if v := extensionString(ref.Extensions, key); v != "" {
		return v
	}
	if ref.Value == nil {
		return ""
	}
	return extensionString(ref.Value.Extensions, key)
}

// extensionBool reads a bool-valued extension from a map.
func extensionBool(extensions map[string]any, key string) bool {
	if extensions == nil {
		return false
	}
	raw, ok := extensions[key]
	if !ok {
		return false
	}
	switch v := raw.(type) {
	case json.RawMessage:
		var b bool
		if err := json.Unmarshal(v, &b); err != nil {
			return false
		}
		return b
	case bool:
		return v
	default:
		return false
	}
}

// extensionStringSlice reads a string-slice-valued extension from a map.
func extensionStringSlice(extensions map[string]any, key string) []string {
	if extensions == nil {
		return nil
	}
	raw, ok := extensions[key]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case json.RawMessage:
		var ss []string
		if err := json.Unmarshal(v, &ss); err != nil {
			return nil
		}
		return ss
	case []any:
		ss := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				ss = append(ss, s)
			}
		}
		return ss
	default:
		return nil
	}
}

// errorResponseWrappers reads the x-error-responses extension and returns
// the wrapper-schema names referenced by each entry (see [wrapperName]),
// each as its [errwrap.Canonical] constant.
//
// Returns nil when the extension is absent or empty. Malformed entries
// are skipped; the caller treats absence as "no auxiliary error
// responses".
func errorResponseWrappers(op *openapi3.Operation) []string {
	var out []string
	for _, ref := range extensionRefs(op, extErrorResponses) {
		if name := wrapperName(ref); name != "" {
			out = append(out, errwrap.Canonical(name))
		}
	}
	return out
}

// errorTypes reads the x-error-types extension and returns each referenced
// wrapper with the status and root cause type its schema declares. An entry
// whose wrapper schema is missing, or lacks either value, is skipped with a
// warning: the operation still generates, without that error type.
func errorTypes(op *openapi3.Operation, spec *openapi3.T) []ir.ErrorType {
	var out []ir.ErrorType
	for _, ref := range extensionRefs(op, extErrorTypes) {
		var schema *openapi3.SchemaRef
		if spec != nil && spec.Components != nil {
			schema = spec.Components.Schemas[refToSchemaKey(ref)]
		}
		if schema == nil || schema.Value == nil {
			log.Printf("osgen: x-error-types entry %q skipped: no such component schema", ref)
			continue
		}
		et := ir.ErrorType{
			Name:          wrapperName(ref),
			Status:        extensionInt(schema.Value.Extensions, extErrorStatus),
			RootCauseType: extensionString(schema.Value.Extensions, extErrorRootCauseType),
		}
		if et.Name == "" || et.Status == 0 || et.RootCauseType == "" {
			log.Printf("osgen: x-error-types entry %q skipped: its schema needs an integer %s and a string %s",
				ref, extErrorStatus, extErrorRootCauseType)
			continue
		}
		out = append(out, et)
	}
	return out
}

// extensionRefs returns the non-empty $ref of each entry in an operation
// extension whose value is a list of {$ref: ...} objects, such as
// x-error-responses and x-error-types. Returns nil when the extension is
// absent or malformed.
func extensionRefs(op *openapi3.Operation, key string) []string {
	if op == nil || op.Extensions == nil {
		return nil
	}
	raw, ok := op.Extensions[key]
	if !ok {
		return nil
	}

	type refEntry struct {
		Ref string `json:"$ref"`
	}
	var entries []refEntry
	switch v := raw.(type) {
	case json.RawMessage:
		if err := json.Unmarshal(v, &entries); err != nil {
			return nil
		}
	case []any:
		for _, item := range v {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if r, ok := obj["$ref"].(string); ok {
				entries = append(entries, refEntry{Ref: r})
			}
		}
	default:
		return nil
	}

	var out []string
	for _, e := range entries {
		if e.Ref != "" {
			out = append(out, e.Ref)
		}
	}
	return out
}

// wrapperName returns the wrapper-schema name a $ref points at. The bundled
// spec uses internal $refs of the form
// "#/components/schemas/_common.errors___<WrapperName>"; the wrapper name is
// the segment after the final triple-underscore, or after the last "/" for a
// source-form ref.
func wrapperName(ref string) string {
	if i := strings.LastIndex(ref, "___"); i >= 0 {
		return ref[i+3:]
	}
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ref
}

// extensionInt reads an int-valued extension from a map.
func extensionInt(extensions map[string]any, key string) int {
	switch v := extensions[key].(type) {
	case json.RawMessage:
		var n int
		if err := json.Unmarshal(v, &n); err != nil {
			return 0
		}
		return n
	case float64:
		return int(v)
	default:
		return 0
	}
}
