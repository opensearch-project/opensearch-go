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
	"sort"
	"strings"
)

// idiomaticAbbreviations rewrites non-idiomatic substrings produced
// by pascal-casing spec names into idiomatic Go forms (acronym
// capitalization, compound-noun splits, established short forms).
// Applied as the last step of every Go-identifier constructor.
//
// Each entry has a `tailUpperOnly` flag controlling whether the
// match must be followed by an uppercase letter (true) or accepts
// end-of-string + uppercase (false). `tailUpperOnly` exists for
// `Response` specifically: a standalone `Response` name (like the
// spec's `_common___SearchResponse` wrapper schema) collides with
// the operation-level response-body name `<Op>Resp`, so the
// substitution is restricted to compound forms (e.g.
// `BulkResponseItem` -> `BulkRespItem`) where the trailing PascalCase
// segment is preserved.
//
//nolint:gochecknoglobals // const-ish read-only lookup table
var idiomaticAbbreviations = []struct {
	from, to      string
	tailUpperOnly bool
}{
	// Sorted by `from`. Order-independent: no entry's `to` re-triggers another
	// entry's `from` at a PascalCase boundary. The aggregation-result entries
	// (D/L/S/UL/UM/Sig + "terms", and "tdigest") split the terse spec oneOf
	// titles into the idiomatic PascalCase the decoded type already uses
	// (e.g. "ulterms" -> "ULTerms", matching UnsignedLongTermsAggregate).
	{from: "Dterms", to: "DTerms"},
	{from: "Forcemerge", to: "ForceMerge"},
	{from: "Lrareterms", to: "LRareTerms"},
	{from: "Lterms", to: "LTerms"},
	{from: "Mget", to: "MGet"},
	{from: "Msearch", to: "MSearch"},
	{from: "Mtermvectors", to: "MTermVectors"},
	{from: "Response", to: "Resp", tailUpperOnly: true},
	{from: "Siglterms", to: "SigLTerms"},
	{from: "Sigsterms", to: "SigSTerms"},
	{from: "Srareterms", to: "SRareTerms"},
	{from: "Sterms", to: "STerms"},
	{from: "Tdigest", to: "TDigest"},
	{from: "Termvectors", to: "TermVectors"},
	{from: "Ulterms", to: "ULTerms"},
	{from: "Umrareterms", to: "UMRareTerms"},
	{from: "Umsigterms", to: "UMSigTerms"},
	{from: "Umterms", to: "UMTerms"},
}

// applyIdiomaticAbbreviations applies all [idiomaticAbbreviations] to
// s, matching each pattern at PascalCase boundaries (followed by
// uppercase, or end-of-string for entries with tailUpperOnly=false).
func applyIdiomaticAbbreviations(s string) string {
	// Normalize embedded acronyms first. A single spec token like
	// "IsmTemplate" pascal-cases to "IsmTemplate" (titleSegment only expands
	// whole, separately-delimited segments), leaving the acronym in mixed
	// case. Canonicalizing "Ism" -> "ISM" here -- at PascalCase boundaries --
	// keeps acronym casing consistent wherever it appears in an identifier
	// (prefix or local part), which also lets deStutterPrefix match.
	for _, a := range acronymBoundaryReplacements() {
		s = replaceAtPascalBoundary(s, a.from, a.to, a.tailUpperOnly)
	}
	for _, a := range idiomaticAbbreviations {
		s = replaceAtPascalBoundary(s, a.from, a.to, a.tailUpperOnly)
	}
	return s
}

// acronymBoundaryReplacements derives, from the acronyms table, the
// substitutions that canonicalize a title-cased embedded acronym (e.g. "Ism",
// "Knn") to its idiomatic all-caps form (e.g. "ISM", "KNN"). Two-letter and
// longer acronyms qualify; single-letter "acronyms" would over-match, so they
// are skipped. Each is applied at PascalCase boundaries (followed by an
// uppercase letter or end-of-string), so "Ismael"-style words are untouched.
//
//nolint:gochecknoglobals // derived once from the acronyms table
var acronymBoundaryReplacementsCache []struct {
	from, to      string
	tailUpperOnly bool
}

func acronymBoundaryReplacements() []struct {
	from, to      string
	tailUpperOnly bool
} {
	if acronymBoundaryReplacementsCache != nil {
		return acronymBoundaryReplacementsCache
	}
	for lower, upper := range acronyms {
		if len(lower) < 2 {
			continue
		}
		title := strings.ToUpper(lower[:1]) + lower[1:]
		if title == upper {
			continue // already idiomatic (e.g. all-lowercase has no title form to fix)
		}
		acronymBoundaryReplacementsCache = append(acronymBoundaryReplacementsCache, struct {
			from, to      string
			tailUpperOnly bool
		}{from: title, to: upper})
	}
	// Deterministic order: map iteration is random, and codegen output must be
	// stable across runs. Longest-from first so multi-token acronyms can't be
	// partially shadowed by a shorter one.
	sort.Slice(acronymBoundaryReplacementsCache, func(i, j int) bool {
		a, b := acronymBoundaryReplacementsCache[i].from, acronymBoundaryReplacementsCache[j].from
		if len(a) != len(b) {
			return len(a) > len(b)
		}
		return a < b
	})
	return acronymBoundaryReplacementsCache
}

// replaceAtPascalBoundary replaces every occurrence of old in s with
// next, but only when old is followed by an uppercase letter -- or
// end-of-string if tailUpperOnly is false. Lowercase-suffix variants
// (e.g. "Responses", "Responsible") are left intact in either mode.
func replaceAtPascalBoundary(s, old, next string, tailUpperOnly bool) string {
	if old == "" || !strings.Contains(s, old) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if i+len(old) <= len(s) && s[i:i+len(old)] == old {
			after := i + len(old)
			atEnd := after == len(s)
			atUpper := !atEnd && s[after] >= 'A' && s[after] <= 'Z'
			if atUpper || (atEnd && !tailUpperOnly) {
				b.WriteString(next)
				i = after
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// acronyms maps lowercase segments to their Go-idiomatic uppercase form.
// Keys are sorted alphabetically; keep them that way when adding entries.
//
//nolint:gochecknoglobals // const-ish read-only lookup table
var acronyms = map[string]string{
	"api":   "API",
	"bm25":  "BM25", // Best Matching 25 ranking function
	"cjk":   "CJK",  // Chinese, Japanese, Korean
	"cpu":   "CPU",
	"csv":   "CSV",
	"dfi":   "DFI", // Divergence From Independence
	"dfr":   "DFR", // Divergence From Randomness
	"dfs":   "DFS", // Distributed Frequency Search
	"dsl":   "DSL",
	"fs":    "FS",  // File System (store type)
	"gc":    "GC",  // Garbage Collection
	"hdr":   "HDR", // High Dynamic Range (percentiles)
	"html":  "HTML",
	"http":  "HTTP",
	"https": "HTTPS",
	"ib":    "IB",  // Information-Based similarity
	"icu":   "ICU", // International Components for Unicode
	"id":    "ID",
	"ids":   "IDs",
	"ip":    "IP",
	"ism":   "ISM", // Index State Management
	"json":  "JSON",
	"jvm":   "JVM",
	"knn":   "KNN", // k-Nearest Neighbors
	"lmd":   "LMD",  // Language Model Dirichlet similarity
	"lmj":   "LMJ",  // Language Model Jelinek-Mercer similarity
	"ltr":   "LTR",  // Learning to Rank
	"ml":    "ML",   // Machine Learning
	"mmap":  "MMap", // memory-mapped store type
	"nio":   "NIO",  // New I/O (Java, store type)
	"pit":   "PIT",  // Point In Time
	"pits":  "PITs",
	"ppl":   "PPL", // Piped Processing Language
	"sm":    "SM",  // Snapshot Management
	"smtp":  "SMTP",
	"sns":   "SNS", // Simple Notification Service
	"sql":   "SQL",
	"ssl":   "SSL",
	"tcp":   "TCP",
	"tfidf": "TFIDF", // Term Frequency-Inverse Document Frequency
	"tls":   "TLS",
	"ttl":   "TTL",
	"uax":   "UAX", // Unicode Annex (UAX #29 text segmentation)
	"ubi":   "UBI", // User Behavior Insights
	"uri":   "URI",
	"url":   "URL",
	"uuid":  "UUID",
	"wkt":   "WKT", // Well-Known Text (geometry format)
	"wlm":   "WLM", // Workload Management
	"xml":   "XML",
	"xy":    "XY", // Cartesian x/y coordinate types
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

// listPathFieldNames maps a singular spec path-parameter name to the plural Go
// field name used when that parameter accepts a comma-separated list. The
// OpenSearch spec names multi-value path parameters in the singular (e.g.
// "index", whose schema is the array-capable _common___Indices), but a Go
// []string field reads naturally with a plural name. Only entries here are
// pluralized; every other list parameter keeps its spec name.
//
//nolint:gochecknoglobals // const-ish read-only lookup table
var listPathFieldNames = map[string]string{
	"index": "Indices",
}

// pathFieldNameList is pathFieldName with list awareness: when the parameter
// accepts a list and has a plural override in listPathFieldNames, it returns
// the plural form (e.g. "index" + list -> "Indices"). The scalar form of the
// same parameter (isList false) is unaffected and stays "Index". Both the api
// (Req struct) and paths (builder struct) subcommands call this so the two
// generated field names stay in sync.
func pathFieldNameList(name string, isList bool) string {
	if isList {
		if plural, ok := listPathFieldNames[name]; ok {
			return plural
		}
	}
	return pathFieldName(name)
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
	result := applyIdiomaticAbbreviations(sb.String())
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
// share the opensearchapi package ("cluster.stats" -> "ClusterStats"). Plugin
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
	return applyIdiomaticAbbreviations(sb.String())
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
	return applyIdiomaticAbbreviations(sb.String())
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
