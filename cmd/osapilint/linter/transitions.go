// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import "github.com/opensearch-project/opensearch-go/v5/cmd/osapilint/internal/apirev"

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

// surfaces holds the embedded exported-struct surface for each known version,
// keyed by major. gensurface produces these JSON files; they are embedded in
// embed.go and referenced here so composition can diff any src/dst pair.
//
//nolint:gochecknoglobals // const-ish surface registry, immutable after init
var surfaces = map[major][]byte{
	2: surfaceV2JSON,
	3: surfaceV3JSON,
	4: surfaceV4JSON,
	5: surfaceV5JSON,
}

// methodRegroup rewrites a source client call path to its target sub-client
// path. Match is on the resolved method selector; the linter confirms the
// receiver is an opensearchapi client type before rewriting. A same-path entry
// with PtrArg set doubles as a "wrap the arg in &" rule for methods that stayed
// put but began taking *Req.
type methodRegroup struct {
	FromPath []string // e.g. ["Indices","Count"] matched as client.Indices.Count
	ToPath   []string // e.g. ["Count"]           -> client.Count
	PtrArg   bool     // target method takes *Req: wrap the sole request argument in &
}

// hop is one adjacent transition (From -> From+1): the hand-authored data that
// cannot be auto-derived from the surfaces alone.
//
//   - TypeRenames:       types whose NAME changed across the hop (surface diffing
//     handles same-name survivors automatically; this is only for renames).
//   - FieldDispositions: rulings for struct fields that vanish on the target -
//     rename, remove, or manual, matched by (source pkg + type + field). A
//     closed, discrete table; a vanished field with no ruling is "unclassified"
//     and fails loudly if used (see apirev.FieldDisposition).
//   - MethodRegroups:    client call-site moves onto new sub-client paths.
//   - RemovedHelpers:    package-level opensearchapi helpers removed across the
//     hop, mapped to an linter action ("addressOf" or "manual").
//   - SemanticFollowups: behavioral changes that cannot be mechanically
//     rewritten, reported to the operator after a rewrite.
type hop struct {
	From, To          major
	TypeRenames       []apirev.TypeRename
	FieldDispositions []apirev.FieldDisposition
	MethodRegroups    []methodRegroup
	RemovedHelpers    map[string]string
	SemanticFollowups []string
}

// hops is the registry of adjacent transitions, keyed by source major. A
// coworker completing the multi-version work appends v3->v4, v2->v3 entries here
// (and their surfaces above); the composition linter chains whatever is present.
//
//nolint:gochecknoglobals // const-ish transition registry, immutable after init
var hops = map[major]hop{
	2: hopV2toV3,
	3: hopV3toV4,
	4: hopV4toV5,
}
