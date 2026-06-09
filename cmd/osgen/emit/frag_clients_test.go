// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/emit"
	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

func TestClientsFragment_Body(t *testing.T) {
	t.Parallel()

	clients := []emit.SubClient{
		{TypeName: "catClient", FieldName: "Cat", Parent: "Client"},
		{TypeName: "indicesClient", FieldName: "Indices", Parent: "Client"},
		{TypeName: "aliasClient", FieldName: "Alias", Parent: "indicesClient"},
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
		{name: "top-level Cat", want: "Cat catClient"},
		{name: "top-level Indices", want: "Indices indicesClient"},
		{name: "clientInit", want: "func clientInit(rootClient *opensearch.Client, mask errmask.ErrorMask) *Client"},
		{name: "errors init", want: "errors: newErrMask(mask),"},
		{name: "init Cat", want: "client.Cat = catClient{apiClient: client}"},
		{name: "init Indices", want: "client.Indices = indicesClient{apiClient: client}"},
		{name: "nested init Alias", want: "client.Indices.Alias = aliasClient{apiClient: client}"},
		{name: "catClient struct", want: "type catClient struct"},
		{name: "indicesClient struct", want: "type indicesClient struct"},
		{name: "aliasClient struct", want: "type aliasClient struct"},
		{name: "nested Alias field", want: "Alias aliasClient"},
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

func TestClientsFragment_Imports(t *testing.T) {
	t.Parallel()

	frag := &emit.ClientsFragment{SubClients: []emit.SubClient{
		{TypeName: "catClient", FieldName: "Cat", Parent: "Client"},
	}}

	imps := frag.Imports()
	require.Len(t, imps, 3)
}

func TestNewClientsFile_Render(t *testing.T) {
	t.Parallel()

	clients := []emit.SubClient{
		{TypeName: "catClient", FieldName: "Cat", Parent: "Client"},
		{TypeName: "indicesClient", FieldName: "Indices", Parent: "Client"},
		{TypeName: "aliasClient", FieldName: "Alias", Parent: "indicesClient"},
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
