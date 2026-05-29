// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/emit"
	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/errwrap"
	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/ir"
)

// errmaskImportPath is the absolute import path that PartialFailureFragment
// adds when it emits any wrapper helpers; DispatchFragment never adds it
// since the methods (and their errmask references) live in the partial-
// failure fragment.
const errmaskImportPath = "github.com/opensearch-project/opensearch-go/v4/internal/errmask"

func newRespType(prefix string, fields ...ir.Field) *ir.Type {
	return &ir.Type{
		Name:       prefix + "Resp",
		Scope:      ir.ScopeLocal,
		OwnerGroup: strings.ToLower(prefix),
		Fields:     fields,
	}
}

func bulkFixtureResp() *ir.Type {
	return newRespType("Bulk",
		ir.Field{GoName: "Errors", GoType: "bool"},
		ir.Field{GoName: "Items", GoType: "[]BulkItem"},
	)
}

func shardsFixtureResp(prefix string) *ir.Type {
	return newRespType(prefix, ir.Field{GoName: "Shards", GoType: "ShardStatistics"})
}

func msearchFixtureResp(reg *ir.TypeRegistry) *ir.Type {
	if _, ok := reg.LookupByName("MSearchResponseItem"); !ok {
		reg.Register(&ir.Type{
			Name:   "MSearchResponseItem",
			Scope:  ir.ScopeLocal,
			Fields: []ir.Field{{GoName: "Shards", GoType: "ShardStatistics"}},
		})
	}
	return newRespType("MSearch", ir.Field{GoName: "Responses", GoType: "[]MSearchResponseItem"})
}

func newRegistry() *ir.TypeRegistry {
	return ir.NewTypeRegistry("opensearchapi", "github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi")
}

// ---------------------------------------------------------------------------
// DispatchFragment: dispatch handler delegates to data.PartialFailures()
// ---------------------------------------------------------------------------

func TestDispatchFragment_Body(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		buildOp     func(reg *ir.TypeRegistry) *ir.Operation
		contains    []string
		notContains []string
	}{
		{
			name: "no wrappers falls through to nil",
			buildOp: func(_ *ir.TypeRegistry) *ir.Operation {
				return &ir.Operation{
					Group:       "cluster.health",
					TypePrefix:  "ClusterHealth",
					HTTPMethods: []string{http.MethodGet},
					PrimaryPath: "/_cluster/health",
					Response:    newRespType("ClusterHealth"),
					DispatchRoutes: []ir.DispatchRoute{
						{ReceiverType: "clusterClient", MethodName: "Health", TopLevel: false},
					},
				}
			},
			contains:    []string{"return &data, nil"},
			notContains: []string{"PartialFailures", "collapsePerOpErrors"},
		},
		{
			name: "single-wrapper op uses nil collapse closure",
			buildOp: func(_ *ir.TypeRegistry) *ir.Operation {
				return &ir.Operation{
					Group:         "search",
					TypePrefix:    "Search",
					HTTPMethods:   []string{http.MethodPost, http.MethodGet},
					PrimaryPath:   "/_search",
					Response:      shardsFixtureResp("Search"),
					ErrorWrappers: []string{errwrap.WrapperSearchShards},
					DispatchRoutes: []ir.DispatchRoute{
						{ReceiverType: "Client", MethodName: "Search", TopLevel: true},
					},
				}
			},
			contains: []string{
				"data.PartialFailures(c.errors)",
				"collapsePerOpErrors(data.PartialFailures(c.errors), nil)",
			},
		},
		{
			name: "multi-wrapper bulk dispatch delegates aggregation",
			buildOp: func(_ *ir.TypeRegistry) *ir.Operation {
				return &ir.Operation{
					Group:         "bulk",
					TypePrefix:    "Bulk",
					HTTPMethods:   []string{http.MethodPost},
					PrimaryPath:   "/_bulk",
					Response:      bulkFixtureResp(),
					ErrorWrappers: []string{errwrap.WrapperBulkItems, errwrap.WrapperWriteShards},
					DispatchRoutes: []ir.DispatchRoute{
						{ReceiverType: "Client", MethodName: "Bulk", TopLevel: true},
					},
				}
			},
			contains: []string{
				"data.PartialFailures(c.errors)",
				"collapsePerOpErrors",
			},
			// All wrapper-detection logic moved into PartialFailureFragment.
			notContains: []string{"errmask.", "PartialBulkError{"},
		},
		{
			name: "sub-client receiver reaches parent mask",
			buildOp: func(_ *ir.TypeRegistry) *ir.Operation {
				return &ir.Operation{
					Group:         "document.create",
					TypePrefix:    "DocumentCreate",
					HTTPMethods:   []string{http.MethodPut, http.MethodPost},
					PrimaryPath:   "/{index}/_create/{id}",
					Response:      shardsFixtureResp("DocumentCreate"),
					ErrorWrappers: []string{errwrap.WrapperWriteShards},
					DispatchRoutes: []ir.DispatchRoute{
						{ReceiverType: "documentClient", MethodName: "Create", TopLevel: false},
					},
				}
			},
			contains: []string{"data.PartialFailures(c.apiClient.errors)"},
		},
		{
			name: "msearch group wraps slice in MsearchErrors",
			buildOp: func(reg *ir.TypeRegistry) *ir.Operation {
				return &ir.Operation{
					Group:         "msearch",
					TypePrefix:    "MSearch",
					HTTPMethods:   []string{http.MethodPost},
					PrimaryPath:   "/_msearch",
					Response:      msearchFixtureResp(reg),
					ErrorWrappers: []string{errwrap.WrapperMultiSearchItems},
					DispatchRoutes: []ir.DispatchRoute{
						{ReceiverType: "Client", MethodName: "MSearch", TopLevel: true},
					},
				}
			},
			contains: []string{
				"data.PartialFailures(c.errors)",
				"&MsearchErrors{errs: errs}",
			},
		},
		{
			name: "wrapper without RenderMethod silently skipped",
			buildOp: func(_ *ir.TypeRegistry) *ir.Operation {
				return &ir.Operation{
					Group:         "tasks.list",
					TypePrefix:    "TasksList",
					HTTPMethods:   []string{http.MethodGet},
					PrimaryPath:   "/_tasks",
					Response:      newRespType("TasksList"),
					ErrorWrappers: []string{errwrap.WrapperTaskFailures},
					DispatchRoutes: []ir.DispatchRoute{
						{ReceiverType: "tasksClient", MethodName: "List", TopLevel: false},
					},
				}
			},
			contains:    []string{"return &data, nil"},
			notContains: []string{"PartialFailures"},
		},
		{
			name: "applies guard drops emission when typed response lacks Shards",
			buildOp: func(_ *ir.TypeRegistry) *ir.Operation {
				return &ir.Operation{
					Group:         "create",
					TypePrefix:    "Create",
					HTTPMethods:   []string{http.MethodPut, http.MethodPost},
					PrimaryPath:   "/{index}/_create/{id}",
					Response:      newRespType("Create"),
					ErrorWrappers: []string{errwrap.WrapperWriteShards},
					DispatchRoutes: []ir.DispatchRoute{
						{ReceiverType: "Client", MethodName: "Create", TopLevel: true},
					},
				}
			},
			notContains: []string{"PartialFailures"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reg := newRegistry()
			body, err := (&emit.DispatchFragment{Op: tt.buildOp(reg), Registry: reg}).Body()
			require.NoError(t, err)
			for _, want := range tt.contains {
				require.Contains(t, body, want)
			}
			for _, dont := range tt.notContains {
				require.NotContains(t, body, dont)
			}
		})
	}
}

func TestDispatchFragment_Imports(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		op             *ir.Operation
		wantErrmaskImp bool
	}{
		{
			name: "no wrappers, no errmask import",
			op: &ir.Operation{
				Group:       "cluster.health",
				TypePrefix:  "ClusterHealth",
				HTTPMethods: []string{http.MethodGet},
				PrimaryPath: "/_cluster/health",
				Response:    newRespType("ClusterHealth"),
				DispatchRoutes: []ir.DispatchRoute{
					{ReceiverType: "clusterClient", MethodName: "Health", TopLevel: false},
				},
			},
			wantErrmaskImp: false,
		},
		{
			// errmask is now imported by PartialFailureFragment, never by
			// DispatchFragment, since the methods (and their errmask
			// references) live in the partial-failure fragment.
			name: "wrappers present, dispatch still skips errmask",
			op: &ir.Operation{
				Group:         "bulk",
				TypePrefix:    "Bulk",
				HTTPMethods:   []string{http.MethodPost},
				PrimaryPath:   "/_bulk",
				Response:      bulkFixtureResp(),
				ErrorWrappers: []string{errwrap.WrapperBulkItems},
				DispatchRoutes: []ir.DispatchRoute{
					{ReceiverType: "Client", MethodName: "Bulk", TopLevel: true},
				},
			},
			wantErrmaskImp: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			imps := (&emit.DispatchFragment{Op: tt.op, Registry: newRegistry()}).Imports()
			var found bool
			for _, imp := range imps {
				if imp.Path == errmaskImportPath {
					found = true
					break
				}
			}
			require.Equal(t, tt.wantErrmaskImp, found)
		})
	}
}

// ---------------------------------------------------------------------------
// PartialFailureFragment: per-Resp helper methods + aggregator
// ---------------------------------------------------------------------------

func TestPartialFailureFragment_Body(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		buildOp     func(reg *ir.TypeRegistry) *ir.Operation
		empty       bool
		contains    []string
		notContains []string
	}{
		{
			name: "no wrappers emits nothing",
			buildOp: func(_ *ir.TypeRegistry) *ir.Operation {
				return &ir.Operation{
					Group:      "cluster.health",
					TypePrefix: "ClusterHealth",
					Response:   newRespType("ClusterHealth"),
				}
			},
			empty: true,
		},
		{
			name: "applies guard skips wrapper when response lacks fields",
			buildOp: func(_ *ir.TypeRegistry) *ir.Operation {
				return &ir.Operation{
					Group:         "create",
					TypePrefix:    "Create",
					Response:      newRespType("Create"), // no Shards
					ErrorWrappers: []string{errwrap.WrapperWriteShards},
				}
			},
			empty: true,
		},
		{
			name: "BulkItems emits per-Resp method + aggregator",
			buildOp: func(_ *ir.TypeRegistry) *ir.Operation {
				return &ir.Operation{
					Group:         "bulk",
					TypePrefix:    "Bulk",
					Response:      bulkFixtureResp(),
					ErrorWrappers: []string{errwrap.WrapperBulkItems},
					DispatchRoutes: []ir.DispatchRoute{
						{ReceiverType: "Client", MethodName: "Bulk", TopLevel: true},
					},
				}
			},
			contains: []string{
				"func (r *BulkResp) BulkItemFailures() *PartialBulkError",
				"func (r *BulkResp) PartialFailures(mask errmask.ErrorMask) []error",
				"if !mask.Has(errmask.BulkItems)",
				"if e := r.BulkItemFailures(); e != nil",
			},
		},
		{
			name: "SearchShards emits SearchShardFailures method",
			buildOp: func(_ *ir.TypeRegistry) *ir.Operation {
				return &ir.Operation{
					Group:         "search",
					TypePrefix:    "Search",
					Response:      shardsFixtureResp("Search"),
					ErrorWrappers: []string{errwrap.WrapperSearchShards},
					DispatchRoutes: []ir.DispatchRoute{
						{ReceiverType: "Client", MethodName: "Search", TopLevel: true},
					},
				}
			},
			contains: []string{
				"func (r *SearchResp) SearchShardFailures() *PartialSearchError",
				"r.Shards.Failed == 0",
				"if !mask.Has(errmask.SearchShards)",
			},
			// Value-typed Shards field: no nil-guard.
			notContains: []string{"r.Shards == nil"},
		},
		{
			name: "WriteShards emits ShardFailureError with op constant",
			buildOp: func(_ *ir.TypeRegistry) *ir.Operation {
				return &ir.Operation{
					Group:         "document.create",
					TypePrefix:    "DocumentCreate",
					Response:      shardsFixtureResp("DocumentCreate"),
					ErrorWrappers: []string{errwrap.WrapperWriteShards},
					DispatchRoutes: []ir.DispatchRoute{
						{ReceiverType: "documentClient", MethodName: "Create", TopLevel: false},
					},
				}
			},
			contains: []string{
				"func (r *DocumentCreateResp) WriteShardFailures() *ShardFailureError",
				"Operation:    OperationCreate",
			},
		},
		{
			name: "pointer Shards emits nil-guard before reading",
			buildOp: func(_ *ir.TypeRegistry) *ir.Operation {
				return &ir.Operation{
					Group:         "search",
					TypePrefix:    "Search",
					Response:      newRespType("Search", ir.Field{GoName: "Shards", GoType: "*ShardStatistics", IsPointer: true}),
					ErrorWrappers: []string{errwrap.WrapperSearchShards},
					DispatchRoutes: []ir.DispatchRoute{
						{ReceiverType: "Client", MethodName: "Search", TopLevel: true},
					},
				}
			},
			contains: []string{"if r.Shards == nil"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reg := newRegistry()
			body, err := (&emit.PartialFailureFragment{Op: tt.buildOp(reg), Registry: reg}).Body()
			require.NoError(t, err)
			if tt.empty {
				require.Empty(t, body)
				return
			}
			for _, want := range tt.contains {
				require.Contains(t, body, want)
			}
			for _, dont := range tt.notContains {
				require.NotContains(t, body, dont)
			}
		})
	}
}

func TestPartialFailureFragment_Imports(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		op             *ir.Operation
		wantErrmaskImp bool
	}{
		{
			name: "no wrappers, no errmask import",
			op: &ir.Operation{
				Group:      "cluster.health",
				TypePrefix: "ClusterHealth",
				Response:   newRespType("ClusterHealth"),
			},
			wantErrmaskImp: false,
		},
		{
			name: "with wrappers, errmask import added",
			op: &ir.Operation{
				Group:         "bulk",
				TypePrefix:    "Bulk",
				Response:      bulkFixtureResp(),
				ErrorWrappers: []string{errwrap.WrapperBulkItems},
			},
			wantErrmaskImp: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			imps := (&emit.PartialFailureFragment{Op: tt.op, Registry: newRegistry()}).Imports()
			var found bool
			for _, imp := range imps {
				if imp.Path == errmaskImportPath {
					found = true
					break
				}
			}
			require.Equal(t, tt.wantErrmaskImp, found)
		})
	}
}
