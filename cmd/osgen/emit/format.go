// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/ir"
)

const (
	commentLineWidth    = 72
	commentContentWidth = commentLineWidth - len("// ")
)

// CommentWrap wraps a description for use as a type-level doc comment.
// Prefixes each line with "// " and includes a leading "//\n" paragraph separator.
func CommentWrap(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("//\n")
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			sb.WriteString("//\n")
		} else {
			sb.WriteString("// ")
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// WrapLine takes a single logical comment line (without "// " prefix) and
// word-wraps it so no output line exceeds commentLineWidth. Each output
// line is prefixed with "// ". The result includes a leading "//\n".
func WrapLine(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	words := strings.Fields(text)
	var sb strings.Builder
	sb.WriteString("//\n")

	lineLen := 0
	for i, w := range words {
		if i == 0 {
			sb.WriteString("// ")
			sb.WriteString(w)
			lineLen = len(w)
			continue
		}
		if lineLen+1+len(w) > commentContentWidth {
			sb.WriteString("\n// ")
			sb.WriteString(w)
			lineLen = len(w)
		} else {
			sb.WriteByte(' ')
			sb.WriteString(w)
			lineLen += 1 + len(w)
		}
	}
	return sb.String()
}

// deprecatedPrefix is the godoc deprecation marker. Spec authors sometimes
// write it followed by a comma instead of the godoc-required colon; WrapField
// rewrites the former to the latter so the marker is honored.
const deprecatedPrefix = "Deprecated"

// WrapField wraps a description for use as a struct field doc comment.
// Output is tab-indented with "// " prefix on each line.
func WrapField(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if after, ok := strings.CutPrefix(text, deprecatedPrefix+", "); ok {
		text = deprecatedPrefix + ": " + after
	}

	const maxWidth = 72
	words := strings.Fields(text)
	var sb strings.Builder

	lineLen := 0
	for i, w := range words {
		if i == 0 {
			sb.WriteString("// ")
			sb.WriteString(w)
			lineLen = len(w)
			continue
		}
		if lineLen+1+len(w) > maxWidth {
			sb.WriteString("\n\t// ")
			sb.WriteString(w)
			lineLen = len(w)
		} else {
			sb.WriteByte(' ')
			sb.WriteString(w)
			lineLen += 1 + len(w)
		}
	}
	return sb.String()
}

// NormalizeSemver ensures a version string has three components.
func NormalizeSemver(v string) string {
	switch strings.Count(v, ".") {
	case 0:
		return v + ".0.0"
	case 1:
		return v + ".0"
	default:
		return v
	}
}

// AvailabilityNote builds the version/deprecation annotation text.
// Returns empty string if no annotation is needed.
func AvailabilityNote(versionAdded, versionDeprecated, deprecMsg string) string {
	versionAdded = strings.TrimSpace(versionAdded)
	versionDeprecated = strings.TrimSpace(versionDeprecated)
	deprecMsg = strings.TrimSpace(deprecMsg)

	if versionAdded == "" && versionDeprecated == "" {
		return ""
	}

	var sb strings.Builder

	if versionDeprecated != "" {
		sb.WriteString("Deprecated: since ")
		sb.WriteString(NormalizeSemver(versionDeprecated))
		sb.WriteString(".")
		if versionAdded != "" {
			sb.WriteString(" Available >= ")
			sb.WriteString(NormalizeSemver(versionAdded))
			sb.WriteString(".")
		}
		if deprecMsg != "" {
			sb.WriteString(" ")
			sb.WriteString(deprecMsg)
		}
	} else {
		sb.WriteString("Available: >= ")
		sb.WriteString(NormalizeSemver(versionAdded))
		sb.WriteString(".")
	}

	return sb.String()
}

// MethodDocData holds the metadata needed to render a method-level doc comment.
type MethodDocData struct {
	MethodName        string
	Group             string
	Description       string
	HTTPMethods       []string
	PrimaryPath       string
	VersionAdded      string
	VersionDeprecated string
	DeprecationMsg    string
	ExcludedDistros   []string
	DocsURL           string
}

// MethodComment builds a complete go doc comment block for a dispatch method.
// The output matches the structure used on Req/Resp type comments.
func MethodComment(d MethodDocData) string {
	var sb strings.Builder

	desc := strings.TrimSpace(d.Description)
	if desc != "" {
		firstLine, rest := splitFirstLine(desc)
		sb.WriteString("// ")
		sb.WriteString(d.MethodName)
		sb.WriteByte(' ')
		lower := lowerFirst(firstLine)
		sb.WriteString(lower)
		if !strings.HasSuffix(lower, ".") {
			sb.WriteByte('.')
		}
		if rest != "" {
			sb.WriteByte('\n')
			sb.WriteString(CommentWrap(rest))
		}
	} else {
		sb.WriteString("// ")
		sb.WriteString(d.MethodName)
		sb.WriteString(" executes the ")
		sb.WriteString(d.Group)
		sb.WriteString(" operation.")
	}

	if d.PrimaryPath != "" && len(d.HTTPMethods) > 0 {
		sb.WriteString("\n//\n")
		if len(d.HTTPMethods) == 1 {
			sb.WriteString("// ")
			sb.WriteString(d.HTTPMethods[0])
			sb.WriteByte(' ')
			sb.WriteString(d.PrimaryPath)
		} else {
			sb.WriteString("// Path: ")
			sb.WriteString(d.PrimaryPath)
			sb.WriteString("\n//\n// Methods: ")
			sb.WriteString(strings.Join(d.HTTPMethods, ", "))
		}
	}

	if note := AvailabilityNote(d.VersionAdded, d.VersionDeprecated, d.DeprecationMsg); note != "" {
		sb.WriteString("\n//\n// ")
		sb.WriteString(note)
	}

	if len(d.ExcludedDistros) > 0 {
		sb.WriteString("\n//\n// Not available on: ")
		sb.WriteString(strings.Join(d.ExcludedDistros, ", "))
		sb.WriteByte('.')
	}

	if d.DocsURL != "" {
		sb.WriteString("\n//\n// See: ")
		sb.WriteString(d.DocsURL)
	}

	return sb.String()
}

// lowerFirst lowercases the first rune of s, unless it looks like an acronym
// (two leading uppercase letters) or is already lowercase.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	if !unicode.IsUpper(r) {
		return s
	}
	if len(s) > size {
		next, _ := utf8.DecodeRuneInString(s[size:])
		if unicode.IsUpper(next) {
			return s
		}
	}
	return string(unicode.ToLower(r)) + s[size:]
}

// splitFirstLine splits a description into its first paragraph line and
// the remainder. It splits on the first blank line (paragraph separator)
// or the first newline if there are no blank lines.
func splitFirstLine(s string) (string, string) {
	s = strings.TrimSpace(s)
	if before, after, ok := strings.Cut(s, "\n\n"); ok {
		return strings.TrimSpace(before), strings.TrimSpace(after)
	}
	if before, after, ok := strings.Cut(s, "\n"); ok {
		return strings.TrimSpace(before), strings.TrimSpace(after)
	}
	return s, ""
}

// FieldNeedsSep returns true if a blank line should precede field[i].
// A blank line is inserted whenever either the current or previous field
// carries a comment or version annotation.
func FieldNeedsSep(hasAnnotation func(int) bool, i int) bool {
	if i == 0 {
		return false
	}
	return hasAnnotation(i-1) || hasAnnotation(i)
}

// HTTPMethodConst converts an HTTP method string to its net/http constant name.
func HTTPMethodConst(method string) string {
	switch method {
	case "GET":
		return "http.MethodGet"
	case "POST":
		return "http.MethodPost"
	case "PUT":
		return "http.MethodPut"
	case "DELETE":
		return "http.MethodDelete"
	case "HEAD":
		return "http.MethodHead"
	case "PATCH":
		return "http.MethodPatch"
	case "OPTIONS":
		return "http.MethodOptions"
	default:
		return `"` + method + `"`
	}
}

// qualifierFunc returns a template function that qualifies type names with the
// core package prefix when the operation is in a plugin package. For core
// operations it returns the type unchanged.
func qualifierFunc(isPlugin bool, reg *ir.TypeRegistry) func(string) string {
	if !isPlugin || reg == nil {
		return func(goType string) string { return goType }
	}
	return func(goType string) string {
		return qualifyType(goType, reg)
	}
}

// qualifyType prefixes shared type names in goType with the registry's core
// package name (e.g. "[]InsightsTopQuery" becomes "[]osapi.InsightsTopQuery").
func qualifyType(goType string, reg *ir.TypeRegistry) string {
	prefix, base := unwrapGoType(goType)
	if base == "" || isBuiltin(base) || strings.Contains(base, ".") {
		return goType
	}
	t, ok := reg.LookupByName(base)
	if !ok || t.Scope != ir.ScopeShared {
		return goType
	}
	return prefix + reg.CorePkg + "." + base
}

// isCrossPackageType returns true if goType references a shared type from the
// core package (i.e. would need qualification in a plugin package).
func isCrossPackageType(goType string, reg *ir.TypeRegistry) bool {
	_, base := unwrapGoType(goType)
	if base == "" || isBuiltin(base) || strings.Contains(base, ".") {
		return false
	}
	t, ok := reg.LookupByName(base)
	return ok && t.Scope == ir.ScopeShared
}

// unwrapGoType splits a Go type expression into its prefix (pointer/slice/map wrappers)
// and the base type name.
func unwrapGoType(goType string) (string, string) {
	i := 0
	for i < len(goType) {
		switch {
		case goType[i] == '*':
			i++
		case i+1 < len(goType) && goType[i:i+2] == "[]":
			i += 2
		case i+4 < len(goType) && goType[i:i+4] == "map[":
			bracket := strings.IndexByte(goType[i:], ']')
			if bracket < 0 {
				return goType, ""
			}
			i += bracket + 1
		default:
			return goType[:i], goType[i:]
		}
	}
	return goType, ""
}

func isBuiltin(name string) bool {
	switch name {
	case "bool", "byte", "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64", "complex64", "complex128",
		"string", "error", "any", "rune", "uintptr":
		return true
	}
	return false
}
