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
	"sync"
)

const (
	pathSep  = "/"
	indexSep = ","
)

// ErrPathRequired is returned by a path's Build method when a required segment
// is empty.
var ErrPathRequired = errors.New("opensearch: required path segment is empty")

// ---------------------------------------------------------------------------
// Pooled path buffer
// ---------------------------------------------------------------------------

type pathBuf struct {
	buf []byte
}

//nolint:gochecknoglobals // sync.Pool must be package-level
var pathBufPool = sync.Pool{
	New: func() any {
		return &pathBuf{buf: make([]byte, 0, 256)}
	},
}

const maxPooledPathBuf = 4096

func acquirePathBuf() *pathBuf {
	pb := pathBufPool.Get().(*pathBuf)
	pb.buf = pb.buf[:0]
	return pb
}

func (pb *pathBuf) release() string {
	s := string(pb.buf)
	if cap(pb.buf) <= maxPooledPathBuf {
		pathBufPool.Put(pb)
	}
	return s
}

func (pb *pathBuf) writeReq(v string) {
	pb.buf = append(pb.buf, pathSep...)
	pb.buf = append(pb.buf, v...)
}

func (pb *pathBuf) writeOpt(v string) {
	if v != "" {
		pb.writeReq(v)
	}
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

// writePathBuf writes "/idx1,idx2" into pb. No-op when the Indices slice is empty.
func (ii Indices) writePathBuf(pb *pathBuf) {
	if len(ii) == 0 {
		return
	}
	pb.buf = append(pb.buf, pathSep...)
	for i, idx := range ii {
		if i > 0 {
			pb.buf = append(pb.buf, indexSep...)
		}
		pb.buf = append(pb.buf, string(idx)...)
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
	pb := acquirePathBuf()
	pb.writeReq(string(p.Index))
	return pb.release(), nil
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
	pb := acquirePathBuf()
	pb.writeReq(string(p.Index))
	pb.writeReq(string(p.Action))
	return pb.release(), nil
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
	pb := acquirePathBuf()
	pb.writeReq(string(p.Index))
	pb.writeReq(string(p.Action))
	pb.writeReq(string(p.DocumentID))
	return pb.release(), nil
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
	pb := acquirePathBuf()
	pb.writeReq(string(p.Index))
	pb.writeReq(string(p.Action))
	pb.writeReq(string(p.Target))
	return pb.release(), nil
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
	pb := acquirePathBuf()
	p.Indices.writePathBuf(pb)
	pb.writeReq(string(p.Action))
	return pb.release(), nil
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
	pb := acquirePathBuf()
	p.Indices.writePathBuf(pb)
	pb.writeReq(action)
	pb.writeReq(string(p.Block))
	return pb.release(), nil
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
	pb := acquirePathBuf()
	p.Indices.writePathBuf(pb)
	pb.writeReq(action)
	pb.writeOpt(string(p.Alias))
	return pb.release(), nil
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
	pb := acquirePathBuf()
	pb.writeReq(string(p.Prefix))
	pb.writeReq(string(p.Name))
	return pb.release(), nil
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
	pb := acquirePathBuf()
	pb.writeReq(string(p.Prefix))
	pb.writeReq(string(p.Name))
	pb.writeReq(string(p.Action))
	return pb.release(), nil
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
	pb := acquirePathBuf()
	pb.writeReq(prefix)
	pb.writeReq(string(p.Repo))
	pb.writeReq(string(p.Snapshot))
	return pb.release(), nil
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
	pb := acquirePathBuf()
	pb.writeReq(prefix)
	pb.writeReq(string(p.Repo))
	pb.writeReq(string(p.Snapshot))
	pb.writeReq(string(p.Action))
	return pb.release(), nil
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
	pb := acquirePathBuf()
	pb.writeReq(prefix)
	pb.writeReq(string(p.Repo))
	pb.writeReq(string(p.Snapshot))
	pb.writeReq(action)
	pb.writeReq(string(p.TargetSnapshot))
	return pb.release(), nil
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
	pb := acquirePathBuf()
	pb.writeReq(prefix)
	pb.writeReq(string(p.Plugin))
	pb.writeReq(api)
	pb.writeReq(string(p.Resource))
	pb.writeReq(string(p.Name))
	return pb.release(), nil
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
	pb := acquirePathBuf()
	pb.writeReq(prefix)
	pb.writeReq(string(p.Plugin))
	pb.writeReq(string(p.Action))
	p.Indices.writePathBuf(pb)
	return pb.release(), nil
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
	pb := acquirePathBuf()
	pb.writeReq(prefix)
	pb.writeReq(string(p.Plugin))
	pb.writeReq(policies)
	pb.writeReq(string(p.Policy))
	return pb.release(), nil
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
	pb := acquirePathBuf()
	pb.writeReq(prefix)
	pb.writeReq(string(p.Attr))
	pb.writeReq(string(p.Value))
	return pb.release(), nil
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
	pb := acquirePathBuf()
	pb.writeOpt(string(p.Prefix))
	pb.writeReq(string(p.Action))
	return pb.release(), nil
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
	pb := acquirePathBuf()
	pb.writeOpt(string(p.Prefix))
	pb.writeReq(string(p.Action))
	pb.writeOpt(string(p.Suffix))
	return pb.release(), nil
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
	pb := acquirePathBuf()
	pb.writeReq(string(p.Action))
	pb.writeOpt(string(p.Suffix))
	return pb.release(), nil
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
	pb := acquirePathBuf()
	pb.writeReq(string(p.Prefix))
	pb.writeOpt(string(p.Suffix))
	pb.writeReq(string(p.Action))
	return pb.release(), nil
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
	pb := acquirePathBuf()
	pb.writeReq(string(p.Alias))
	pb.writeReq(action)
	pb.writeOpt(string(p.Index))
	return pb.release(), nil
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
	pb := acquirePathBuf()
	pb.writeOpt(string(p.Index))
	pb.writeReq(action)
	pb.writeOpt(string(p.DocumentID))
	return pb.release(), nil
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
	pb := acquirePathBuf()
	pb.writeReq(prefix)
	pb.writeOpt(string(p.NodeID))
	pb.writeReq(string(p.Action))
	pb.writeOpt(string(p.Metric))
	pb.writeOpt(string(p.IndexMetric))
	return pb.release(), nil
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
	pb := acquirePathBuf()
	pb.writeReq(prefix)
	pb.writeOpt(string(p.Metrics))
	p.Indices.writePathBuf(pb)
	return pb.release(), nil
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
	pb := acquirePathBuf()
	pb.writeReq(prefix)
	nf := string(p.NodeFilter)
	if nf != "" {
		pb.writeReq(nodes)
		pb.writeReq(nf)
	}
	return pb.release(), nil
}
