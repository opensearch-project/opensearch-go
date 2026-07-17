// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

func TestExtractResourceNoun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		suffix string
		want   string
	}{
		{name: "get prefix", suffix: "get_action_group", want: "action_group"},
		{name: "create prefix", suffix: "create_role", want: "role"},
		{name: "delete prefix", suffix: "delete_user", want: "user"},
		{name: "patch prefix", suffix: "patch_roles", want: "roles"},
		{name: "update prefix", suffix: "update_connector", want: "connector"},
		{name: "get_all prefix", suffix: "get_all_certificates", want: "certificates"},
		{name: "search prefix", suffix: "search_models", want: "models"},
		{name: "execute prefix", suffix: "execute_agent", want: "agent"},
		{name: "deploy prefix", suffix: "deploy_model", want: "model"},
		{name: "register prefix", suffix: "register_model", want: "model"},
		{name: "reload prefix", suffix: "reload_http_certificates", want: "http_certificates"},
		{name: "create_update prefix", suffix: "create_update_tenancy_config", want: "tenancy_config"},
		{name: "no verb", suffix: "authinfo", want: ""},
		{name: "no verb health", suffix: "health", want: ""},
		{name: "no verb migrate", suffix: "migrate", want: ""},
		{name: "flush prefix", suffix: "flush_cache", want: "cache"},
		{name: "generate prefix", suffix: "generate_obo_token", want: "obo_token"},
		{name: "post prefix", suffix: "post_dashboards_info", want: "dashboards_info"},
		{name: "change prefix", suffix: "change_password", want: "password"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, extractResourceNoun(tt.suffix))
		})
	}
}

func TestNormalizeNoun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		noun string
		want string
	}{
		{name: "singular", noun: "action_group", want: "action_group"},
		{name: "plural trailing s", noun: "action_groups", want: "action_group"},
		{name: "plural roles", noun: "roles", want: "role"},
		{name: "plural mappings", noun: "role_mappings", want: "role_mapping"},
		{name: "plural users", noun: "users", want: "user"},
		{name: "plural users_legacy", noun: "users_legacy", want: "user_legacy"},
		{name: "ies plural", noun: "memories", want: "memory"},
		{name: "no change ss", noun: "address", want: "address"},
		{name: "no change us", noun: "status", want: "status"},
		{name: "no change is", noun: "analysis", want: "analysis"},
		{name: "ses plural", noun: "aliases", want: "alias"},
		{name: "certificates", noun: "certificates", want: "certificate"},
		{name: "connectors", noun: "connectors", want: "connector"},
		{name: "agents", noun: "agents", want: "agent"},
		{name: "irregular caches", noun: "caches", want: "cache"},
		{name: "irregular indices", noun: "indices", want: "index"},
		{name: "ches plural batches", noun: "batches", want: "batch"},
		{name: "ches plural matches", noun: "matches", want: "match"},
		{name: "ches plural branches", noun: "branches", want: "branch"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, normalizeNoun(tt.noun))
		})
	}
}

func TestResourceToTypeName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		resource string
		want     string
	}{
		{name: "simple", resource: "role", want: "RoleClient"},
		{name: "compound", resource: "action_group", want: "ActionGroupClient"},
		{name: "triple", resource: "role_mapping", want: "RoleMappingClient"},
		{name: "long", resource: "distinguished_name", want: "DistinguishedNameClient"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, resourceToTypeName(tt.resource))
		})
	}
}

func TestResourceToFieldName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		resource string
		want     string
	}{
		{name: "simple", resource: "role", want: "Role"},
		{name: "compound", resource: "action_group", want: "ActionGroup"},
		{name: "triple", resource: "role_mapping", want: "RoleMapping"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, resourceToFieldName(tt.resource))
		})
	}
}

func TestResolvePluginSubClients_Security(t *testing.T) {
	t.Parallel()

	groups := []string{
		"security.authinfo",
		"security.authtoken",
		"security.change_password",
		"security.create_action_group",
		"security.create_role",
		"security.create_role_mapping",
		"security.create_tenant",
		"security.create_user",
		"security.delete_action_group",
		"security.delete_role",
		"security.delete_role_mapping",
		"security.delete_tenant",
		"security.delete_user",
		"security.flush_cache",
		"security.get_action_group",
		"security.get_action_groups",
		"security.get_role",
		"security.get_role_mapping",
		"security.get_role_mappings",
		"security.get_roles",
		"security.get_tenant",
		"security.get_tenants",
		"security.get_user",
		"security.get_users",
		"security.health",
		"security.patch_action_group",
		"security.patch_action_groups",
		"security.patch_role",
		"security.patch_role_mapping",
		"security.patch_role_mappings",
		"security.patch_roles",
		"security.patch_tenant",
		"security.patch_tenants",
		"security.patch_user",
		"security.patch_users",
	}

	result := resolvePluginSubClients(groups)

	// Verify sub-clients exist for multi-operation resources.
	scByField := make(map[string]pluginSubClientInfo)
	for _, sc := range result.SubClients {
		scByField[sc.FieldName] = sc
	}

	require.Contains(t, scByField, "ActionGroup")
	require.Contains(t, scByField, "Role")
	require.Contains(t, scByField, "RoleMapping")
	require.Contains(t, scByField, "Tenant")
	require.Contains(t, scByField, "User")

	require.Equal(t, "ActionGroupClient", scByField["ActionGroup"].TypeName)
	require.Equal(t, "RoleClient", scByField["Role"].TypeName)

	// Verify assignments.
	require.Equal(t, "ActionGroup", result.Assignment["security.get_action_group"])
	require.Equal(t, "ActionGroup", result.Assignment["security.get_action_groups"])
	require.Equal(t, "ActionGroup", result.Assignment["security.create_action_group"])
	require.Equal(t, "Role", result.Assignment["security.get_role"])
	require.Equal(t, "Role", result.Assignment["security.get_roles"])
	require.Equal(t, "RoleMapping", result.Assignment["security.get_role_mapping"])
	require.Equal(t, "User", result.Assignment["security.patch_users"])

	// Operations without a verb stay flat (empty assignment).
	require.Empty(t, result.Assignment["security.authinfo"])
	require.Empty(t, result.Assignment["security.health"])
}

func TestResolvePluginSubClients_SmallPlugin(t *testing.T) {
	t.Parallel()

	groups := []string{
		"asynchronous_search.delete",
		"asynchronous_search.get",
		"asynchronous_search.search",
		"asynchronous_search.stats",
	}

	result := resolvePluginSubClients(groups)

	// Small plugin with no verb-prefixed suffixes should have no sub-clients.
	require.Empty(t, result.SubClients)
	for _, g := range groups {
		require.Empty(t, result.Assignment[g])
	}
}

func TestResolvePluginSubClients_PluralGrouping(t *testing.T) {
	t.Parallel()

	groups := []string{
		"security.get_user",
		"security.get_users",
		"security.create_user",
		"security.get_user_legacy",
		"security.get_users_legacy",
		"security.create_user_legacy",
		"security.delete_user_legacy",
	}

	result := resolvePluginSubClients(groups)

	// user and users should normalize to the same resource.
	require.Equal(t, "User", result.Assignment["security.get_user"])
	require.Equal(t, "User", result.Assignment["security.get_users"])
	require.Equal(t, "User", result.Assignment["security.create_user"])

	// user_legacy and users_legacy should group together.
	require.Equal(t, "UserLegacy", result.Assignment["security.get_user_legacy"])
	require.Equal(t, "UserLegacy", result.Assignment["security.get_users_legacy"])
	require.Equal(t, "UserLegacy", result.Assignment["security.create_user_legacy"])
	require.Equal(t, "UserLegacy", result.Assignment["security.delete_user_legacy"])
}

func TestTrimSuffixes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		suffixes []string
		want     string
	}{
		{name: "no suffixes", input: "create_pit", suffixes: nil, want: "create_pit"},
		{name: "longest matches first", input: "get_all_pits", suffixes: []string{"_pits", "_pit"}, want: "get_all"},
		{name: "shorter suffix", input: "create_pit", suffixes: []string{"_pits", "_pit"}, want: "create"},
		{name: "no match", input: "bulk", suffixes: []string{"_pits", "_pit"}, want: "bulk"},
		{name: "only first match stripped", input: "delete_all_pits", suffixes: []string{"_pits", "_pit"}, want: "delete_all"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, trimSuffixes(tt.input, tt.suffixes))
		})
	}
}

// TestUnprefixedSubClientGroups verifies the declarative table is folded into
// unprefixedGroupOverrides with the right receiver and (trimmed) method names.
func TestUnprefixedSubClientGroups(t *testing.T) {
	t.Parallel()

	tests := []struct {
		group        string
		wantReceiver string
		wantMethod   string
	}{
		// Point-in-time: the pit/pits tail is trimmed off the method name.
		{group: "create_pit", wantReceiver: "PointInTimeClient", wantMethod: "Create"},
		{group: "delete_pit", wantReceiver: "PointInTimeClient", wantMethod: "Delete"},
		{group: "get_all_pits", wantReceiver: "PointInTimeClient", wantMethod: "GetAll"},
		{group: "delete_all_pits", wantReceiver: "PointInTimeClient", wantMethod: "DeleteAll"},
		// Document: verb-only group names map straight through.
		{group: "create", wantReceiver: "DocumentClient", wantMethod: "Create"},
		{group: "get", wantReceiver: "DocumentClient", wantMethod: "Get"},
		{group: "bulk", wantReceiver: "DocumentClient", wantMethod: "Bulk"},
		{group: "mtermvectors", wantReceiver: "DocumentClient", wantMethod: "MTermVectors"},
		{group: "get_source", wantReceiver: "DocumentClient", wantMethod: "GetSource"},
	}

	for _, tt := range tests {
		t.Run(tt.group, func(t *testing.T) {
			t.Parallel()
			route, ok := unprefixedGroupOverrides[tt.group]
			require.True(t, ok, "group %q must have an unprefixed override", tt.group)
			require.Equal(t, tt.wantReceiver, route.ReceiverType)
			require.Equal(t, tt.wantMethod, route.MethodName)
			require.False(t, route.TopLevel, "sub-client routes are not top-level")
		})
	}
}

// TestResolvePrimaryDispatch_DocumentAndPIT confirms the prefix-less document
// and PIT groups resolve onto their sub-clients rather than top-level Client.
func TestResolvePrimaryDispatch_DocumentAndPIT(t *testing.T) {
	t.Parallel()

	doc := resolvePrimaryDispatch("create", groupPrefix("create"))
	require.Equal(t, "DocumentClient", doc.ReceiverType)
	require.Equal(t, "Create", doc.MethodName)
	require.False(t, doc.TopLevel)

	pit := resolvePrimaryDispatch("create_pit", groupPrefix("create_pit"))
	require.Equal(t, "PointInTimeClient", pit.ReceiverType)
	require.Equal(t, "Create", pit.MethodName)
	require.False(t, pit.TopLevel)

	// DocumentClient.Create and PointInTimeClient.Create share a method name on
	// different receivers; both are valid and must not be conflated.
	require.NotEqual(t, doc.ReceiverType, pit.ReceiverType)
}

func opWithRoute(receiver string, topLevel bool) *ir.Operation {
	return &ir.Operation{
		DispatchRoutes: []ir.DispatchRoute{{ReceiverType: receiver, TopLevel: topLevel}},
	}
}

func TestUsedSubClientTypes(t *testing.T) {
	t.Parallel()

	t.Run("nested child retains parent chain", func(t *testing.T) {
		t.Parallel()
		// Only AliasClient is routed to; its parent IndicesClient must be retained.
		used := usedSubClientTypes([]*ir.Operation{opWithRoute("AliasClient", false)})
		require.True(t, used["AliasClient"])
		require.True(t, used["IndicesClient"], "parent of a used child must be retained")
	})

	t.Run("top-level and Client routes ignored", func(t *testing.T) {
		t.Parallel()
		used := usedSubClientTypes([]*ir.Operation{
			opWithRoute("Client", true),
			opWithRoute("", true),
		})
		require.Empty(t, used)
	})

	t.Run("document and pit retained when routed", func(t *testing.T) {
		t.Parallel()
		used := usedSubClientTypes([]*ir.Operation{
			opWithRoute("DocumentClient", false),
			opWithRoute("PointInTimeClient", false),
		})
		require.True(t, used["DocumentClient"])
		require.True(t, used["PointInTimeClient"])
	})
}

func TestFilterSubClients(t *testing.T) {
	t.Parallel()

	t.Run("preserves hierarchy order and drops unused", func(t *testing.T) {
		t.Parallel()
		used := map[string]bool{"IndicesClient": true, "CatClient": true}
		got := filterSubClients(used)
		// Order must match subClientHierarchy: CatClient precedes IndicesClient.
		require.Len(t, got, 2)
		require.Equal(t, "CatClient", got[0].TypeName)
		require.Equal(t, "IndicesClient", got[1].TypeName)
	})

	t.Run("dead template and data-stream clients dropped", func(t *testing.T) {
		t.Parallel()
		used := map[string]bool{"DocumentClient": true}
		got := filterSubClients(used)
		names := make(map[string]bool, len(got))
		for _, sc := range got {
			names[sc.TypeName] = true
		}
		require.True(t, names["DocumentClient"])
		for _, dead := range []string{
			"DataStreamClient", "IndexTemplateClient",
			"ComponentTemplateClient", "TemplateClient", "ScriptClient",
		} {
			require.False(t, names[dead], "%s has no routes and must be dropped", dead)
		}
	})
}

func TestCompatForwardersFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		group        string
		wantReceiver string
		wantMethod   string
		wantTopLevel bool
		wantForward  string
	}{
		{
			// "bulk" canonical route is DocumentClient.Bulk; the forwarder
			// restores top-level Client.Bulk forwarding to Doc.Bulk.
			name:  "top-level forwarder targets sub-client field path",
			group: "bulk", wantReceiver: "Client", wantMethod: "Bulk",
			wantTopLevel: true, wantForward: "Doc.Bulk",
		},
		{
			// get_all_pits canonical is PointInTimeClient.GetAll; the alias keeps
			// the historical name Get on the same receiver.
			name:  "same-receiver name alias forwards to canonical method",
			group: "get_all_pits", wantReceiver: "PointInTimeClient", wantMethod: "Get",
			wantTopLevel: false, wantForward: "GetAll",
		},
		{
			name:  "get_source alias on DocumentClient",
			group: "get_source", wantReceiver: "DocumentClient", wantMethod: "Source",
			wantTopLevel: false, wantForward: "GetSource",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			primary := resolvePrimaryDispatch(tt.group, groupPrefix(tt.group))
			fwds := compatForwardersFor(tt.group, primary)
			require.Len(t, fwds, 1)
			require.Equal(t, tt.wantReceiver, fwds[0].ReceiverType)
			require.Equal(t, tt.wantMethod, fwds[0].MethodName)
			require.Equal(t, tt.wantTopLevel, fwds[0].TopLevel)
			require.Equal(t, tt.wantForward, fwds[0].Forward)
		})
	}

	t.Run("no forwarders for an unmapped group", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, compatForwardersFor("search", resolvePrimaryDispatch("search", "")))
	})
}

func TestResolveDispatchRoutes_IncludesForwarders(t *testing.T) {
	t.Parallel()

	routes := resolveDispatchRoutes("bulk")
	// Canonical sub-client route first, then the top-level forwarder.
	require.GreaterOrEqual(t, len(routes), 2)
	require.Equal(t, "DocumentClient", routes[0].ReceiverType)
	require.Empty(t, routes[0].Forward, "canonical route is not a forwarder")

	var fwd *dispatchRoute
	for i := range routes {
		if routes[i].Forward != "" {
			fwd = &routes[i]
		}
	}
	require.NotNil(t, fwd, "bulk must carry a compatibility forwarder route")
	require.Equal(t, "Client", fwd.ReceiverType)
}

func TestApplyCompatPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		cfg             CompatConfig
		wantRoutes      int
		wantForwarder   bool // a forwarder route survives
		wantFwdDeprecat bool // the surviving forwarder is marked deprecated
	}{
		{name: "compat off drops forwarder routes", cfg: CompatConfig{V4Compat: false}, wantRoutes: 1, wantForwarder: false},
		{
			name: "compat on keeps forwarder, not deprecated",
			cfg:  CompatConfig{V4Compat: true}, wantRoutes: 2, wantForwarder: true, wantFwdDeprecat: false,
		},
		{
			name: "deprecation marks forwarder only",
			cfg:  CompatConfig{V4Compat: true, V4Deprecation: true}, wantRoutes: 2, wantForwarder: true, wantFwdDeprecat: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			op := &ir.Operation{DispatchRoutes: []ir.DispatchRoute{
				{ReceiverType: "DocumentClient", MethodName: "Bulk", FieldPath: "Doc"},
				{ReceiverType: "Client", MethodName: "Bulk", TopLevel: true, Forward: "Doc.Bulk"},
			}}
			applyCompatPolicy([]*ir.Operation{op}, tt.cfg)

			require.Len(t, op.DispatchRoutes, tt.wantRoutes)
			// Canonical route is always retained and never deprecated.
			require.Empty(t, op.DispatchRoutes[0].Forward)
			require.False(t, op.DispatchRoutes[0].Deprecated, "canonical route stays non-deprecated")

			var fwd *ir.DispatchRoute
			for i := range op.DispatchRoutes {
				if op.DispatchRoutes[i].Forward != "" {
					fwd = &op.DispatchRoutes[i]
				}
			}
			if !tt.wantForwarder {
				require.Nil(t, fwd, "forwarder route must be dropped")
				return
			}
			require.NotNil(t, fwd)
			require.Equal(t, tt.wantFwdDeprecat, fwd.Deprecated)
		})
	}
}
