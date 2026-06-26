// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"encoding/json"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
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

	// extErrorResponses lists wrapper-schema $refs for partial-failure
	// shapes an operation may surface alongside its primary 2xx response
	// (per the proposed x-error-responses OpenAPI extension). Each entry
	// is an object {$ref: "#/components/schemas/_common.errors___WrapperName"};
	// the codegen extracts the terminal segment after the last underscore-
	// triple as the wrapper name (e.g. "BulkItems") and feeds it into the
	// emit phase.
	extErrorResponses = "x-error-responses"
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
// the wrapper-schema names referenced by each entry. The bundled spec
// uses internal $refs of the form
// "#/components/schemas/_common.errors___<WrapperName>"; the wrapper name
// is the segment after the final triple-underscore.
//
// Returns nil when the extension is absent or empty. Malformed entries
// are skipped; the caller treats absence as "no auxiliary error
// responses".
func errorResponseWrappers(op *openapi3.Operation) []string {
	if op == nil || op.Extensions == nil {
		return nil
	}
	raw, ok := op.Extensions[extErrorResponses]
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
		// Wrapper names live under #/components/schemas/<...>___<Name>;
		// the segment after the last "___" is the wrapper.
		ref := e.Ref
		if i := strings.LastIndex(ref, "___"); i >= 0 {
			ref = ref[i+3:]
		} else if i := strings.LastIndex(ref, "/"); i >= 0 {
			ref = ref[i+1:]
		}
		if ref != "" {
			out = append(out, ref)
		}
	}
	return out
}
