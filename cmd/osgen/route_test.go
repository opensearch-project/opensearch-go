// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
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
		{name: "simple", resource: "role", want: "roleClient"},
		{name: "compound", resource: "action_group", want: "actionGroupClient"},
		{name: "triple", resource: "role_mapping", want: "roleMappingClient"},
		{name: "long", resource: "distinguished_name", want: "distinguishedNameClient"},
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

	require.Equal(t, "actionGroupClient", scByField["ActionGroup"].TypeName)
	require.Equal(t, "roleClient", scByField["Role"].TypeName)

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
