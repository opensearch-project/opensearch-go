// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// testifyCompareFuncs are the testify assert/require helpers that compare two
// values of static type `any` at run time and fail on a type mismatch. The
// values are the two argument indices (0-based, counting the *testing.T-like
// first arg) that get compared against each other.
//
//nolint:gochecknoglobals // const-ish lookup table, immutable after init
var testifyCompareFuncs = map[string][2]int{
	"Equal":          {1, 2},
	"Equalf":         {1, 2},
	"NotEqual":       {1, 2},
	"NotEqualf":      {1, 2},
	"Greater":        {1, 2},
	"Greaterf":       {1, 2},
	"GreaterOrEqual": {1, 2},
	"Less":           {1, 2},
	"LessOrEqual":    {1, 2},
}

// TypedAssertAnalyzer reports testify comparison calls whose two operands have
// mismatched static types - the type-erasure hazard described in main's doc
// comment. It is deliberately conservative: it only fires when both operand
// types are statically known and genuinely incompatible for a testify
// comparison, and stays silent when either side is an interface (the dynamic
// type is unknowable) to avoid false positives.
//
//nolint:gochecknoglobals // analyzer definition, immutable after init
var TypedAssertAnalyzer = &analysis.Analyzer{
	Name:     "typedassert",
	Doc:      "reports testify comparisons whose operands have mismatched static types (v4->v5 upgrade hazard)",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      runTypedAssert,
}

func runTypedAssert(pass *analysis.Pass) (any, error) {
	insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	insp.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
		call := n.(*ast.CallExpr)

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return
		}
		idxs, ok := testifyCompareFuncs[sel.Sel.Name]
		if !ok {
			return
		}
		if !isTestifyCall(pass, sel) {
			return
		}
		if len(call.Args) <= idxs[1] {
			return
		}

		xT := pass.TypesInfo.TypeOf(call.Args[idxs[0]])
		yT := pass.TypesInfo.TypeOf(call.Args[idxs[1]])
		if xT == nil || yT == nil {
			return
		}

		if reason, mismatched := compareMismatch(xT, yT); mismatched {
			diag := analysis.Diagnostic{
				Pos: call.Pos(),
				Message: "testify " + sel.Sel.Name + ": operands have mismatched types (" +
					xT.String() + " vs " + yT.String() + "); " + reason +
					". This compiles but fails at run time. Convert to a common type " +
					"(dereference a pointer, or type the literal, e.g. int64(0)).",
			}
			if fix, ok := retypeLiteralFix(pass, call.Args[idxs[0]], call.Args[idxs[1]]); ok {
				diag.SuggestedFixes = []analysis.SuggestedFix{fix}
			}
			pass.Report(diag)
		}
	})

	return nil, nil //nolint:nilnil // go/analysis Run returns (nil result, nil error) when it produces no fact
}

// retypeLiteralFix offers a safe autofix for the common case where exactly one
// operand is a constant literal (e.g. 0, 5, or int64(0)) and the other has a
// predeclared numeric basic type: it rewrites the literal to a conversion in the
// other operand's type, so both sides share a dynamic type at run time.
//
// It deliberately does NOT fix pointer mismatches - dereferencing requires a
// nil-check that cannot be mechanically inserted safely - so those diagnostics
// are reported without a suggested fix.
func retypeLiteralFix(pass *analysis.Pass, x, y ast.Expr) (analysis.SuggestedFix, bool) {
	lit, target, ok := pickLiteralAndTarget(pass, x, y)
	if !ok {
		return analysis.SuggestedFix{}, false
	}
	tv := pass.TypesInfo.Types[lit]
	if tv.Value == nil {
		return analysis.SuggestedFix{}, false // not a constant; can't retype safely
	}
	newText := target.Name() + "(" + tv.Value.String() + ")"
	return analysis.SuggestedFix{
		Message: "convert the literal to " + target.Name(),
		TextEdits: []analysis.TextEdit{{
			Pos:     lit.Pos(),
			End:     lit.End(),
			NewText: []byte(newText),
		}},
	}, true
}

// pickLiteralAndTarget returns the operand that is a constant literal and the
// predeclared numeric basic type of the other operand, if exactly one operand
// is such a literal and the other is a non-pointer predeclared numeric basic.
func pickLiteralAndTarget(pass *analysis.Pass, x, y ast.Expr) (ast.Expr, *types.Basic, bool) {
	xBasic, xPre := predeclaredNumericBasic(pass.TypesInfo.TypeOf(x))
	yBasic, yPre := predeclaredNumericBasic(pass.TypesInfo.TypeOf(y))

	xConst := pass.TypesInfo.Types[x].Value != nil
	yConst := pass.TypesInfo.Types[y].Value != nil

	switch {
	case xConst && !yConst && yPre:
		return x, yBasic, true
	case yConst && !xConst && xPre:
		return y, xBasic, true
	default:
		return nil, nil, false
	}
}

// predeclaredNumericBasic reports whether t is a predeclared numeric basic type
// (int, int64, float64, ...) - one whose name is a valid builtin identifier, so a
// conversion T(x) is always in scope without an import.
func predeclaredNumericBasic(t types.Type) (*types.Basic, bool) {
	if t == nil {
		return nil, false
	}
	b, ok := t.(*types.Basic) // predeclared basics are unnamed at top level
	if !ok || !numericKind(b) {
		return nil, false
	}
	return b, true
}

// isTestifyCall reports whether sel refers to a function in
// github.com/stretchr/testify's assert or require package.
func isTestifyCall(pass *analysis.Pass, sel *ast.SelectorExpr) bool {
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	pkgName, ok := pass.TypesInfo.Uses[id].(*types.PkgName)
	if !ok {
		return false
	}
	path := pkgName.Imported().Path()
	return path == "github.com/stretchr/testify/require" ||
		path == "github.com/stretchr/testify/assert"
}

// compareMismatch reports whether testify would treat x and y as different
// types at run time. testify's ObjectsAreEqual falls back to reflect.DeepEqual
// for non-[]byte values, and DeepEqual is false whenever the dynamic types
// differ; the ordered comparisons (Greater/Less) likewise require an identical
// numeric kind. We flag only statically knowable, certain mismatches.
func compareMismatch(x, y types.Type) (string, bool) {
	// If either operand is an interface, the dynamic type is unknown at compile
	// time; don't guess.
	if types.IsInterface(x) || types.IsInterface(y) {
		return "", false
	}

	xu, yu := x.Underlying(), y.Underlying()

	xPtr, xIsPtr := xu.(*types.Pointer)
	yPtr, yIsPtr := yu.(*types.Pointer)

	// Pointer vs non-pointer: the classic *int64 vs int literal. Always wrong
	// for a value comparison.
	if xIsPtr != yIsPtr {
		return "one side is a pointer and the other is not", true
	}

	// Both pointers: compare the pointed-to types.
	if xIsPtr && yIsPtr {
		return compareMismatch(xPtr.Elem(), yPtr.Elem())
	}

	xBasic, xIsBasic := xu.(*types.Basic)
	yBasic, yIsBasic := yu.(*types.Basic)

	// Two numeric basics of different kinds (int vs int64, float64 vs int):
	// testify's DeepEqual sees different dynamic types and fails.
	if xIsBasic && yIsBasic {
		if numericKind(xBasic) && numericKind(yBasic) && xBasic.Kind() != yBasic.Kind() {
			return "the two numeric kinds differ", true
		}
	}

	return "", false
}

func numericKind(b *types.Basic) bool {
	return b.Info()&(types.IsInteger|types.IsFloat|types.IsUnsigned) != 0
}
