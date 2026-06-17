// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/emit"
	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

func TestPluginClientFragment_Body(t *testing.T) {
	t.Parallel()

	ops := []emit.PluginClientOp{
		{MethodName: "GetRoles", TypePrefix: "SecurityGetRoles", IsPointerReq: true},
		{MethodName: "CreateRole", TypePrefix: "SecurityCreateRole", IsPointerReq: false},
		{MethodName: "DeleteRole", TypePrefix: "SecurityDeleteRole", IsPointerReq: true, IsNoBody: true},
	}

	frag := &emit.PluginClientFragment{Ops: ops}

	body, err := frag.Body()
	require.NoError(t, err)

	checks := []struct {
		name string
		want string
	}{
		{name: "Client struct", want: "type Client struct"},
		{name: "NewClient", want: "func NewClient(client *opensearch.Client) *Client"},
		{name: "do helper", want: "func do[T any](ctx context.Context"},
		{name: "pointer req method", want: "func (c *Client) GetRoles(ctx context.Context, req *SecurityGetRolesReq)"},
		{name: "value req method", want: "func (c *Client) CreateRole(ctx context.Context, req SecurityCreateRoleReq)"},
		{name: "nil guard", want: "if req == nil"},
		{name: "noBody return type", want: "*opensearch.Response"},
		{name: "noBody sentinel", want: "var noBody *struct{}"},
		{name: "typed resp", want: "*SecurityGetRolesResp"},
	}

	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Contains(t, body, tc.want)
		})
	}
}

func TestPluginClientFragment_Imports(t *testing.T) {
	t.Parallel()

	frag := &emit.PluginClientFragment{Ops: []emit.PluginClientOp{{MethodName: "Get", TypePrefix: "X"}}}
	imps := frag.Imports()

	paths := make(map[string]bool)
	for _, imp := range imps {
		paths[imp.Path] = true
	}

	require.True(t, paths["context"], "missing context import")
	require.True(t, paths["fmt"], "missing fmt import")
	require.True(t, paths["github.com/opensearch-project/opensearch-go/v5"], "missing opensearch import")
}

func TestPluginTestHelperFragment_Body(t *testing.T) {
	t.Parallel()

	frag := &emit.PluginTestHelperFragment{
		Pkg:          "ossecurity",
		PluginImport: "github.com/opensearch-project/opensearch-go/v5/plugins/security",
		CoreImport:   "github.com/opensearch-project/opensearch-go/v5",
		CorePkg:      ir.DefaultCorePkgName,
	}

	body, err := frag.Body()
	require.NoError(t, err)

	checks := []struct {
		name string
		want string
	}{
		{name: "NewClient func", want: "func NewClient(t *testing.T) (*ossecurity.Client, error)"},
		{name: "CreateFailingClient func", want: "func CreateFailingClient(t *testing.T) (*ossecurity.Client, error)"},
		{name: "testutil config", want: "testutil.ClientConfig(t)"},
		{name: "plugin NewClient call", want: "ossecurity.NewClient(osClient)"},
	}

	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Contains(t, body, tc.want)
		})
	}
}

func TestNewPluginClientFile_Render(t *testing.T) {
	t.Parallel()

	ops := []*ir.Operation{
		{Group: "security.get_roles", MethodName: "GetRoles", TypePrefix: "SecurityGetRoles", IsPointerReq: true},
		{Group: "security.create_role", MethodName: "CreateRole", TypePrefix: "SecurityCreateRole", IsPointerReq: false},
	}

	target := emit.NewPluginClientFile("/tmp/test", "ossecurity", ops, nil)
	require.NotNil(t, target)

	src, err := target.Render()
	require.NoError(t, err)

	output := string(src)
	require.Contains(t, output, "package ossecurity")
	require.Contains(t, output, "func (c *Client) GetRoles")
	require.Contains(t, output, "func (c *Client) CreateRole")
}

func TestPluginClientFragment_WithSubClients(t *testing.T) {
	t.Parallel()

	roleSC := &emit.PluginSubClient{TypeName: "roleClient", FieldName: "Role"}

	ops := []emit.PluginClientOp{
		{MethodName: "FlushCache", TypePrefix: "SecurityFlushCache", IsPointerReq: true, IsNoBody: true, HTTPMethod: "http.MethodDelete"},
		{MethodName: "GetRole", TypePrefix: "SecurityGetRole", IsPointerReq: true, HTTPMethod: "http.MethodGet", SubClient: roleSC},
		{MethodName: "CreateRole", TypePrefix: "SecurityCreateRole", IsPointerReq: false, HTTPMethod: "http.MethodPut", SubClient: roleSC},
		{
			MethodName:   "DeleteRole",
			TypePrefix:   "SecurityDeleteRole",
			IsPointerReq: true,
			IsNoBody:     true,
			HTTPMethod:   "http.MethodDelete",
			SubClient:    roleSC,
		},
	}

	frag := &emit.PluginClientFragment{
		Ops:        ops,
		SubClients: []emit.PluginSubClient{*roleSC},
	}

	body, err := frag.Body()
	require.NoError(t, err)

	checks := []struct {
		name string
		want string
	}{
		{name: "sub-client field on Client", want: "Role roleClient"},
		{name: "sub-client struct", want: "type roleClient struct"},
		{name: "sub-client back pointer", want: "client *Client"},
		{name: "NewClient init", want: "c.Role = roleClient{client: c}"},
		{name: "root method", want: "func (c *Client) FlushCache("},
		{name: "sub-client method GetRole", want: "func (c roleClient) GetRole("},
		{name: "sub-client method CreateRole", want: "func (c roleClient) CreateRole("},
		{name: "sub-client uses c.client", want: "do(ctx, c.client,"},
		{name: "deprecated GetRole", want: "return c.Role.GetRole(ctx, req)"},
		{name: "deprecated CreateRole", want: "return c.Role.CreateRole(ctx, req)"},
		{name: "deprecated comment", want: "Deprecated: use Client.Role.GetRole instead"},
	}

	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Contains(t, body, tc.want)
		})
	}
}

func TestNewPluginClientFile_WithSubClients(t *testing.T) {
	t.Parallel()

	roleSC := &emit.PluginSubClient{TypeName: "roleClient", FieldName: "Role"}

	ops := []*ir.Operation{
		{
			Group: "security.flush_cache", MethodName: "FlushCache", TypePrefix: "SecurityFlushCache",
			IsPointerReq: true, IsNoBody: true, HTTPMethods: []string{http.MethodDelete},
		},
		{
			Group: "security.get_role", MethodName: "GetRole", TypePrefix: "SecurityGetRole",
			IsPointerReq: true, HTTPMethods: []string{http.MethodGet},
		},
		{
			Group: "security.create_role", MethodName: "CreateRole", TypePrefix: "SecurityCreateRole",
			IsPointerReq: false, HTTPMethods: []string{http.MethodPut},
		},
	}

	byGroup := map[string]*emit.PluginSubClient{
		"security.get_role":    roleSC,
		"security.create_role": roleSC,
	}

	target := emit.NewPluginClientFile("/tmp/test", "ossecurity", ops, byGroup)
	require.NotNil(t, target)

	src, err := target.Render()
	require.NoError(t, err)

	output := string(src)
	require.Contains(t, output, "package ossecurity")
	require.Contains(t, output, "roleClient")
	require.Contains(t, output, "func (c *Client) FlushCache(")
	require.Contains(t, output, "func (c roleClient) GetRole(")
	require.Contains(t, output, "func (c roleClient) CreateRole(")
}
