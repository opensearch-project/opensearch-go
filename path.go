// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package opensearch

import (
	"errors"
	"fmt"
	"strings"
)

const pathSep = "/"

// ErrPathRequired is returned by a path's Build method when a required segment
// is empty.
var ErrPathRequired = errors.New("opensearch: required path segment is empty")

// MustBuild is a helper that wraps a Build call and panics on error. Use it
// in call sites where the path segments are known to be populated.
//
//	return opensearch.BuildRequest(
//	    http.MethodPut,
//	    opensearch.MustBuild(opensearch.AliasPath{...}.Build()),
//	    body, params, header,
//	)
func MustBuild(path string, err error) string {
	if err != nil {
		panic(err)
	}
	return path
}

// ---------------------------------------------------------------------------
// Typed path components
// ---------------------------------------------------------------------------

// Index is a single OpenSearch index name.
type Index string

// Indices is a list of OpenSearch index names.
type Indices []Index

// Action is an API action segment (e.g. "_open", "_close", "_search").
type Action string

// DocumentID is a single OpenSearch document identifier.
type DocumentID string

// Alias is an OpenSearch index alias name.
type Alias string

// Repo is an OpenSearch snapshot repository name.
type Repo string

// Snapshot is an OpenSearch snapshot name.
type Snapshot string

// NodeID is an OpenSearch node identifier.
type NodeID string

// Plugin is an OpenSearch plugin name (e.g. "_security", "_ism").
type Plugin string

// Policy is an OpenSearch ISM policy name.
type Policy string

// Block is an index block type (e.g. "write", "read", "metadata").
type Block string

// Prefix is a path prefix segment.
type Prefix string

// Suffix is a path suffix segment.
type Suffix string

// Name is a named resource identifier (e.g. template name, pipeline ID).
type Name string

// Resource is a plugin resource type (e.g. "roles", "internalusers").
type Resource string

// Attr is a cluster attribute name (e.g. decommission awareness attribute).
type Attr string

// Value is a cluster attribute value.
type Value string

// Metric is a node metric name.
type Metric string

// IndexMetric is a node index-level metric name.
type IndexMetric string

// Metrics is a cluster state metrics filter.
type Metrics string

// NodeFilter is a cluster stats node filter expression.
type NodeFilter string

// ToIndices converts a plain []string to the typed [Indices] slice.
func ToIndices(ss []string) Indices {
	ii := make(Indices, len(ss))
	for i, s := range ss {
		ii[i] = Index(s)
	}
	return ii
}

// Join returns the comma-separated index names, or "" if empty.
func (ii Indices) Join() string {
	switch len(ii) {
	case 0:
		return ""
	case 1:
		return string(ii[0])
	}
	var b strings.Builder
	b.Grow(ii.joinLen())
	ii.join(&b)
	return b.String()
}

// joinLen returns the byte length of the comma-joined result.
func (ii Indices) joinLen() int {
	if len(ii) == 0 {
		return 0
	}
	n := len(ii) - 1 // commas
	for _, idx := range ii {
		n += len(idx)
	}
	return n
}

// optSegLen returns the path contribution of the optional indices segment:
// len("/") + joinLen when non-empty, 0 otherwise. Used in the pre-compute
// phase to size the strings.Builder before writing.
func (ii Indices) optSegLen() int {
	jl := ii.joinLen()
	if jl == 0 {
		return 0
	}
	return len(pathSep) + jl
}

// optSegWrite writes "/idx1,idx2" into b during the copy phase. No-op when
// the Indices slice is empty, matching the zero returned by optSegLen.
func (ii Indices) optSegWrite(b *strings.Builder) {
	if len(ii) == 0 {
		return
	}
	b.WriteString(pathSep)
	ii.join(b)
}

// join writes the comma-separated names into b without a leading slash.
func (ii Indices) join(b *strings.Builder) {
	for i, idx := range ii {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(string(idx))
	}
}

// ---------------------------------------------------------------------------
// Internal helpers for scalar path segments
//
// Path strings are built in two phases:
//   1. Pre-compute: sum the byte length of every segment using reqSegLen /
//      optSegLen, then Grow the strings.Builder once to avoid reallocation.
//   2. Copy: write each segment into the builder using reqSegWrite /
//      optSegWrite, which prepend the "/" separator before the value.
//
// "req" helpers always contribute their segment.
// "opt" helpers are no-ops (and return 0) when the value is empty.
// ---------------------------------------------------------------------------

// reqSegWrite writes "/{value}" into b during the copy phase. The caller must
// have accounted for this segment with reqSegLen during the pre-compute phase.
func reqSegWrite(b *strings.Builder, value string) {
	b.WriteString(pathSep)
	b.WriteString(value)
}

// reqSegLen returns the number of bytes "/{value}" contributes to the path
// during the pre-compute phase: len("/") + len(value).
func reqSegLen(v string) int { return len(pathSep) + len(v) }

// optSegLen returns reqSegLen(v) when v is non-empty, 0 otherwise. Used in
// the pre-compute phase for optional path segments.
func optSegLen(v string) int {
	if v == "" {
		return 0
	}
	return reqSegLen(v)
}

// optSegWrite writes "/{value}" into b only when value is non-empty. The
// caller must have used optSegLen during the pre-compute phase so the builder
// capacity is correct regardless of whether the segment is present.
func optSegWrite(b *strings.Builder, value string) {
	if value != "" {
		reqSegWrite(b, value)
	}
}

// ---------------------------------------------------------------------------
// IndexPath — /{index}
// Used by: IndicesCreate
// ---------------------------------------------------------------------------

// IndexPath builds /{index}. Index is required.
type IndexPath struct {
	Index Index
}

// Build returns the path or [ErrPathRequired] when Index is empty.
func (p IndexPath) Build() (string, error) {
	if p.Index == "" {
		return "", fmt.Errorf("IndexPath.Index: %w", ErrPathRequired)
	}
	var b strings.Builder
	b.Grow(reqSegLen(string(p.Index)))
	reqSegWrite(&b, string(p.Index))
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// IndexActionPath — /{index}/{action}
// Used by: IndicesOpen (/_open), IndicesClose (/_close)
// ---------------------------------------------------------------------------

// IndexActionPath builds /{index}/{action}. Both fields are required.
type IndexActionPath struct {
	Index  Index
	Action Action
}

// Build returns the path or [ErrPathRequired] when Index or Action is empty.
func (p IndexActionPath) Build() (string, error) {
	if p.Index == "" || p.Action == "" {
		return "", fmt.Errorf("IndexActionPath.Index or .Action: %w", ErrPathRequired)
	}
	var b strings.Builder
	b.Grow(reqSegLen(string(p.Index)) + reqSegLen(string(p.Action)))
	reqSegWrite(&b, string(p.Index))
	reqSegWrite(&b, string(p.Action))
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// DocumentPath — /{index}/{action}/{documentID}
// Used by: Index (_doc), Create (_create), Get, Delete, Exists, Source,
//
//	ExistsSource, Update (_update), Explain (_explain)
//
// ---------------------------------------------------------------------------

// DocumentPath builds /{index}/{action}/{documentID}. All fields are required.
type DocumentPath struct {
	Index      Index
	Action     Action
	DocumentID DocumentID
}

// Build returns the path or [ErrPathRequired] when any field is empty.
func (p DocumentPath) Build() (string, error) {
	if p.Index == "" || p.Action == "" || p.DocumentID == "" {
		return "", fmt.Errorf("DocumentPath.Index, .Action, or .DocumentID: %w", ErrPathRequired)
	}
	var b strings.Builder
	b.Grow(reqSegLen(string(p.Index)) + reqSegLen(string(p.Action)) + reqSegLen(string(p.DocumentID)))
	reqSegWrite(&b, string(p.Index))
	reqSegWrite(&b, string(p.Action))
	reqSegWrite(&b, string(p.DocumentID))
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// IndexTargetPath — /{index}/{action}/{target}
// Used by: IndicesClone, IndicesShrink, IndicesSplit
// ---------------------------------------------------------------------------

// IndexTargetPath builds /{index}/{action}/{target}. All fields are required.
type IndexTargetPath struct {
	Index  Index
	Action Action
	Target Index
}

// Build returns the path or [ErrPathRequired] when any field is empty.
func (p IndexTargetPath) Build() (string, error) {
	if p.Index == "" || p.Action == "" || p.Target == "" {
		return "", fmt.Errorf("IndexTargetPath.Index, .Action, or .Target: %w", ErrPathRequired)
	}
	var b strings.Builder
	b.Grow(reqSegLen(string(p.Index)) + reqSegLen(string(p.Action)) + reqSegLen(string(p.Target)))
	reqSegWrite(&b, string(p.Index))
	reqSegWrite(&b, string(p.Action))
	reqSegWrite(&b, string(p.Target))
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// IndicesActionPath — /{indices}/{action}
// Used by: UpdateByQuery, DeleteByQuery, MappingPut, SettingsPut
// ---------------------------------------------------------------------------

// IndicesActionPath builds /{indices}/{action}. Both fields are required.
type IndicesActionPath struct {
	Indices Indices
	Action  Action
}

// Build returns the path or [ErrPathRequired] when Indices is empty or Action
// is empty.
func (p IndicesActionPath) Build() (string, error) {
	if len(p.Indices) == 0 || p.Action == "" {
		return "", fmt.Errorf("IndicesActionPath.Indices or .Action: %w", ErrPathRequired)
	}
	var b strings.Builder
	b.Grow(p.Indices.optSegLen() + reqSegLen(string(p.Action)))
	p.Indices.optSegWrite(&b)
	reqSegWrite(&b, string(p.Action))
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// IndicesBlockPath — /{indices}/_block/{block}
// Used by: IndicesBlock
// ---------------------------------------------------------------------------

// IndicesBlockPath builds /{indices}/_block/{block}. All fields are required.
type IndicesBlockPath struct {
	Indices Indices
	Block   Block
}

// Build returns the path or [ErrPathRequired] when any field is empty.
func (p IndicesBlockPath) Build() (string, error) {
	if len(p.Indices) == 0 || p.Block == "" {
		return "", fmt.Errorf("IndicesBlockPath.Indices or .Block: %w", ErrPathRequired)
	}
	const action = "_block"
	var b strings.Builder
	b.Grow(p.Indices.optSegLen() + reqSegLen(action) + reqSegLen(string(p.Block)))
	p.Indices.optSegWrite(&b)
	reqSegWrite(&b, action)
	reqSegWrite(&b, string(p.Block))
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// AliasPath — /{?indices}/_alias/{?alias}
// Used by: AliasPut, AliasDelete, AliasGet, AliasExists
// ---------------------------------------------------------------------------

// AliasPath builds /{?indices}/_alias/{?alias}. Both Indices and Alias are
// optional.
type AliasPath struct {
	Indices Indices
	Alias   Alias
}

// Build returns the path.
func (p AliasPath) Build() (string, error) { //nolint:unparam // error kept for interface consistency
	const action = "_alias"
	var b strings.Builder
	alias := string(p.Alias)
	b.Grow(p.Indices.optSegLen() + reqSegLen(action) + optSegLen(alias))
	p.Indices.optSegWrite(&b)
	reqSegWrite(&b, action)
	optSegWrite(&b, alias)
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// ResourcePath — /{prefix}/{name}
// Used by: TemplateCreate/Delete/Exists, IndexTemplateCreate/Delete/Exists,
//
//	ComponentTemplateCreate/Delete/Exists, ScriptGet/Delete,
//	SnapshotRepositoryCreate, TasksGet, DataStreamCreate/Delete,
//	DanglingImport/Delete, CatSnapshots
//
// ---------------------------------------------------------------------------

// ResourcePath builds /{prefix}/{name}. Both fields are required.
type ResourcePath struct {
	Prefix Prefix
	Name   Name
}

// Build returns the path or [ErrPathRequired] when either field is empty.
func (p ResourcePath) Build() (string, error) {
	if p.Prefix == "" || p.Name == "" {
		return "", fmt.Errorf("ResourcePath.Prefix or .Name: %w", ErrPathRequired)
	}
	var b strings.Builder
	b.Grow(reqSegLen(string(p.Prefix)) + reqSegLen(string(p.Name)))
	reqSegWrite(&b, string(p.Prefix))
	reqSegWrite(&b, string(p.Name))
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// ResourceActionPath — /{prefix}/{name}/{action}
// Used by: SnapshotRepositoryVerify (/_verify), SnapshotRepositoryCleanup
//
//	(/_cleanup), IndexTemplateSimulate, IngestCreate/Delete,
//	Rethrottle operations, IndicesResolve
//
// ---------------------------------------------------------------------------

// ResourceActionPath builds /{prefix}/{name}/{action}. All fields are required.
type ResourceActionPath struct {
	Prefix Prefix
	Name   Name
	Action Action
}

// Build returns the path or [ErrPathRequired] when any field is empty.
func (p ResourceActionPath) Build() (string, error) {
	if p.Prefix == "" || p.Name == "" || p.Action == "" {
		return "", fmt.Errorf("ResourceActionPath.Prefix, .Name, or .Action: %w", ErrPathRequired)
	}
	var b strings.Builder
	b.Grow(reqSegLen(string(p.Prefix)) + reqSegLen(string(p.Name)) + reqSegLen(string(p.Action)))
	reqSegWrite(&b, string(p.Prefix))
	reqSegWrite(&b, string(p.Name))
	reqSegWrite(&b, string(p.Action))
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// SnapshotPath — /_snapshot/{repo}/{snapshot}
// Used by: SnapshotCreate, SnapshotGet, SnapshotDelete
// ---------------------------------------------------------------------------

// SnapshotPath builds /_snapshot/{repo}/{snapshot}. Both fields are required.
type SnapshotPath struct {
	Repo     Repo
	Snapshot Snapshot
}

// Build returns the path or [ErrPathRequired] when Repo or Snapshot is empty.
func (p SnapshotPath) Build() (string, error) {
	if p.Repo == "" || p.Snapshot == "" {
		return "", fmt.Errorf("SnapshotPath.Repo or .Snapshot: %w", ErrPathRequired)
	}
	const prefix = "_snapshot"
	var b strings.Builder
	b.Grow(reqSegLen(prefix) + reqSegLen(string(p.Repo)) + reqSegLen(string(p.Snapshot)))
	reqSegWrite(&b, prefix)
	reqSegWrite(&b, string(p.Repo))
	reqSegWrite(&b, string(p.Snapshot))
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// SnapshotActionPath — /_snapshot/{repo}/{snapshot}/{action}
// Used by: SnapshotRestore (/_restore), SnapshotStatus (/_status)
// ---------------------------------------------------------------------------

// SnapshotActionPath builds /_snapshot/{repo}/{snapshot}/{action}. All fields
// are required.
type SnapshotActionPath struct {
	Repo     Repo
	Snapshot Snapshot
	Action   Action
}

// Build returns the path or [ErrPathRequired] when any field is empty.
func (p SnapshotActionPath) Build() (string, error) {
	if p.Repo == "" || p.Snapshot == "" || p.Action == "" {
		return "", fmt.Errorf("SnapshotActionPath.Repo, .Snapshot, or .Action: %w", ErrPathRequired)
	}
	const prefix = "_snapshot"
	var b strings.Builder
	b.Grow(reqSegLen(prefix) + reqSegLen(string(p.Repo)) + reqSegLen(string(p.Snapshot)) + reqSegLen(string(p.Action)))
	reqSegWrite(&b, prefix)
	reqSegWrite(&b, string(p.Repo))
	reqSegWrite(&b, string(p.Snapshot))
	reqSegWrite(&b, string(p.Action))
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// SnapshotClonePath — /_snapshot/{repo}/{snapshot}/_clone/{target}
// Used by: SnapshotClone
// ---------------------------------------------------------------------------

// SnapshotClonePath builds /_snapshot/{repo}/{snapshot}/_clone/{target}. All
// fields are required.
type SnapshotClonePath struct {
	Repo           Repo
	Snapshot       Snapshot
	TargetSnapshot Snapshot
}

// Build returns the path or [ErrPathRequired] when any field is empty.
func (p SnapshotClonePath) Build() (string, error) {
	if p.Repo == "" || p.Snapshot == "" || p.TargetSnapshot == "" {
		return "", fmt.Errorf("SnapshotClonePath.Repo, .Snapshot, or .TargetSnapshot: %w", ErrPathRequired)
	}
	const prefix = "_snapshot"
	const action = "_clone"
	var b strings.Builder
	b.Grow(reqSegLen(prefix) + reqSegLen(string(p.Repo)) + reqSegLen(string(p.Snapshot)) +
		reqSegLen(action) + reqSegLen(string(p.TargetSnapshot)))
	reqSegWrite(&b, prefix)
	reqSegWrite(&b, string(p.Repo))
	reqSegWrite(&b, string(p.Snapshot))
	reqSegWrite(&b, action)
	reqSegWrite(&b, string(p.TargetSnapshot))
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// PluginResourcePath — /_plugins/{plugin}/api/{resource}/{name}
// Used by: Security plugin (roles, rolesmapping, internalusers, actiongroups,
//
//	tenants, nodesdn) — Put and Delete methods
//
// ---------------------------------------------------------------------------

// PluginResourcePath builds /_plugins/{plugin}/api/{resource}/{name}. All
// fields are required.
type PluginResourcePath struct {
	Plugin   Plugin
	Resource Resource
	Name     Name
}

// Build returns the path or [ErrPathRequired] when any field is empty.
func (p PluginResourcePath) Build() (string, error) {
	if p.Plugin == "" || p.Resource == "" || p.Name == "" {
		return "", fmt.Errorf("PluginResourcePath.Plugin, .Resource, or .Name: %w", ErrPathRequired)
	}
	const prefix = "_plugins"
	const api = "api"
	var b strings.Builder
	b.Grow(reqSegLen(prefix) + reqSegLen(string(p.Plugin)) + reqSegLen(api) + reqSegLen(string(p.Resource)) + reqSegLen(string(p.Name)))
	reqSegWrite(&b, prefix)
	reqSegWrite(&b, string(p.Plugin))
	reqSegWrite(&b, api)
	reqSegWrite(&b, string(p.Resource))
	reqSegWrite(&b, string(p.Name))
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// PluginIndexPath — /_plugins/{plugin}/{action}/{?indices}
// Used by: ISM plugin (explain, add, remove, retry, change_policy,
//
//	refresh_search_analyzers)
//
// ---------------------------------------------------------------------------

// PluginIndexPath builds /_plugins/{plugin}/{action} with an optional /{indices}
// suffix. Plugin and Action are required; Indices is optional.
type PluginIndexPath struct {
	Plugin  Plugin
	Action  Action
	Indices Indices
}

// Build returns the path or [ErrPathRequired] when Plugin or Action is empty.
func (p PluginIndexPath) Build() (string, error) {
	if p.Plugin == "" || p.Action == "" {
		return "", fmt.Errorf("PluginIndexPath.Plugin or .Action: %w", ErrPathRequired)
	}
	const prefix = "_plugins"
	var b strings.Builder
	b.Grow(reqSegLen(prefix) + reqSegLen(string(p.Plugin)) + reqSegLen(string(p.Action)) + p.Indices.optSegLen())
	reqSegWrite(&b, prefix)
	reqSegWrite(&b, string(p.Plugin))
	reqSegWrite(&b, string(p.Action))
	p.Indices.optSegWrite(&b)
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// PluginPolicyPath — /_plugins/{plugin}/policies/{policy}
// Used by: ISM PoliciesPut, PoliciesDelete
// ---------------------------------------------------------------------------

// PluginPolicyPath builds /_plugins/{plugin}/policies/{policy}. All fields are
// required.
type PluginPolicyPath struct {
	Plugin Plugin
	Policy Policy
}

// Build returns the path or [ErrPathRequired] when either field is empty.
func (p PluginPolicyPath) Build() (string, error) {
	if p.Plugin == "" || p.Policy == "" {
		return "", fmt.Errorf("PluginPolicyPath.Plugin or .Policy: %w", ErrPathRequired)
	}
	const prefix = "_plugins"
	const policies = "policies"
	var b strings.Builder
	b.Grow(reqSegLen(prefix) + reqSegLen(string(p.Plugin)) + reqSegLen(policies) + reqSegLen(string(p.Policy)))
	reqSegWrite(&b, prefix)
	reqSegWrite(&b, string(p.Plugin))
	reqSegWrite(&b, policies)
	reqSegWrite(&b, string(p.Policy))
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// DecommissionPath — /_cluster/decommission/awareness/{attr}/{value}
// Used by: ClusterPutDecommission, ClusterGetDecommission
// ---------------------------------------------------------------------------

// DecommissionPath builds /_cluster/decommission/awareness/{attr}/{value}. Both
// fields are required.
type DecommissionPath struct {
	Attr  Attr
	Value Value
}

// Build returns the path or [ErrPathRequired] when Attr or Value is empty.
func (p DecommissionPath) Build() (string, error) {
	if p.Attr == "" || p.Value == "" {
		return "", fmt.Errorf("DecommissionPath.Attr or .Value: %w", ErrPathRequired)
	}
	const prefix = "_cluster/decommission/awareness"
	var b strings.Builder
	b.Grow(reqSegLen(prefix) + reqSegLen(string(p.Attr)) + reqSegLen(string(p.Value)))
	reqSegWrite(&b, prefix)
	reqSegWrite(&b, string(p.Attr))
	reqSegWrite(&b, string(p.Value))
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// PrefixActionPath — /{?prefix}/{action}
// Used by: Bulk (opt index + /_bulk), ClusterHealth, many cat APIs, search
//
//	APIs, etc.
//
// ---------------------------------------------------------------------------

// PrefixActionPath builds an optional /{prefix} followed by a required
// /{action}. Prefix is optional.
type PrefixActionPath struct {
	Prefix Prefix
	Action Action
}

// Build returns the path or [ErrPathRequired] when Action is empty.
func (p PrefixActionPath) Build() (string, error) {
	if p.Action == "" {
		return "", fmt.Errorf("PrefixActionPath.Action: %w", ErrPathRequired)
	}
	pfx := string(p.Prefix)
	var b strings.Builder
	b.Grow(optSegLen(pfx) + reqSegLen(string(p.Action)))
	optSegWrite(&b, pfx)
	reqSegWrite(&b, string(p.Action))
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// PrefixActionSuffixPath — /{?prefix}/{action}/{?suffix}
// Used by: SettingsGet, MappingGet, CatAliases, NodesInfo, etc.
// ---------------------------------------------------------------------------

// PrefixActionSuffixPath builds /{?prefix}/{action}/{?suffix}. Action is
// required; Prefix and Suffix are optional.
type PrefixActionSuffixPath struct {
	Prefix Prefix
	Action Action
	Suffix Suffix
}

// Build returns the path or [ErrPathRequired] when Action is empty.
func (p PrefixActionSuffixPath) Build() (string, error) {
	if p.Action == "" {
		return "", fmt.Errorf("PrefixActionSuffixPath.Action: %w", ErrPathRequired)
	}
	pfx := string(p.Prefix)
	sfx := string(p.Suffix)
	var b strings.Builder
	b.Grow(optSegLen(pfx) + reqSegLen(string(p.Action)) + optSegLen(sfx))
	optSegWrite(&b, pfx)
	reqSegWrite(&b, string(p.Action))
	optSegWrite(&b, sfx)
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// ActionSuffixPath — /{action}/{?suffix}
// Used by: ScrollDelete, PluginPoliciesGet, PluginSecurityGet/Patch
// ---------------------------------------------------------------------------

// ActionSuffixPath builds /{action}/{?suffix}. Action is required; Suffix is
// optional.
type ActionSuffixPath struct {
	Action Action
	Suffix Suffix
}

// Build returns the path or [ErrPathRequired] when Action is empty.
func (p ActionSuffixPath) Build() (string, error) {
	if p.Action == "" {
		return "", fmt.Errorf("ActionSuffixPath.Action: %w", ErrPathRequired)
	}
	sfx := string(p.Suffix)
	var b strings.Builder
	b.Grow(reqSegLen(string(p.Action)) + optSegLen(sfx))
	reqSegWrite(&b, string(p.Action))
	optSegWrite(&b, sfx)
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// PrefixSuffixActionPath — /{prefix}/{?suffix}/{action}
// Used by: TasksCancel, IngestSimulate, DataStreamStats
// ---------------------------------------------------------------------------

// PrefixSuffixActionPath builds /{prefix}/{?suffix}/{action}. Prefix and
// Action are required; Suffix is optional.
type PrefixSuffixActionPath struct {
	Prefix Prefix
	Suffix Suffix
	Action Action
}

// Build returns the path or [ErrPathRequired] when Prefix or Action is empty.
func (p PrefixSuffixActionPath) Build() (string, error) {
	if p.Prefix == "" || p.Action == "" {
		return "", fmt.Errorf("PrefixSuffixActionPath.Prefix or .Action: %w", ErrPathRequired)
	}
	sfx := string(p.Suffix)
	var b strings.Builder
	b.Grow(reqSegLen(string(p.Prefix)) + optSegLen(sfx) + reqSegLen(string(p.Action)))
	reqSegWrite(&b, string(p.Prefix))
	optSegWrite(&b, sfx)
	reqSegWrite(&b, string(p.Action))
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// RolloverPath — /{alias}/_rollover/{?index}
// Used by: IndicesRollover
// ---------------------------------------------------------------------------

// RolloverPath builds /{alias}/_rollover with an optional /{index} suffix.
// Alias is required.
type RolloverPath struct {
	Alias Alias
	Index Index
}

// Build returns the path or [ErrPathRequired] when Alias is empty.
func (p RolloverPath) Build() (string, error) {
	if p.Alias == "" {
		return "", fmt.Errorf("RolloverPath.Alias: %w", ErrPathRequired)
	}
	const action = "_rollover"
	alias := string(p.Alias)
	idx := string(p.Index)
	var b strings.Builder
	b.Grow(reqSegLen(alias) + reqSegLen(action) + optSegLen(idx))
	reqSegWrite(&b, alias)
	reqSegWrite(&b, action)
	optSegWrite(&b, idx)
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// TermvectorsPath — /{?index}/_termvectors/{?documentID}
// Used by: Termvectors, MTermvectors
// ---------------------------------------------------------------------------

// TermvectorsPath builds an optional /{index} followed by /_termvectors and an
// optional /{documentID}.
type TermvectorsPath struct {
	Index      Index
	DocumentID DocumentID
}

// Build returns the path.
func (p TermvectorsPath) Build() (string, error) { //nolint:unparam // error kept for interface consistency
	const action = "_termvectors"
	idx := string(p.Index)
	docID := string(p.DocumentID)
	var b strings.Builder
	b.Grow(optSegLen(idx) + reqSegLen(action) + optSegLen(docID))
	optSegWrite(&b, idx)
	reqSegWrite(&b, action)
	optSegWrite(&b, docID)
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// NodesPath — /_nodes/{?nodeID}/{action}/{?metric}/{?indexMetric}
// Used by: NodesInfo, NodesStats, NodesUsage, NodesHotThreads,
//
//	NodesReloadSecuritySettings
//
// ---------------------------------------------------------------------------

// NodesPath builds /_nodes with optional NodeID, required Action, and optional
// Metric/IndexMetric suffixes.
type NodesPath struct {
	NodeID      NodeID
	Action      Action
	Metric      Metric
	IndexMetric IndexMetric
}

// Build returns the path or [ErrPathRequired] when Action is empty.
func (p NodesPath) Build() (string, error) {
	if p.Action == "" {
		return "", fmt.Errorf("NodesPath.Action: %w", ErrPathRequired)
	}
	const prefix = "_nodes"
	nodeID := string(p.NodeID)
	metric := string(p.Metric)
	idxMetric := string(p.IndexMetric)
	var b strings.Builder
	b.Grow(reqSegLen(prefix) + optSegLen(nodeID) + reqSegLen(string(p.Action)) + optSegLen(metric) + optSegLen(idxMetric))
	reqSegWrite(&b, prefix)
	optSegWrite(&b, nodeID)
	reqSegWrite(&b, string(p.Action))
	optSegWrite(&b, metric)
	optSegWrite(&b, idxMetric)
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// ClusterStatePath — /_cluster/state/{?metrics}/{?indices}
// Used by: ClusterState
// ---------------------------------------------------------------------------

// ClusterStatePath builds /_cluster/state with optional /{metrics} and
// /{indices} suffixes.
type ClusterStatePath struct {
	Metrics Metrics
	Indices Indices
}

// Build returns the path.
func (p ClusterStatePath) Build() (string, error) { //nolint:unparam // error kept for interface consistency
	const prefix = "_cluster/state"
	metrics := string(p.Metrics)
	var b strings.Builder
	b.Grow(reqSegLen(prefix) + optSegLen(metrics) + p.Indices.optSegLen())
	reqSegWrite(&b, prefix)
	optSegWrite(&b, metrics)
	p.Indices.optSegWrite(&b)
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// ClusterStatsPath — /_cluster/stats/nodes/{?nodeFilter}
// Used by: ClusterStats
// ---------------------------------------------------------------------------

// ClusterStatsPath builds /_cluster/stats with an optional /nodes/{filter}.
type ClusterStatsPath struct {
	NodeFilter NodeFilter
}

// Build returns the path.
func (p ClusterStatsPath) Build() (string, error) { //nolint:unparam // error kept for interface consistency
	const prefix = "_cluster/stats"
	const nodes = "nodes"
	nf := string(p.NodeFilter)
	var b strings.Builder
	if nf == "" {
		b.Grow(reqSegLen(prefix))
		reqSegWrite(&b, prefix)
	} else {
		b.Grow(reqSegLen(prefix) + reqSegLen(nodes) + reqSegLen(nf))
		reqSegWrite(&b, prefix)
		reqSegWrite(&b, nodes)
		reqSegWrite(&b, nf)
	}
	return b.String(), nil
}
