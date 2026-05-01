// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"encoding/json"

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

	// extDistributionsExcluded lists distributions (e.g. "amazon-managed") where
	// the operation is unavailable.
	extDistributionsExcluded = "x-distributions-excluded"
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
