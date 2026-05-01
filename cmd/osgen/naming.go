// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import "strings"

// acronyms maps lowercase segments to their Go-idiomatic uppercase form.
var acronyms = map[string]string{
	"id": "ID", "uuid": "UUID", "uri": "URI", "url": "URL",
	"http": "HTTP", "https": "HTTPS", "ttl": "TTL", "ip": "IP",
	"tcp": "TCP", "tls": "TLS", "ssl": "SSL", "api": "API",
	"json": "JSON", "xml": "XML", "sql": "SQL",
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
	return sb.String()
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
			lower := strings.ToLower(p)
			if upper, ok := acronyms[lower]; ok {
				sb.WriteString(strings.ToLower(upper[:1]) + upper[1:])
			} else {
				sb.WriteString(lower)
			}
		} else {
			sb.WriteString(titleSegment(p))
		}
	}
	result := sb.String()
	if isGoKeyword(result) {
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
		return r == '_' || r == '.'
	})
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString(titleSegment(p))
	}
	return sb.String()
}

// pkgScopedName returns the Go type prefix for an operation, scoped to its
// target package. Core operations retain their full group prefix because they
// share the opensearchapi package ("cluster.stats" -> "ClusterStats"). Plugin
// operations strip the plugin prefix because the package already provides it
// ("knn.stats" -> "Stats" within package knn).
func pkgScopedName(group string) string {
	prefix := groupPrefix(group)
	var name string
	if coreGroups[prefix] {
		if prefix == "_core" {
			name = group[len("_core."):]
		} else {
			name = group
		}
	} else {
		if idx := strings.IndexByte(group, '.'); idx >= 0 {
			name = group[idx+1:]
		} else {
			name = group
		}
	}

	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '.' || r == '_'
	})
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString(titleSegment(p))
	}
	return sb.String()
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
