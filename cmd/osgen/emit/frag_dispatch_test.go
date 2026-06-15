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

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/emit"
	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/errwrap"
	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

// errmaskImportPath is the absolute import path that PartialFailureFragment
// adds when it emits any wrapper helpers; DispatchFragment never adds it
// since the methods (and their errmask references) live in the partial-
// failure fragment.
const errmaskImportPath = "github.com/opensearch-project/opensearch-go/v5/errmask"

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
	return ir.NewTypeRegistry("opensearchapi", "github.com/opensearch-project/opensearch-go/v5/opensearchapi")
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
			name: "compat forwarder renders thin forwarding body",
			buildOp: func(_ *ir.TypeRegistry) *ir.Operation {
				return &ir.Operation{
					Group:       "bulk",
					TypePrefix:  "Bulk",
					HTTPMethods: []string{http.MethodPost},
					PrimaryPath: "/_bulk",
					Response:    newRespType("Bulk"),
					DispatchRoutes: []ir.DispatchRoute{
						{ReceiverType: "documentClient", MethodName: "Bulk", FieldPath: "Doc"},
						{ReceiverType: "Client", MethodName: "Bulk", TopLevel: true, Forward: "Doc.Bulk"},
					},
				}
			},
			contains: []string{
				"func (c documentClient) Bulk(",
				"func (c Client) Bulk(",
				"return c.Doc.Bulk(ctx, req)",
			},
		},
		{
			name: "deprecated compat forwarder carries deprecation comment",
			buildOp: func(_ *ir.TypeRegistry) *ir.Operation {
				return &ir.Operation{
					Group:       "bulk",
					TypePrefix:  "Bulk",
					HTTPMethods: []string{http.MethodPost},
					PrimaryPath: "/_bulk",
					Response:    newRespType("Bulk"),
					DispatchRoutes: []ir.DispatchRoute{
						{ReceiverType: "documentClient", MethodName: "Bulk", FieldPath: "Doc"},
						{ReceiverType: "Client", MethodName: "Bulk", TopLevel: true, Forward: "Doc.Bulk", Deprecated: true},
					},
				}
			},
			contains: []string{
				"// Deprecated: use Doc.Bulk instead.",
				"return c.Doc.Bulk(ctx, req)",
			},
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
				"data.PartialFailures(c.errorMask())",
				"collapsePerOpErrors(data.PartialFailures(c.errorMask()), nil)",
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
				"data.PartialFailures(c.errorMask())",
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
			contains: []string{"data.PartialFailures(c.apiClient.errorMask())"},
		},
		{
			name: "msearch group wraps slice in MSearchErrors when 2+ wrappers fire",
			buildOp: func(reg *ir.TypeRegistry) *ir.Operation {
				return &ir.Operation{
					Group:       "msearch",
					TypePrefix:  "MSearch",
					HTTPMethods: []string{http.MethodPost},
					PrimaryPath: "/_msearch",
					Response:    msearchFixtureResp(reg),
					// Both categories apply: the per-op aggregator is referenced
					// only when 2+ can fire (matching collapsePerOpErrors' wrap
					// threshold).
					ErrorWrappers: []string{errwrap.WrapperSearchShards, errwrap.WrapperMultiSearchItems},
					DispatchRoutes: []ir.DispatchRoute{
						{ReceiverType: "Client", MethodName: "MSearch", TopLevel: true},
					},
				}
			},
			contains: []string{
				"data.PartialFailures(c.errorMask())",
				"&MSearchErrors{errs: errs}",
			},
		},
		{
			// F7 guard: an msearch-group op with a single emittable wrapper
			// must NOT reference the per-op aggregator (collapse never wraps a
			// 0/1 result), so the coupling can't dangle into a compile break if
			// a group ever drops to one wrapper.
			name: "msearch group with single wrapper uses nil collapse closure",
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
				"collapsePerOpErrors(data.PartialFailures(c.errorMask()), nil)",
			},
			notContains: []string{"MSearchErrors{errs: errs}"},
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

// TestPerOpErrorTypeName_CatalogConsistency pins the catalog <-> switch
// coupling between [emit.PerOpErrorTypeName] and
// [errwrap.OperationWrappers]. It asserts three directions, all keyed
// off groups present in either side:
//
//  1. every group naming a per-op aggregator type has 2+ wrappers in
//     the catalog (otherwise the per-op type is referenced from
//     generated code but the catalog says only 0 or 1 wrappers exist);
//  2. every catalog entry with 2+ wrappers names a per-op aggregator
//     type (otherwise the dispatch emits an empty per-op type name);
//  3. every group named by the switch is present in the catalog at all
//     (otherwise a switch arm exists for a group neither loop above
//     would iterate, so it could go stale silently).
//
// What this does NOT pin: the runtime emission decision is gated by
// `len(emittable) >= 2` from [DispatchFragment.emittableWrappers],
// which further filters the catalog through the emission `wrappers`
// map and per-wrapper `Applies` predicates, and by
// `resolveErrorWrappers` (which prefers the spec extension
// `x-error-responses` over the catalog). Today those sets coincide for
// the only 2+-wrapper groups (`msearch` / `msearch_template`); a
// future spec edit or wrapper-table change could still desync those
// without this test failing. Tracked separately if it ever matters.
func TestPerOpErrorTypeName_CatalogConsistency(t *testing.T) {
	t.Parallel()

	// switchGroups enumerates every group named by perOpErrorTypeName's
	// hardcoded switch. Kept in sync with the switch by hand: when a new
	// arm is added there, add it here too. (3) below catches the inverse
	// drift -- a switch arm whose group never reaches the catalog.
	switchGroups := []string{
		errwrap.GroupMSearch,
		errwrap.GroupMSearchTemplate,
	}

	// (1) Forward: every group the catalog names with a per-op
	// aggregator type must declare 2+ wrappers there.
	for group := range errwrap.OperationWrappers() {
		typeName := emit.PerOpErrorTypeName(group)
		if typeName == "" {
			continue
		}
		t.Run("type_for_"+group, func(t *testing.T) {
			t.Parallel()
			require.GreaterOrEqual(t, len(errwrap.OperationWrappers()[group]), 2,
				"group %q has per-op error type %q but only %d wrapper(s) in OperationWrappers; either add wrappers or remove the switch arm",
				group, typeName, len(errwrap.OperationWrappers()[group]))
		})
	}

	// (2) Reverse: every catalog entry with 2+ wrappers must name a
	// per-op aggregator type.
	for group, wrappers := range errwrap.OperationWrappers() {
		if len(wrappers) < 2 {
			continue
		}
		t.Run("catalog_entry_"+group, func(t *testing.T) {
			t.Parallel()
			require.NotEmpty(t, emit.PerOpErrorTypeName(group),
				"group %q declares %d wrappers %v in OperationWrappers but PerOpErrorTypeName returns empty; "+
					"add a switch arm and a hand-written %q-style aggregator type",
				group, len(wrappers), wrappers, group)
		})
	}

	// (3) Switch-arm catalog presence: every group named by the
	// per-op switch must appear in OperationWrappers. A switch arm
	// for a group missing from the catalog is dead code: neither
	// loop above iterates it, so without this check it could
	// outlive the catalog entry that justified it.
	for _, group := range switchGroups {
		t.Run("switch_arm_in_catalog_"+group, func(t *testing.T) {
			t.Parallel()
			_, ok := errwrap.OperationWrappers()[group]
			require.True(t, ok,
				"perOpErrorTypeName has a switch arm for group %q but the group is absent from errwrap.OperationWrappers; "+
					"remove the arm or restore the catalog entry",
				group)
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
			name: "BulkItems resolves spec-derived inner item type when outer is registered",
			buildOp: func(reg *ir.TypeRegistry) *ir.Operation {
				// Register the outer item wrapper with its discriminated
				// pointer fields. bulkInnerItemType walks pointer fields
				// to find the inner per-op item; emission then keys off
				// that resolved name rather than the "BulkRespItem" literal
				// fallback.
				reg.Register(&ir.Type{
					Name:      "CustomBulkItem",
					SchemaRef: "#/test/CustomBulkItem",
					Scope:     ir.ScopeLocal,
					Fields: []ir.Field{
						{GoName: "Create", GoType: "*CustomBulkRespItem", IsPointer: true},
						{GoName: "Delete", GoType: "*CustomBulkRespItem", IsPointer: true},
						{GoName: "Index", GoType: "*CustomBulkRespItem", IsPointer: true},
						{GoName: "Update", GoType: "*CustomBulkRespItem", IsPointer: true},
					},
				})
				return &ir.Operation{
					Group:      "bulk",
					TypePrefix: "Bulk",
					Response: newRespType("Bulk",
						ir.Field{GoName: "Errors", GoType: "bool"},
						ir.Field{GoName: "Items", GoType: "[]CustomBulkItem"},
					),
					ErrorWrappers: []string{errwrap.WrapperBulkItems},
					DispatchRoutes: []ir.DispatchRoute{
						{ReceiverType: "Client", MethodName: "Bulk", TopLevel: true},
					},
				}
			},
			contains: []string{
				// Resolved inner type appears verbatim in the emission.
				"var failed []CustomBulkRespItem",
				"for _, v := range []*CustomBulkRespItem{",
			},
			notContains: []string{
				// Fallback literal must not leak into the emission.
				"var failed []BulkRespItem",
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
		{
			name: "BroadcastShards emits aggregate-shape helper without per-op constant",
			buildOp: func(_ *ir.TypeRegistry) *ir.Operation {
				return &ir.Operation{
					Group:         "indices.refresh",
					TypePrefix:    "IndicesRefresh",
					Response:      shardsFixtureResp("IndicesRefresh"),
					ErrorWrappers: []string{errwrap.WrapperBroadcastShards},
					DispatchRoutes: []ir.DispatchRoute{
						{ReceiverType: "indicesClient", MethodName: "Refresh", TopLevel: false},
					},
				}
			},
			contains: []string{
				"func (r *IndicesRefreshResp) BroadcastShardFailures() *PartialSearchError",
				"r.Shards.Failed == 0",
				"Failures:     r.Shards.Failures",
				"if !mask.Has(errmask.BroadcastShards)",
				"if e := r.BroadcastShardFailures(); e != nil",
			},
			// Broadcast envelope: no per-op Operation constant (that's
			// WriteShards' surface, not BroadcastShards').
			notContains: []string{"Operation:"},
		},
		{
			name: "MultiSearchItems flat shape emits Responses iteration over Status/Error",
			buildOp: func(reg *ir.TypeRegistry) *ir.Operation {
				// Element type carries both Shards (so applyMultiSearchItems'
				// elementTypeHasShards guard passes) AND Status+Error (so the
				// renderer's flat-shape branch produces working code).
				if _, ok := reg.LookupByName("MSearchFlatItem"); !ok {
					reg.Register(&ir.Type{
						Name:      "MSearchFlatItem",
						SchemaRef: "#/test/MSearchFlatItem",
						Scope:     ir.ScopeLocal,
						Fields: []ir.Field{
							{GoName: "Shards", GoType: "ShardStatistics"},
							{GoName: "Status", GoType: "int"},
							{GoName: "Error", GoType: "*DocumentError", IsPointer: true},
						},
					})
				}
				return &ir.Operation{
					Group:         "msearch",
					TypePrefix:    "MSearch",
					Response:      newRespType("MSearch", ir.Field{GoName: "Responses", GoType: "[]MSearchFlatItem"}),
					ErrorWrappers: []string{errwrap.WrapperMultiSearchItems},
					DispatchRoutes: []ir.DispatchRoute{
						{ReceiverType: "Client", MethodName: "MSearch", TopLevel: true},
					},
				}
			},
			contains: []string{
				"func (r *MSearchResp) MultiSearchItemFailures() *MultiSearchItemError",
				"for i, resp := range r.Responses",
				"if resp.Error != nil",
				"Status: resp.Status",
				"Error:  resp.Error",
				"if !mask.Has(errmask.MultiSearchItems)",
			},
			notContains: []string{
				// Flat shape must not reach the union-discriminator template.
				".Type() ==",
				"resp.ErrorRespBase",
			},
		},
		{
			name: "MultiSearchItems union shape emits union-discriminator dispatch",
			buildOp: func(reg *ir.TypeRegistry) *ir.Operation {
				// Build a discriminated-union response item with one
				// Shards-bearing branch and one ErrorRespBase-shape branch
				// so resolveUnionShape classifies both sides.
				reg.Register(&ir.Type{
					Name:      "MsearchSuccessBranch",
					SchemaRef: "#/test/MsearchSuccessBranch",
					Scope:     ir.ScopeLocal,
					Fields: []ir.Field{
						{GoName: "Shards", GoType: "ShardStatistics"},
					},
				})
				reg.Register(&ir.Type{
					Name:      "MsearchErrorBranch",
					SchemaRef: "#/test/MsearchErrorBranch",
					Scope:     ir.ScopeLocal,
					Fields: []ir.Field{
						{GoName: "Status", GoType: "int"},
						{GoName: "Error", GoType: "*DocumentError", IsPointer: true},
					},
				})
				reg.Register(&ir.Type{
					Name:      "MSearchUnionItem",
					SchemaRef: "#/test/MSearchUnionItem",
					Kind:      ir.TypeUnion,
					Scope:     ir.ScopeLocal,
					Branches: []ir.UnionBranch{
						// Name deliberately differs from GoType (as a titled
						// branch would): the dispatch must splice the accessor
						// Name, not the GoType, into the const/accessor.
						{Name: "Success", GoType: "MsearchSuccessBranch"},
						{Name: "ErrorResult", GoType: "MsearchErrorBranch"},
					},
				})
				return &ir.Operation{
					Group:         "msearch",
					TypePrefix:    "MSearch",
					Response:      newRespType("MSearch", ir.Field{GoName: "Responses", GoType: "[]MSearchUnionItem"}),
					ErrorWrappers: []string{errwrap.WrapperMultiSearchItems},
					DispatchRoutes: []ir.DispatchRoute{
						{ReceiverType: "Client", MethodName: "MSearch", TopLevel: true},
					},
				}
			},
			contains: []string{
				"func (r *MSearchResp) MultiSearchItemFailures() *MultiSearchItemError",
				"resp.Type() == MSearchUnionItemErrorResultType",
				"ErrorRespBase: resp.ErrorResult()",
			},
			// Union shape must not fall back to the flat template.
			notContains: []string{"if resp.Error != nil"},
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
