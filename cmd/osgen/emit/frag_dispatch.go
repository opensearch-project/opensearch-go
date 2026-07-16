// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/errwrap"
	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

// DispatchFragment renders one or more client dispatch methods for an operation.
type DispatchFragment struct {
	Op       *ir.Operation
	Registry *ir.TypeRegistry
}

// Imports returns the imports the dispatch-method fragment needs.
func (f *DispatchFragment) Imports() []Import {
	if len(f.Op.DispatchRoutes) == 0 {
		return nil
	}
	imps := []Import{
		{Path: "context"},
		{Path: "net/http"},
	}
	if f.Op.IsNoBody {
		imps = append(imps, Import{Path: LocalModule, Alias: "opensearch"})
	}
	return imps
}

// Body renders the client method that dispatches a request to the operation.
func (f *DispatchFragment) Body() (string, error) {
	if len(f.Op.DispatchRoutes) == 0 {
		return "", nil
	}

	emittable := f.emittableWrappers()
	hasPartial := len(emittable) > 0
	// The per-op aggregator wrapper is referenced only when 2+ categories can
	// fire -- exactly the case where collapsePerOpErrors wraps (0 -> nil, 1 ->
	// bare sub-error, 2+ -> wrap). Gating perOpType on the same count makes that
	// coupling explicit: a group that ever drops to a single emittable wrapper
	// stops referencing the per-op type, so it can't dangle into a compile break.
	perOpType := ""
	if len(emittable) >= 2 {
		perOpType = perOpErrorTypeName(f.Op.Group)
	}

	tmpl := template.Must(template.New("dispatch").Funcs(template.FuncMap{
		"methodConst":      HTTPMethodConst,
		"primaryMethod":    PrimaryMethod,
		"bodyMethodSwitch": bodyMethodSwitch,
		"recvTopLevel":     func() string { return recvTopLevel },
		"recvSubClient":    func() string { return recvSubClient },
		"opMethodComment": func(methodName string, op *ir.Operation) string {
			return MethodComment(MethodDocData{
				MethodName:        methodName,
				Group:             op.Group,
				Description:       op.Description,
				HTTPMethods:       op.HTTPMethods,
				PrimaryPath:       op.PrimaryPath,
				VersionAdded:      op.VersionAdded,
				VersionDeprecated: op.VersionDeprecated,
				DeprecationMsg:    op.DeprecationMsg,
				ExcludedDistros:   op.ExcludedDistros,
				DocsURL:           op.DocsURL,
			})
		},
	}).Parse(dispatchTemplateText))

	data := struct {
		*ir.Operation
		Routes             []ir.DispatchRoute
		HasPartialFailures bool
		PerOpErrType       string
	}{
		Operation:          f.Op,
		Routes:             f.Op.DispatchRoutes,
		HasPartialFailures: hasPartial,
		PerOpErrType:       perOpType,
	}

	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("rendering DispatchFragment for %s: %w", f.Op.Group, err)
	}
	return sb.String(), nil
}

// emittableWrappers returns the subset of op.ErrorWrappers for which the
// dispatch template has a hand-written emission AND the response shape
// actually carries the field path the emission references.
//
// The first guard (presence in [wrappers]) keeps unimplemented wrappers
// from rendering anything. The second guard (Applies) suppresses
// emissions whose required fields are missing from the typed response,
// e.g. when the spec annotates an op with WriteShards but the bundled
// schema for its 200 response leaves out _shards. Both cases are spec
// or type-gen gaps tracked separately; we just don't emit broken code.
func (f *DispatchFragment) emittableWrappers() []string {
	var out []string
	for _, w := range f.Op.ErrorWrappers {
		entry, ok := wrappers[w]
		if !ok {
			continue
		}
		if entry.Applies != nil && !entry.Applies(f.Op.Response, f.Registry) {
			continue
		}
		out = append(out, w)
	}
	return out
}

// Field names referenced by the wrapper templates. Centralized so the
// applies check stays in lock-step with the emission template strings.
// These names match the JSON-tag-driven Go fields that osgen produces
// for the corresponding response wrapper schemas in
// `_common.errors.yaml`.
const (
	respFieldShards    = "Shards"
	respFieldErrors    = "Errors"
	respFieldItems     = "Items"
	respFieldResponses = "Responses"
	respFieldStatus    = "Status"
	respFieldError     = "Error"
)

// Receiver expressions used at API method call sites. Top-level Client
// methods read the mask via `c.errorMask()`; sub-clients reach the
// parent via `c.apiClient.errorMask()`.
const (
	recvTopLevel  = "c"
	recvSubClient = "c.apiClient"
)

// errmaskImportPath is the absolute import path used by every generated
// dispatch handler that emits a wrapper check.
const errmaskImportPath = "github.com/opensearch-project/opensearch-go/v5/errmask"

// wrapperApplyFunc reports whether the wrapper's emission template
// applies to a given response shape. It is consulted before emission so
// the dispatch template never references fields the typed response
// lacks.
type wrapperApplyFunc func(resp *ir.Type, reg *ir.TypeRegistry) bool

// applyHasShards is reused by every wrapper whose emission accesses
// `data.Shards` directly on the response.
func applyHasShards(resp *ir.Type, reg *ir.TypeRegistry) bool {
	return responseHasField(resp, respFieldShards, reg)
}

// applyBulkItems checks for the BulkItems wire shape: top-level
// `errors: bool` plus `items: []`.
func applyBulkItems(resp *ir.Type, reg *ir.TypeRegistry) bool {
	return responseHasField(resp, respFieldErrors, reg) && responseHasField(resp, respFieldItems, reg)
}

// applyMultiSearchItems checks that the response carries a Responses
// slice whose element type itself has a Shards field. The current
// emission still uses the per-shard aggregation path; richer
// union-aware detection lives in [TODO].
func applyMultiSearchItems(resp *ir.Type, reg *ir.TypeRegistry) bool {
	f, ok := lookupResponseField(resp, respFieldResponses, reg)
	if !ok {
		return false
	}
	return elementTypeHasShards(f.GoType, reg)
}

// responseHasField walks the response struct (including any embedded
// types resolved through the registry) looking for a field with the
// given exported Go name.
func responseHasField(resp *ir.Type, goName string, reg *ir.TypeRegistry) bool {
	_, ok := lookupResponseField(resp, goName, reg)
	return ok
}

// lookupResponseField is the depth-walking variant of responseHasField:
// it returns the resolved field as well as a presence bool so callers
// can inspect the Go type.
func lookupResponseField(resp *ir.Type, goName string, reg *ir.TypeRegistry) (ir.Field, bool) {
	if resp == nil {
		return ir.Field{}, false
	}
	for _, f := range resp.Fields {
		if !f.IsEmbed && f.GoName == goName {
			return f, true
		}
	}
	if reg == nil {
		return ir.Field{}, false
	}
	for _, f := range resp.Fields {
		if !f.IsEmbed {
			continue
		}
		emb, ok := reg.LookupByName(f.GoType)
		if !ok {
			continue
		}
		if got, ok := lookupResponseField(emb, goName, reg); ok {
			return got, true
		}
	}
	return ir.Field{}, false
}

// elementTypeHasShards reports whether goType (e.g. "[]Foo") has an
// element type with a Shards field. Only handles slice and pointer
// wrappers since those are the shapes we care about.
func elementTypeHasShards(goType string, reg *ir.TypeRegistry) bool {
	if reg == nil {
		return false
	}
	t := strings.TrimPrefix(goType, "[]")
	t = strings.TrimPrefix(t, "*")
	resolved, ok := reg.LookupByName(t)
	if !ok {
		return false
	}
	if responseHasField(resolved, respFieldShards, reg) {
		return true
	}
	// Element is a discriminated union -- delegate to union resolution
	// to see whether any branch carries Shards.
	if resolved.Kind == ir.TypeLazyUnion || resolved.Kind == ir.TypeUnion {
		return resolveUnionShape(resolved, reg).success != ""
	}
	return false
}

// bulkInnerItemType resolves the per-op item type a Bulk-shaped
// response wrapper must walk. The response carries Items[]<Outer>
// (e.g. []BulkItem); each Outer in turn has N optional pointer
// fields (`Create/Delete/Index/Update`) all targeting the same Inner
// type (e.g. *BulkRespItem). Returns the unwrapped Inner name.
//
// Returns ("", false) when:
//   - Items is missing (no Bulk-shape response)
//   - the Outer element type isn't in the registry
//   - the Outer has no pointer fields (malformed schema)
//
// In those cases the caller falls back to a hardcoded literal so the
// emission stays compilable.
func bulkInnerItemType(resp *ir.Type, reg *ir.TypeRegistry) (string, bool) {
	if reg == nil {
		return "", false
	}
	itemsField, ok := lookupResponseField(resp, respFieldItems, reg)
	if !ok {
		return "", false
	}
	outerName := strings.TrimPrefix(itemsField.GoType, "[]")
	outerName = strings.TrimPrefix(outerName, "*")
	outer, ok := reg.LookupByName(outerName)
	if !ok {
		return "", false
	}
	for _, f := range outer.Fields {
		if !f.IsPointer {
			continue
		}
		inner := strings.TrimPrefix(f.GoType, "*")
		if inner != "" {
			return inner, true
		}
	}
	return "", false
}

// unionShape captures the metadata cmd/osgen needs to emit
// type-discriminated access into a union element. The success branch
// is whichever branch carries a Shards field (recursively); the error
// branch matches the spec's ErrorRespBase shape (Status + Error).
type unionShape struct {
	unionName   string // e.g. "MSearchMultiSearchResultResponsesItem"
	success     string // success branch accessor Name (Shards-bearing), or ""
	errorBranch string // error branch accessor Name (Status + Error), or ""
}

// resolveUnionShape walks a TypeLazyUnion/TypeUnion's branches and
// classifies them. Branches whose resolved type has a Shards field
// become the success branch; branches with both Status and Error
// fields become the error branch. Returns zero-value if the type isn't
// a union or no branches matched.
func resolveUnionShape(t *ir.Type, reg *ir.TypeRegistry) unionShape {
	if t == nil || (t.Kind != ir.TypeLazyUnion && t.Kind != ir.TypeUnion) {
		return unionShape{}
	}
	out := unionShape{unionName: t.Name}
	for _, b := range t.Branches {
		bt, ok := reg.LookupByName(b.GoType)
		if !ok {
			continue
		}
		// Store b.Name (the accessor/const identifier), not b.GoType: the
		// dispatch template splices these into "{{UnionName}}{{Success}}Type"
		// and "resp.{{Success}}()", which the union codegen declares from
		// b.Name. They coincide only when a $ref branch has no title; a
		// titled branch (or an abbreviation rule) makes Name != GoType.
		switch {
		case responseHasField(bt, respFieldShards, reg) && out.success == "":
			out.success = b.Name
		case responseHasField(bt, respFieldStatus, reg) &&
			responseHasField(bt, respFieldError, reg) &&
			out.errorBranch == "":
			out.errorBranch = b.Name
		}
	}
	return out
}

// wrapperEmission pairs per-wrapper code generators with an
// applicability predicate. RenderMethod emits a `func (r *<Resp>)
// <Wrapper>Failures() *<TypedError>` helper anchored on the typed
// response; it returns the typed error or nil. The dispatch handler
// reaches the helper through the [PartialFailureFragment]-emitted
// `PartialFailures(mask)` aggregator, so the dispatch template itself
// stays trivial.
type wrapperEmission struct {
	Applies      wrapperApplyFunc
	RenderMethod func(ctx wrapperRenderCtx) (string, error)
}

// wrapperRenderCtx carries everything a wrapper Render function needs
// to produce its emission block: the receiver expression, the
// operation under emission, the type registry (needed to resolve
// union-element response shapes), and the response Go type name (used
// when emitting method receivers).
type wrapperRenderCtx struct {
	Recv     string
	Op       *ir.Operation
	Registry *ir.TypeRegistry
	RespType string // e.g. "BulkResp" -- non-empty when emitting a method
	ItemType string // spec-derived element type of `Items[]`; only set by Bulk render
}

// perOpErrorTypeName returns the hand-written per-op error-aggregator
// type for ops with multiple x-error-responses entries. Returns "" for
// single-wrapper ops, in which case [collapsePerOpErrors] is called with
// a nil wrap closure (its 0-or-1 paths cover the single-wrapper case).
func perOpErrorTypeName(group string) string {
	switch group {
	case errwrap.GroupMSearch:
		return "MSearchErrors"
	case errwrap.GroupMSearchTemplate:
		return "MSearchTemplateErrors"
	}
	return ""
}

// wrappers maps each wrapper-schema name to its per-Resp helper
// method renderer and applicability predicate. RenderMethod emits the
// full `func (r *<Resp>) <Wrapper>Failures() *<TypedError>` helper.
//
// Add new wrappers here as we add hand-written detection for more
// categories; entries missing from this map are recorded in the IR
// but skipped at emit time.
//
//nolint:gochecknoglobals // const-ish read-only catalog
var wrappers = map[string]wrapperEmission{
	errwrap.WrapperBulkItems: {
		Applies:      applyBulkItems,
		RenderMethod: renderBulkItemsMethod,
	},
	errwrap.WrapperSearchShards: {
		Applies:      applySearchShards,
		RenderMethod: renderSearchShardsMethod,
	},
	errwrap.WrapperWriteShards: {
		Applies:      applyHasShards,
		RenderMethod: renderWriteShardsMethod,
	},
	errwrap.WrapperBroadcastShards: {
		Applies:      applyHasShards,
		RenderMethod: renderBroadcastShardsMethod,
	},
	errwrap.WrapperMultiSearchItems: {
		Applies:      applyMultiSearchItems,
		RenderMethod: renderMultiSearchItemsMethod,
	},
}

// applySearchShards returns true when the response either carries a
// top-level Shards field (Search, Scroll, Index-style ops) or has a
// Responses slice whose element-type union exposes a Shards-bearing
// branch (MSearch / MSearchTemplate).
func applySearchShards(resp *ir.Type, reg *ir.TypeRegistry) bool {
	if applyHasShards(resp, reg) {
		return true
	}
	f, ok := lookupResponseField(resp, respFieldResponses, reg)
	if !ok {
		return false
	}
	return elementTypeHasShards(f.GoType, reg)
}

func renderBulkItemsMethod(ctx wrapperRenderCtx) (string, error) {
	// The walk needs the inner per-op item type (e.g. BulkRespItem),
	// not the outer wrapper (BulkItem) that's the element of Items[].
	// Each outer wrapper carries N optional pointer fields
	// (Create/Delete/Index/Update for Bulk) all pointing to the inner
	// type; we resolve the inner by:
	//   1. lookup Items[] -> outer element type (BulkItem)
	//   2. inspect the outer's first pointer field -> *BulkRespItem
	//   3. strip the pointer -> BulkRespItem
	//
	// Falls back to the hardcoded literal if either lookup misses;
	// the Applies guard would have rejected the wrapper at emit time
	// for any response missing the path.
	itemType := "BulkRespItem"
	if outer, ok := bulkInnerItemType(ctx.Op.Response, ctx.Registry); ok {
		itemType = outer
	}
	ctx.ItemType = itemType
	return execTpl("BulkItemsMethod", `
// BulkItemFailures detects partial failures on a Bulk response by
// scanning every per-item op for a non-nil Error. Returns nil when no
// items failed.
func (r *{{.RespType}}) BulkItemFailures() *PartialBulkError {
	if r == nil || !r.Errors {
		return nil
	}
	var failed []{{.ItemType}}
	succeeded := 0
	for _, item := range r.Items {
		for _, v := range []*{{.ItemType}}{item.Create, item.Delete, item.Index, item.Update} {
			if v == nil {
				continue
			}
			if v.Error != nil {
				failed = append(failed, *v)
			} else {
				succeeded++
			}
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return &PartialBulkError{
		FailedItems:    failed,
		SucceededCount: succeeded,
	}
}

`, ctx)
}

func renderSearchShardsMethod(ctx wrapperRenderCtx) (string, error) {
	if u := unionFromResponses(ctx.Op.Response, ctx.Registry); u.success != "" {
		return execTpl("SearchShardsMethod-union", `
// SearchShardFailures detects partial failures on a {{.RespType}} by
// aggregating shard envelopes across union-typed sub-responses.
func (r *{{.RespType}}) SearchShardFailures() *PartialSearchError {
	if r == nil {
		return nil
	}
	var totalShards, failedShards int
	var failures []ShardSearchFailure
	for _, resp := range r.Responses {
		if resp.Type() == {{.Union.UnionName}}{{.Union.Success}}Type {
			item := resp.{{.Union.Success}}()
			totalShards += item.Shards.Total
			failedShards += item.Shards.Failed
			failures = append(failures, item.Shards.Failures...)
		}
	}
	if failedShards == 0 {
		return nil
	}
	return &PartialSearchError{
		FailedShards: failedShards,
		TotalShards:  totalShards,
		Failures:     failures,
	}
}

`, ctx)
	}
	return execTpl("SearchShardsMethod", `
// SearchShardFailures detects partial failures on a {{.RespType}} by
// inspecting the top-level _shards envelope. Returns nil when no
// shards failed.
func (r *{{.RespType}}) SearchShardFailures() *PartialSearchError {
	if r == nil {
		return nil
	}
{{- if .ShardsNilGuard}}
	if r.Shards == nil {
		return nil
	}
{{- end}}
	if r.Shards.Failed == 0 {
		return nil
	}
	return &PartialSearchError{
		FailedShards: r.Shards.Failed,
		TotalShards:  r.Shards.Total,
		Failures:     r.Shards.Failures,
	}
}

`, ctx)
}

func renderWriteShardsMethod(ctx wrapperRenderCtx) (string, error) {
	return execTpl("WriteShardsMethod", `
// WriteShardFailures detects replica-shard failures on a {{.RespType}}.
// Returns nil when no shards failed.
func (r *{{.RespType}}) WriteShardFailures() *ShardFailureError {
	if r == nil {
		return nil
	}
{{- if .ShardsNilGuard}}
	if r.Shards == nil {
		return nil
	}
{{- end}}
	if r.Shards.Failed == 0 {
		return nil
	}
	return &ShardFailureError{
		Operation:    {{.WriteOperation}},
		FailedShards: r.Shards.Failed,
		TotalShards:  r.Shards.Total,
	}
}

`, ctx)
}

func renderBroadcastShardsMethod(ctx wrapperRenderCtx) (string, error) {
	return execTpl("BroadcastShardsMethod", `
// BroadcastShardFailures detects partial failures on a broadcast-shape
// response (one envelope, all-shards aggregate). Returns nil when no
// shards failed.
func (r *{{.RespType}}) BroadcastShardFailures() *PartialSearchError {
	if r == nil {
		return nil
	}
{{- if .ShardsNilGuard}}
	if r.Shards == nil {
		return nil
	}
{{- end}}
	if r.Shards.Failed == 0 {
		return nil
	}
	return &PartialSearchError{
		FailedShards: r.Shards.Failed,
		TotalShards:  r.Shards.Total,
		Failures:     r.Shards.Failures,
	}
}

`, ctx)
}

func renderMultiSearchItemsMethod(ctx wrapperRenderCtx) (string, error) {
	if u := unionFromResponses(ctx.Op.Response, ctx.Registry); u.errorBranch != "" {
		return execTpl("MultiSearchItemsMethod-union", `
// MultiSearchItemFailures detects per-sub-response Error objects on a
// {{.RespType}} via union-branch dispatch. Returns nil when every
// sub-response succeeded.
func (r *{{.RespType}}) MultiSearchItemFailures() *MultiSearchItemError {
	if r == nil {
		return nil
	}
	var failed []MultiSearchItemFailure
	succeeded := 0
	for i, resp := range r.Responses {
		if resp.Type() == {{.Union.UnionName}}{{.Union.ErrorBranch}}Type {
			failed = append(failed, MultiSearchItemFailure{
				Index:             i,
				ErrorRespBase: resp.{{.Union.ErrorBranch}}(),
			})
		} else {
			succeeded++
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return &MultiSearchItemError{
		Items:          failed,
		SucceededCount: succeeded,
	}
}

`, ctx)
	}
	// Fallback for responses that are not unions (a flat struct with a
	// direct Error field). All multi-search responses generate as unions
	// today, so this branch is currently unreached.
	return execTpl("MultiSearchItemsMethod", `
// MultiSearchItemFailures detects per-sub-response Error objects on a
// {{.RespType}}. Returns nil when every sub-response succeeded.
func (r *{{.RespType}}) MultiSearchItemFailures() *MultiSearchItemError {
	if r == nil {
		return nil
	}
	var failed []MultiSearchItemFailure
	succeeded := 0
	for i, resp := range r.Responses {
		if resp.Error != nil {
			failed = append(failed, MultiSearchItemFailure{
				Index:  i,
				Status: resp.Status,
				Error:  resp.Error,
			})
		} else {
			succeeded++
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return &MultiSearchItemError{
		Items:          failed,
		SucceededCount: succeeded,
	}
}

`, ctx)
}

// unionFromResponses returns the union shape if op's response carries a
// `Responses` slice whose element type is a discriminated union; the
// zero value otherwise. Centralized so SearchShards and
// MultiSearchItems agree on what "MSearch-style" means.
func unionFromResponses(resp *ir.Type, reg *ir.TypeRegistry) unionShape {
	f, ok := lookupResponseField(resp, respFieldResponses, reg)
	if !ok {
		return unionShape{}
	}
	t := strings.TrimPrefix(f.GoType, "[]")
	t = strings.TrimPrefix(t, "*")
	resolved, ok := reg.LookupByName(t)
	if !ok {
		return unionShape{}
	}
	if resolved.Kind != ir.TypeLazyUnion && resolved.Kind != ir.TypeUnion {
		return unionShape{}
	}
	return resolveUnionShape(resolved, reg)
}

// renderData carries everything a per-wrapper method template needs:
// the response Go type name (for receivers), the union shape (for
// MSearch-style ops), the OperationXxx constant for write ops, and a
// nil-guard flag for pointer-typed Shards fields.
type renderData struct {
	RespType       string
	WriteOperation string
	Union          unionRenderData
	// ShardsNilGuard is true when the response's Shards field is a
	// pointer type (e.g. *ShardStatistics, used by some omitempty
	// responses). The method template emits an `if r.Shards == nil
	// { return nil }` guard so the helper stays panic-free when the
	// wire response omits _shards.
	ShardsNilGuard bool
	// ItemType is the spec-derived element type of the response's
	// `Items` field (e.g. "BulkRespItem"). Empty unless the wrapper
	// emitter set it; consumed only by the BulkItems template.
	ItemType string
}

type unionRenderData struct {
	UnionName   string
	Success     string
	ErrorBranch string
}

// execTpl is the shared text/template invocation for wrapper
// RenderMethod functions. Builds a renderData (RespType +
// WriteOperation + Union + ShardsNilGuard) pulling the union shape and
// Shards-field pointer state from ctx.Op when applicable.
func execTpl(name, tpl string, ctx wrapperRenderCtx) (string, error) {
	t, err := template.New("wrapper:" + name).Parse(tpl)
	if err != nil {
		return "", fmt.Errorf("parsing %s emission: %w", name, err)
	}
	u := unionFromResponses(ctx.Op.Response, ctx.Registry)
	data := renderData{
		RespType:       ctx.RespType,
		WriteOperation: writeOperationConst(ctx.Op.Group),
		Union: unionRenderData{
			UnionName:   u.unionName,
			Success:     u.success,
			ErrorBranch: u.errorBranch,
		},
		ShardsNilGuard: shardsIsPointer(ctx.Op.Response, ctx.Registry),
		ItemType:       ctx.ItemType,
	}
	var sb strings.Builder
	if err := t.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("rendering %s emission: %w", name, err)
	}
	return sb.String(), nil
}

// shardsIsPointer reports whether the response's Shards field is a
// pointer (omitempty in the spec). Method templates use this to decide
// whether to emit a nil-guard before reading r.Shards.
func shardsIsPointer(resp *ir.Type, reg *ir.TypeRegistry) bool {
	field, ok := lookupResponseField(resp, respFieldShards, reg)
	if !ok {
		return false
	}
	return field.IsPointer
}

// writeOperationConst returns the OperationXxx identifier used in
// ShardFailureError.Operation for single-document write groups.
// Returned as a Go identifier (no quotes) so the emitted code reads
// e.g. `Operation: OperationCreate` rather than a literal string.
func writeOperationConst(group string) string {
	g := group
	if i := strings.LastIndex(g, "."); i >= 0 {
		g = g[i+1:]
	}
	switch g {
	case errwrap.GroupIndex:
		return errwrap.WriteOpIndex
	case errwrap.GroupCreate:
		return errwrap.WriteOpCreate
	case errwrap.GroupUpdate:
		return errwrap.WriteOpUpdate
	case errwrap.GroupDelete:
		return errwrap.WriteOpDelete
	}
	return ""
}

// dispatchTemplateText is the per-operation dispatch handler template.
// Each route renders one Go method that builds the request, calls request(),
// then delegates partial-failure detection to the Resp's PartialFailures
// aggregator (emitted by [PartialFailureFragment]).
//
// The dispatch's return passes through collapsePerOpErrors:
//
//   - returns nil if the aggregator returned no sub-errors,
//   - returns the bare sub-error if exactly one fired,
//   - wraps the slice in the per-op error type otherwise.
//
// Single-wrapper ops pass nil as the wrap closure (the 2+ branch is
// unreachable for them); multi-wrapper ops pass a closure that
// constructs their per-op error type.
const dispatchTemplateText = `{{- $op := .Operation -}}
{{- $hasPartial := .HasPartialFailures -}}
{{- range .Routes}}
{{- if .Forward}}
{{- if .Deprecated}}
// Deprecated: use {{.Forward}} instead. This compatibility forwarder will be removed in a future major version.
{{- else}}
{{opMethodComment .MethodName $op}}
{{- end}}
func (c {{.ReceiverType}}) {{.MethodName}}(ctx context.Context, req {{if $op.IsPointerReq}}*{{end}}{{$op.TypePrefix}}Req) ({{- ""}}
	{{- if $op.IsNoBody}}*opensearch.Response{{else}}*{{$op.TypePrefix}}Resp{{end}}, error) {
	return c.{{.Forward}}(ctx, req)
}
{{- else}}
{{- if .Deprecated}}
// Deprecated: use {{$op.TypePrefix}} via the parent client instead.
{{- else}}
{{opMethodComment .MethodName $op}}
{{- end}}
func (c {{.ReceiverType}}) {{.MethodName}}(ctx context.Context, req {{if $op.IsPointerReq}}*{{end}}{{$op.TypePrefix}}Req) ({{- ""}}
	{{- if $op.IsNoBody}}*opensearch.Response{{else}}*{{$op.TypePrefix}}Resp{{end}}, error) {
{{- if $op.IsPointerReq}}
	if req == nil {
		req = &{{$op.TypePrefix}}Req{}
	}
{{end}}
{{- if $op.IsNoBody}}
	return request(ctx, {{if .TopLevel}}&c{{else}}c.apiClient{{end}}, {{methodConst (primaryMethod $op)}}, req, noBody)
{{- else}}
	var (
		data {{$op.TypePrefix}}Resp
		err  error
	)
{{- if bodyMethodSwitch $op}}
	method := {{methodConst (primaryMethod $op)}}
	if req.Body != nil{{if $op.HasTypedBody}} || req.BodyReader != nil{{end}} {
		method = {{methodConst (bodyMethodSwitch $op)}}
	}
	if data.response, err = request( {{- ""}}
		ctx,
		{{if .TopLevel}}&c{{else}}c.apiClient{{end}},
		method,
		req, &data,
	); err != nil {
		return &data, err
	}
{{- else}}
	if data.response, err = request( {{- ""}}
		ctx,
		{{if .TopLevel}}&c{{else}}c.apiClient{{end}},
		{{methodConst (primaryMethod $op)}},
		req, &data,
	); err != nil {
		return &data, err
	}
{{- end}}
{{- if $hasPartial}}
{{- $recv := recvTopLevel}}{{if not .TopLevel}}{{$recv = recvSubClient}}{{end}}
	return &data, collapsePerOpErrors(data.PartialFailures({{$recv}}.errorMask()), {{if $.PerOpErrType}}func(errs []error) error {
		return &{{$.PerOpErrType}}{errs: errs}
	}{{else}}nil{{end}})
{{- else}}
	return &data, nil
{{- end}}
{{- end}}
}
{{- end}}
{{end}}`
