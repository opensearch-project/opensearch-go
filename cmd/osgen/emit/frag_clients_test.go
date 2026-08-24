// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/emit"
	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

func TestClientsFragment_Body(t *testing.T) {
	t.Parallel()

	clients := []emit.SubClient{
		{TypeName: "CatClient", FieldName: "Cat", Parent: "Client"},
		{TypeName: "IndicesClient", FieldName: "Indices", Parent: "Client"},
		{TypeName: "AliasClient", FieldName: "Alias", Parent: "IndicesClient"},
	}

	frag := &emit.ClientsFragment{SubClients: clients}

	body, err := frag.Body()
	require.NoError(t, err)

	checks := []struct {
		name   string
		want   string
		absent bool
	}{
		{name: "Client struct", want: "type Client struct"},
		{name: "Client field", want: "Client *opensearch.Client"},
		{name: "errors mask field", want: "errors *errMaskWidth"},
		{name: "top-level Cat", want: "Cat CatClient"},
		{name: "top-level Indices", want: "Indices IndicesClient"},
		{name: "clientInit", want: "func clientInit(rootClient *opensearch.Client, mask errmask.ErrorMask) *Client"},
		{name: "errors init", want: "errors: newErrMask(mask),"},
		{name: "init Cat", want: "client.Cat = CatClient{apiClient: client}"},
		{name: "init Indices", want: "client.Indices = IndicesClient{apiClient: client}"},
		{name: "nested init Alias", want: "client.Indices.Alias = AliasClient{apiClient: client}"},
		{name: "CatClient struct", want: "type CatClient struct"},
		{name: "IndicesClient struct", want: "type IndicesClient struct"},
		{name: "AliasClient struct", want: "type AliasClient struct"},
		{name: "nested Alias field", want: "Alias AliasClient"},
		{name: "Inspect alias", want: "type Inspect = apiutil.Inspect"},
		{name: "noBody sentinel", want: "var noBody *opensearch.NoBody"},
	}

	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.absent {
				require.NotContains(t, body, tc.want, "should not contain %q", tc.want)
			} else {
				require.Contains(t, body, tc.want, "missing %q", tc.want)
			}
		})
	}
}

// TestClientsFragment_Body_Aliases verifies that a top-level sub-client with
// Aliases emits an extra field per alias and an init statement assigning each
// alias to the canonical field, with only one struct type declared.
func TestClientsFragment_Body_Aliases(t *testing.T) {
	t.Parallel()

	clients := []emit.SubClient{
		{TypeName: "DocumentClient", FieldName: "Doc", Parent: "Client", Aliases: []string{"Document"}},
		{TypeName: "PointInTimeClient", FieldName: "PIT", Parent: "Client", Aliases: []string{"PointInTime"}},
	}

	frag := &emit.ClientsFragment{SubClients: clients}
	body, err := frag.Body()
	require.NoError(t, err)

	// Canonical + alias fields, both typed as the same sub-client.
	require.Contains(t, body, "Doc DocumentClient")
	require.Contains(t, body, "Document DocumentClient")
	require.Contains(t, body, "PIT PointInTimeClient")
	require.Contains(t, body, "PointInTime PointInTimeClient")

	// Canonical init plus alias assignment to the canonical field.
	require.Contains(t, body, "client.Doc = DocumentClient{apiClient: client}")
	require.Contains(t, body, "client.Document = client.Doc")
	require.Contains(t, body, "client.PIT = PointInTimeClient{apiClient: client}")
	require.Contains(t, body, "client.PointInTime = client.PIT")

	// The aliased type is declared exactly once (no duplicate struct decl).
	require.Equal(t, 1, strings.Count(body, "type DocumentClient struct"))
	require.Equal(t, 1, strings.Count(body, "type PointInTimeClient struct"))
}

// TestClientsFragment_Body_AliasInitOrder verifies that a top-level alias is
// assigned AFTER the canonical sub-client's nested children, so the alias copy
// captures a fully-populated value (the indices sub-client has nested
// Alias/Mapping/Settings children).
func TestClientsFragment_Body_AliasInitOrder(t *testing.T) {
	t.Parallel()

	clients := []emit.SubClient{
		{TypeName: "IndicesClient", FieldName: "Index", Parent: "Client", Aliases: []string{"Indices", "Indexes"}},
		{TypeName: "AliasClient", FieldName: "Alias", Parent: "IndicesClient"},
	}

	frag := &emit.ClientsFragment{SubClients: clients}
	body, err := frag.Body()
	require.NoError(t, err)

	// Nested child is assigned onto the canonical field.
	require.Contains(t, body, "client.Index.Alias = AliasClient{apiClient: client}")
	// Aliases copy the canonical field.
	require.Contains(t, body, "client.Indices = client.Index")
	require.Contains(t, body, "client.Indexes = client.Index")

	// The nested-child assignment must come BEFORE the alias copies, otherwise
	// client.Indices.Alias would be a zero value.
	nestedIdx := strings.Index(body, "client.Index.Alias = AliasClient{apiClient: client}")
	indicesAliasIdx := strings.Index(body, "client.Indices = client.Index")
	indexesAliasIdx := strings.Index(body, "client.Indexes = client.Index")
	require.Positive(t, nestedIdx)
	require.Less(t, nestedIdx, indicesAliasIdx, "nested child must be assigned before the Indices alias copy")
	require.Less(t, nestedIdx, indexesAliasIdx, "nested child must be assigned before the Indexes alias copy")
}

func TestClientsFragment_Imports(t *testing.T) {
	t.Parallel()

	frag := &emit.ClientsFragment{SubClients: []emit.SubClient{
		{TypeName: "CatClient", FieldName: "Cat", Parent: "Client"},
	}}

	imps := frag.Imports()
	require.Len(t, imps, 3)
}

func TestNewClientsFile_Render(t *testing.T) {
	t.Parallel()

	clients := []emit.SubClient{
		{TypeName: "CatClient", FieldName: "Cat", Parent: "Client"},
		{TypeName: "IndicesClient", FieldName: "Indices", Parent: "Client"},
		{TypeName: "AliasClient", FieldName: "Alias", Parent: "IndicesClient"},
	}

	target := emit.NewClientsFile("/tmp/test", ir.DefaultCorePkgName, clients)
	require.NotNil(t, target)

	src, err := target.Render()
	require.NoError(t, err)

	output := string(src)
	require.Contains(t, output, "package "+ir.DefaultCorePkgName)
	require.Contains(t, output, `"github.com/opensearch-project/opensearch-go/v5"`)
	require.Contains(t, output, `"github.com/opensearch-project/opensearch-go/v5/internal/apiutil"`)
	require.Contains(t, output, `"github.com/opensearch-project/opensearch-go/v5/errmask"`)
}

func TestNewClientsFile_NilWhenEmpty(t *testing.T) {
	t.Parallel()

	target := emit.NewClientsFile("/tmp/test", ir.DefaultCorePkgName, nil)
	require.Nil(t, target)
}
