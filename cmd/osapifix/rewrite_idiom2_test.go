// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/require"
)

// parseCall parses src as a single expression and returns its *ast.CallExpr.
func parseCall(t *testing.T, src string) *ast.CallExpr {
	t.Helper()
	e, err := parser.ParseExpr(src)
	require.NoError(t, err)
	call, ok := e.(*ast.CallExpr)
	require.True(t, ok, "expr is not a *ast.CallExpr: %T", e)
	return call
}

// mustFormat renders a synthetic AST node to source, matching writeFormatted.
func mustFormat(t *testing.T, n ast.Node) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, format.Node(&buf, token.NewFileSet(), n))
	return buf.String()
}

// rootIdent walks a selector spine down to its base *ast.Ident (the receiver).
func rootIdent(t *testing.T, sel *ast.SelectorExpr) ast.Expr {
	t.Helper()
	var cur ast.Expr = sel
	for {
		s, ok := cur.(*ast.SelectorExpr)
		if !ok {
			return cur
		}
		cur = s.X
	}
}

func TestRewriteIdiom2Call_PingWithContext(t *testing.T) {
	call := parseCall(t, `client.Ping(client.Ping.WithContext(ctx))`)
	root := call.Fun.(*ast.SelectorExpr).X // "client"
	out, edits := rewriteIdiom2Call(call, root, []string{"Ping"}, "opensearchapi")
	require.NotEmpty(t, edits)
	require.Equal(t, `client.Ping(ctx, &opensearchapi.PingReq{})`, mustFormat(t, out))
}

func TestRewriteIdiom2Call_IndicesExistsWithContext(t *testing.T) {
	call := parseCall(t, `client.Indices.Exists(idx, client.Indices.Exists.WithContext(ctx))`)
	root := rootIdent(t, call.Fun.(*ast.SelectorExpr)) // "client"
	out, _ := rewriteIdiom2Call(call, root, []string{"Indices", "Exists"}, "opensearchapi")
	require.Equal(t, `client.Indices.Exists(ctx, opensearchapi.IndicesExistsReq{Indices: idx})`, mustFormat(t, out))
}

func TestRewriteIdiom2Call_FilterPathMarker(t *testing.T) {
	call := parseCall(t, `client.Ping(client.Ping.WithContext(ctx), client.Ping.WithFilterPath("a"))`)
	root := call.Fun.(*ast.SelectorExpr).X
	out, _ := rewriteIdiom2Call(call, root, []string{"Ping"}, "opensearchapi")
	got := mustFormat(t, out)
	require.Contains(t, got, "_OSAPIFIX_RESOLVE")
	require.Contains(t, got, "WithFilterPath") // salvage names the dropped option
}

func TestRewriteIdiom2Call_MissingContextMarker(t *testing.T) {
	call := parseCall(t, `client.Ping()`)
	root := call.Fun.(*ast.SelectorExpr).X
	out, _ := rewriteIdiom2Call(call, root, []string{"Ping"}, "opensearchapi")
	require.Contains(t, mustFormat(t, out), "_OSAPIFIX_RESOLVE") // won't invent ctx
}

func TestRewriteIdiom2Call_ParamsEmission(t *testing.T) {
	call := parseCall(t, `client.Indices.Exists(idx, client.Indices.Exists.WithContext(ctx), client.Indices.Exists.WithLocal(true))`)
	root := rootIdent(t, call.Fun.(*ast.SelectorExpr))
	out, _ := rewriteIdiom2Call(call, root, []string{"Indices", "Exists"}, "opensearchapi")
	require.Equal(t,
		`client.Indices.Exists(ctx, opensearchapi.IndicesExistsReq{Indices: idx, Params: opensearchapi.IndicesExistsParams{Local: true}})`,
		mustFormat(t, out))
}

func TestRewriteIdiom2Call_Unrecognized(t *testing.T) {
	call := parseCall(t, `client.Bogus(client.Bogus.WithContext(ctx))`)
	root := call.Fun.(*ast.SelectorExpr).X
	out, edits := rewriteIdiom2Call(call, root, []string{"Bogus"}, "opensearchapi")
	require.Nil(t, out)
	require.Nil(t, edits)
}
