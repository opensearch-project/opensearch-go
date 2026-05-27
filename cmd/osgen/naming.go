// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"fmt"
	"go/token"
	"go/types"
	"strings"
)

// acronyms maps lowercase segments to their Go-idiomatic uppercase form.
// Keys are sorted alphabetically; keep them that way when adding entries.
//
//nolint:gochecknoglobals // const-ish read-only lookup table
var acronyms = map[string]string{
	"api":   "API",
	"dsl":   "DSL",
	"http":  "HTTP",
	"https": "HTTPS",
	"id":    "ID",
	"ip":    "IP",
	"json":  "JSON",
	"pit":   "PIT",
	"sql":   "SQL",
	"ssl":   "SSL",
	"tcp":   "TCP",
	"tls":   "TLS",
	"ttl":   "TTL",
	"uri":   "URI",
	"url":   "URL",
	"uuid":  "UUID",
	"xml":   "XML",
}

// titleSegment capitalizes a segment with full acronym expansion.
func titleSegment(s string) string {
	if upper, ok := acronyms[strings.ToLower(s)]; ok {
		return upper
	}
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// pathBuilderName returns the path builder struct name from an operation group.
// e.g. "cluster.stats" -> "ClusterStatsPath"
// e.g. "security.reload_http_certificates" -> "SecurityReloadHTTPCertificatesPath"
func pathBuilderName(group string) string {
	parts := strings.FieldsFunc(group, func(r rune) bool {
		return r == '.' || r == '_'
	})
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString(titleSegment(p))
	}
	sb.WriteString("Path")
	return sb.String()
}

// pathFieldName converts a raw spec parameter name to the EXPORTED field name
// used by the path builder struct.
// e.g. "index_uuid" -> "IndexUUID", "node_id" -> "NodeID"
func pathFieldName(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '_' || r == '.'
	})
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString(titleSegment(p))
	}
	result := sb.String()
	if result == "" || !token.IsIdentifier(result) {
		panic(fmt.Sprintf("pathFieldName(%q) produced invalid Go identifier %q", name, result))
	}
	return result
}

// unexportedFieldName converts a spec parameter name to an unexported Go field
// name with full acronym expansion. First segment stays lowercase.
// e.g. "index_uuid" -> "indexUUID", "node_id" -> "nodeID", "chime.url" -> "chimeURL"
func unexportedFieldName(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '_' || r == '.'
	})
	var sb strings.Builder
	for i, p := range parts {
		if i == 0 {
			// First segment is always all lowercase to keep the identifier
			// unexported. If the segment is itself an acronym (id, url, http),
			// we still want plain lowercase rather than "iD" / "uRL" / "hTTP".
			sb.WriteString(strings.ToLower(p))
		} else {
			sb.WriteString(titleSegment(p))
		}
	}
	result := sb.String()
	if token.IsKeyword(result) || isPredeclaredIdent(result) {
		return result + "Val"
	}
	return result
}

// goFieldName is an alias for unexportedFieldName, used by the api subcommand.
func goFieldName(name string) string {
	return unexportedFieldName(name)
}

// baseGoName converts a JSON field name to an EXPORTED Go field name.
// Strips leading underscores, splits on _ and ., title-cases each segment.
// e.g. "_nodes" -> "Nodes", "cluster_uuid" -> "ClusterUUID"
func baseGoName(jsonName string) string {
	name := strings.TrimLeft(jsonName, "_")
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '_' || r == '.' || r == '-'
	})
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString(titleSegment(p))
	}
	result := sb.String()
	if len(result) > 0 && result[0] >= '0' && result[0] <= '9' {
		result = "N" + result
	}
	if result == "" || !token.IsIdentifier(result) {
		panic(fmt.Sprintf("baseGoName(%q) produced invalid Go identifier %q", jsonName, result))
	}
	return result
}

// pkgScopedName returns the Go type prefix for an operation, scoped to its
// target package. Core operations retain their full group prefix because they
// share the osapi package ("cluster.stats" -> "ClusterStats"). Plugin
// operations strip the plugin prefix because the package already provides it
// ("knn.stats" -> "Stats" within package knn).
func pkgScopedName(group string) string {
	prefix := groupPrefix(group)
	name := scopedNameForPkg(group, prefix)

	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '.' || r == '_'
	})
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString(titleSegment(p))
	}
	return sb.String()
}

// scopedNameForPkg returns the input to titleSegment-ize for pkgScopedName.
// Core groups keep their prefix (or strip "_core."); plugin groups drop
// the plugin prefix.
func scopedNameForPkg(group, prefix string) string {
	if coreGroups[prefix] {
		if prefix == "_core" {
			return group[len("_core."):]
		}
		return group
	}
	if _, after, ok := strings.Cut(group, "."); ok {
		return after
	}
	return group
}

// isPredeclaredIdent reports whether s shadows a Go predeclared identifier
// (builtins, named primitive types, true/false/nil/iota). The set is
// sourced from go/types.Universe so it stays current with the language.
func isPredeclaredIdent(s string) bool {
	if s == "_" {
		return false
	}
	return types.Universe.Lookup(s) != nil
}

// schemaTypeName converts an OpenAPI spec schema key (e.g.
// "cluster.health___IndexHealthStats") to a Go type name. It implements
// de-stutter for operation-specific types and prefix-free naming for shared
// _common___ types.
//
// When isRespBody is true, the function returns the response body type name
// (e.g. "ClusterHealthResp") regardless of the local schema name.
func schemaTypeName(schemaKey string, isRespBody bool) string {
	groupPart, localPart, ok := strings.Cut(schemaKey, "___")
	if !ok {
		return pascalFromSegments(schemaKey)
	}

	if groupPart == "_common" {
		return pascalFromSegments(localPart)
	}

	// Handle group._common (e.g. "nodes._common___NodesResponseBase").
	if before, ok0 := strings.CutSuffix(groupPart, "._common"); ok0 {
		parentGroup := before
		prefix := pascalFromSegments(parentGroup)
		local := pascalFromSegments(localPart)
		return deStutterPrefix(prefix, local, parentGroup)
	}

	prefix := pascalFromSegments(groupPart)

	if isRespBody {
		return prefix + "Resp"
	}

	local := pascalFromSegments(localPart)
	return deStutterPrefix(prefix, local, groupPart)
}

// deStutterPrefix removes the first PascalCase-boundary occurrence of the
// group's leaf word from local, provided the result is non-empty.
func deStutterPrefix(prefix, local, group string) string {
	leaf := groupLeaf(group)
	leafPascal := pascalFromSegments(leaf)
	if leafPascal == "" {
		return prefix + local
	}
	idx := strings.Index(local, leafPascal)
	if idx < 0 {
		return prefix + local
	}
	after := idx + len(leafPascal)
	if after < len(local) && local[after] >= 'a' && local[after] <= 'z' {
		return prefix + local
	}
	trimmed := local[:idx] + local[after:]
	if trimmed != "" {
		return prefix + trimmed
	}
	return prefix + local
}

// groupLeaf returns the last segment of a dotted group name.
func groupLeaf(group string) string {
	if idx := strings.LastIndexByte(group, '.'); idx >= 0 {
		return group[idx+1:]
	}
	return group
}

// pascalFromSegments converts a dot-and-underscore-separated string to
// PascalCase with acronym expansion. Leading "_core" is converted to "Core".
func pascalFromSegments(s string) string {
	s = strings.TrimPrefix(s, "_core.")
	if s == "_core" {
		return "Core"
	}
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '.' || r == '_'
	})
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString(titleSegment(p))
	}
	return sb.String()
}

// scalarAliases maps OpenAPI spec $ref suffixes to their Go primitive types.
// Schemas matching these are inlined as the primitive type rather than
// generating a named Go type. Keys are sorted alphabetically; keep them
// that way when adding entries.
//
//nolint:gochecknoglobals // const-ish read-only lookup table
var scalarAliases = map[string]string{
	"_common___BuiltinScriptLanguage":   "string",
	"_common___ByteCount":               "int64",
	"_common___ClusterSearchStatus":     "string",
	"_common___DataStreamName":          "string",
	"_common___DataStreamNames":         "string",
	"_common___DateFormat":              "string",
	"_common___DateMath":                "string",
	"_common___DateTime":                "string",
	"_common___Distance":                "string",
	"_common___Duration":                "string",
	"_common___DurationLarge":           "string",
	"_common___DurationValueUnitMicros": "int64",
	"_common___DurationValueUnitMillis": "int64",
	"_common___DurationValueUnitNanos":  "int64",
	"_common___EmptyObject":             "struct{}",
	"_common___EpochTimeUnitMillis":     "int64",
	"_common___EpochTimeUnitSeconds":    "int64",
	"_common___Field":                   "string",
	"_common___Fields":                  "string",
	"_common___Fuzziness":               "string",
	"_common___GeoHash":                 "string",
	"_common___Host":                    "string",
	"_common___HumanReadableByteCount":  "string",
	"_common___Id":                      "string",
	"_common___Ids":                     "string",
	"_common___IndexAlias":              "string",
	"_common___IndexName":               "string",
	"_common___Indices":                 "[]string",
	"_common___Ip":                      "string",
	"_common___Name":                    "string",
	"_common___Names":                   "[]string",
	"_common___NodeId":                  "string",
	"_common___NodeName":                "string",
	"_common___Password":                "string",
	"_common___PercentageNumber":        "float64",
	"_common___PercentageString":        "string",
	"_common___PipelineName":            "string",
	"_common___RelationName":            "string",
	"_common___ResourceType":            "string",
	"_common___Routing":                 "string",
	"_common___ScrollId":                "string",
	"_common___SequenceNumber":          "int64",
	"_common___SortOrder":               "string",
	"_common___StringifiedBoolean":      "string",
	"_common___StringifiedDouble":       "string",
	"_common___StringifiedInteger":      "string",
	"_common___StringifiedLong":         "string",
	"_common___SuggestMode":             "string",
	"_common___TaskId":                  "string",
	"_common___TimeZone":                "string",
	"_common___TransportAddress":        "string",
	"_common___Type":                    "string",
	"_common___Uri":                     "string",
	"_common___Username":                "string",
	"_common___Uuid":                    "string",
	"_common___VersionNumber":           "int64",
	"_common___VersionString":           "string",
	"_common___byte":                    "int",
	"_common___double":                  "float64",
	"_common___float":                   "float64",
	"_common___integer":                 "int",
	"_common___long":                    "int64",
	"_common___short":                   "int",
	"_common___uint":                    "int",
}

// isScalarAlias returns the Go primitive type for schema references that are
// simple type aliases and should not generate named Go types. The ref should
// be the schema key portion after "#/components/schemas/" (e.g. "_common___uint").
func isScalarAlias(ref string) (string, bool) {
	goType, ok := scalarAliases[ref]
	return goType, ok
}
