// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"cmp"
	"fmt"
	"math"
	"slices"

	"github.com/opensearch-project/opensearch-go/cmd/osapilint/v5/internal/apirev"
)

// transitions.go is the generic transition registry: the version-neutral types
// and the map of hand-authored, per-adjacent-hop migration data. The linter
// (applydelta.go), composition (compose.go), and CLI (run.go) are all
// version-agnostic; everything migration-specific lives in a hop, registered
// here.
//
// Adding a new transition (e.g. v3 -> v4) is purely additive: run cmd/gensurface
// for the new version, embed its surface in surfaces, author a hop_vX_to_vY.go
// with a hop value, and register it in hops. No linter change is required - see
// README.md "Adding a new hop".

// major is an opensearch-go module major version (the N in .../opensearch-go/vN).
type major int

// Major is the exported alias of major. It lets the library entrypoint
// MigrateSDK name versions in its public surface without changing any of the
// package's internal signatures (which keep using major).
type Major = major

// version is a release line with its own surface: a major, and the minor its
// surface starts at. A line covers every release up to the next line of the same
// major, so {5, 0} covers v5.0 and v5.1, which are compatible, and {5, 2} starts
// where generated types broke compatibility within v5.
type version struct {
	major major
	minor int
}

// String renders v as a version label: "v5" for a major's first line, "v5.2"
// for a later one.
func (v version) String() string {
	if v.minor == 0 {
		return fmt.Sprintf("v%d", v.major)
	}
	return fmt.Sprintf("v%d.%d", v.major, v.minor)
}

// less reports whether v is an older line than o.
func (v version) less(o version) bool {
	if v.major != o.major {
		return v.major < o.major
	}
	return v.minor < o.minor
}

// surfaces holds the embedded exported-struct surface for each known line.
// gensurface produces these JSON files; they are embedded in embed.go and
// referenced here so composition can diff any src/dst pair.
//
//nolint:gochecknoglobals // const-ish surface registry, immutable after init
var surfaces = map[version][]byte{
	{major: 2}:           surfaceV2JSON,
	{major: 3}:           surfaceV3JSON,
	{major: 4}:           surfaceV4JSON,
	{major: 5}:           surfaceV5JSON,
	{major: 5, minor: 2}: surfaceV5_2JSON,
}

// pluginSurfaces holds the embedded plugin-package struct surface for the lines
// that have one. decodeSurface appends it to the line's surface.
//
//nolint:gochecknoglobals // const-ish surface registry, immutable after init
var pluginSurfaces = map[version][]byte{
	{major: 4}:           surfaceV4PluginsJSON,
	{major: 5}:           surfaceV5PluginsJSON,
	{major: 5, minor: 2}: surfaceV5_2PluginsJSON,
}

// lines returns every line with a surface, oldest first. It reads surfaces on
// every call rather than once, because tests swap the registry.
func lines() []version {
	out := make([]version, 0, len(surfaces))
	for v := range surfaces {
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b version) int {
		return cmp.Or(cmp.Compare(a.major, b.major), cmp.Compare(a.minor, b.minor))
	})
	return out
}

// lineResolver resolves a major to one of its release lines, reporting false
// when the major has none.
type lineResolver func(major) (version, bool)

// lineOf returns the line a release of major m with the given minor belongs to:
// the newest line of m that starts at or before minor. It reports false when m
// has no line.
func lineOf(m major, minor int) (version, bool) {
	var found version
	ok := false
	for _, v := range lines() {
		if v.major == m && v.minor <= minor {
			found, ok = v, true
		}
	}
	return found, ok
}

// firstLineOf returns the first line of major m, and false when m has none.
func firstLineOf(m major) (version, bool) {
	return lineOf(m, 0)
}

// newestLineOf returns the newest line of major m, and false when m has none.
func newestLineOf(m major) (version, bool) {
	return lineOf(m, math.MaxInt)
}

// noSurfaceError reports that no embedded surface covers the version label.
func noSurfaceError(label string) error {
	return fmt.Errorf("no embedded surface for %q", label)
}

// nextLineOf returns the line of the same major that follows v, and false when v
// is its major's newest line.
func nextLineOf(v version) (version, bool) {
	for _, w := range lines() {
		if w.major == v.major && v.less(w) {
			return w, true
		}
	}
	return version{}, false
}

// methodRegroup rewrites a source client call path to its target sub-client
// path. Match is on the resolved method selector; the linter confirms the
// receiver is an opensearchapi client type before rewriting. A same-path entry
// with Arg other than argSame doubles as an argument-adjusting rule for methods
// that stayed put but changed how they take the request.
type methodRegroup struct {
	FromPath []string // e.g. ["Indices","Count"] matched as client.Indices.Count
	ToPath   []string // e.g. ["Count"]           -> client.Count
	Arg      argShape // how the target method takes the request argument
}

// argShape is how a regrouped method's request argument changes between the
// source and target signatures.
type argShape int

const (
	argSame  argShape = iota // argument passes through unchanged
	argAddr                  // target takes *Req and the source took Req: wrap the argument in &
	argValue                 // target takes Req and the source took *Req: pass the value
)

// hop is one adjacent transition (From -> the next line): the hand-authored data
// that cannot be auto-derived from the surfaces alone.
//
//   - TypeRenames:       structs with exported fields whose NAME changed across the
//     hop (surface diffing handles same-name survivors automatically; this is
//     only for renames).
//   - SymbolRenames:     package-level constants, functions, non-struct types and
//     structs with no exported field (such as unions) whose NAME changed across
//     the hop. Each pair is checked equivalent against the surfaces (see
//     apirev.EquivalentSymbol).
//     The rewriter reads them through the same rename index as TypeRenames.
//   - FieldDispositions: rulings for struct fields that vanish on the target -
//     rename, remove, or manual, matched by (source pkg + type + field). A
//     closed, discrete table; a vanished field with no ruling is "unclassified"
//     and fails loudly if used (see apirev.FieldDisposition).
//   - MethodRegroups:    client call-site moves onto new sub-client paths.
//   - RemovedHelpers:    package-level opensearchapi or root-package helpers
//     removed across the hop, mapped to a linter action (helperNativeNew or
//     apirev.KindManual).
//   - SemanticFollowups: behavioral changes that cannot be mechanically
//     rewritten, reported to the operator after a rewrite.
type hop struct {
	From, To          version
	TypeRenames       []apirev.TypeRename
	SymbolRenames     []apirev.TypeRename
	FieldDispositions []apirev.FieldDisposition
	MethodRegroups    []methodRegroup
	RemovedHelpers    map[string]string
	SemanticFollowups []string
}

// helperNativeNew is the RemovedHelpers action that rewrites a call Helper(x)
// to the native new(x).
const helperNativeNew = "nativeNew"

// hops is the registry of adjacent transitions, keyed by source line; the
// composition linter chains whatever is present.
//
//nolint:gochecknoglobals // const-ish transition registry, immutable after init
var hops = map[version]hop{
	{major: 2}: hopV2toV3,
	{major: 3}: hopV3toV4,
	{major: 4}: hopV4toV5,
	{major: 5}: hopV5toV5_2,
}
