// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit

import "github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"

// Test bridges for unexported helpers used by black-box tests.
var (
	LowerFirst              = lowerFirst
	SplitFirstLine          = splitFirstLine
	SplitUnionsFromSiblings = splitUnionsFromSiblings

	// frag_dispatch.go helpers (white-box-style coverage from
	// _test.go files in the emit package; the test file lives in
	// emit_test so these names cross the boundary as exports).
	LookupResponseField   = lookupResponseField
	ElementTypeHasShards  = elementTypeHasShards
	ApplyHasShards        = applyHasShards
	ApplyBulkItems        = applyBulkItems
	ApplyMultiSearchItems = applyMultiSearchItems
	ApplySearchShards     = applySearchShards
	ShardsIsPointer       = shardsIsPointer
	WriteOperationConst   = writeOperationConst
	PerOpErrorTypeName    = perOpErrorTypeName

	// frag_partial_failure.go helper.
	WrapperMethodName = wrapperMethodName

	// frag_union.go helpers.
	TokenClassStr = tokenClassStr
	QuotedKeys    = quotedKeys
)

// UnionFromResponses returns the resolved union shape for the test
// caller as three strings (UnionName, Success, ErrorBranch). Returning
// strings rather than the unexported unionShape avoids leaking the
// type through export_test.go.
func UnionFromResponses(resp *ir.Type, reg *ir.TypeRegistry) (string, string, string) {
	u := unionFromResponses(resp, reg)
	return u.unionName, u.success, u.errorBranch
}

// ResolveUnionShape mirrors UnionFromResponses for direct calls
// against a TypeUnion / TypeLazyUnion (skipping the Responses-field
// indirection). Returns (unionName, success, errorBranch).
func ResolveUnionShape(t *ir.Type, reg *ir.TypeRegistry) (string, string, string) {
	u := resolveUnionShape(t, reg)
	return u.unionName, u.success, u.errorBranch
}

// ImportSet exposes the unexported importSet type to black-box tests.
type ImportSet = importSet

// ImportGroup exposes importGroup to black-box tests.
var ImportGroup = importGroup

// BulkInnerItemType exposes bulkInnerItemType for the test caller
// without leaking the (string, bool) signature through the var-block.
func BulkInnerItemType(resp *ir.Type, reg *ir.TypeRegistry) (string, bool) {
	return bulkInnerItemType(resp, reg)
}
