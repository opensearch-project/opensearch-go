// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// --- parsePolicyOverrides ---

func TestParsePolicyOverrides_BoolValue(t *testing.T) {
	t.Setenv("OPENSEARCH_GO_POLICY_ROLE", "false")

	overrides := parsePolicyOverrides()
	require.Len(t, overrides, 1)

	o := overrides[0]
	require.Equal(t, "role", o.typeName)
	require.NotNil(t, o.applyAll)
	require.False(t, *o.applyAll)
}

func TestParsePolicyOverrides_BoolTrue(t *testing.T) {
	t.Setenv("OPENSEARCH_GO_POLICY_ROUNDROBIN", "true")

	overrides := parsePolicyOverrides()
	require.Len(t, overrides, 1)
	require.NotNil(t, overrides[0].applyAll)
	require.True(t, *overrides[0].applyAll)
}

func TestParsePolicyOverrides_PathBool(t *testing.T) {
	t.Setenv("OPENSEARCH_GO_POLICY_ROLE", "chain[0].mux[0].role[0]=false")

	overrides := parsePolicyOverrides()
	require.Len(t, overrides, 1)

	o := overrides[0]
	require.Nil(t, o.applyAll)
	require.Len(t, o.matchers, 1)

	m := o.matchers[0]
	require.Equal(t, "chain[0].mux[0].role[0]", m.pattern)
	require.False(t, m.enable)
}

func TestParsePolicyOverrides_CommaSeparated(t *testing.T) {
	t.Setenv("OPENSEARCH_GO_POLICY_ROLE", "chain[0].role[0]=false,chain[0].role[1]=true")

	overrides := parsePolicyOverrides()
	require.Len(t, overrides, 1)
	require.Len(t, overrides[0].matchers, 2)
	require.False(t, overrides[0].matchers[0].enable, "first matcher should be disable")
	require.True(t, overrides[0].matchers[1].enable, "second matcher should be enable")
}

func TestParsePolicyOverrides_RegexPattern(t *testing.T) {
	t.Setenv("OPENSEARCH_GO_POLICY_AFFINITY", ".*mux.*=false")

	overrides := parsePolicyOverrides()
	require.Len(t, overrides, 1)

	m := overrides[0].matchers[0]
	require.NotNil(t, m.regex, "expected regex to be compiled")
	require.True(t, m.regex.MatchString("chain[0].mux[0].affinity[0]"))
}

func TestParsePolicyOverrides_UnsetVar(t *testing.T) {
	// Don't set any env vars -- overrides should be empty.
	overrides := parsePolicyOverrides()
	require.Empty(t, overrides)
}

func TestParsePolicyOverrides_EmptyValue(t *testing.T) {
	t.Setenv("OPENSEARCH_GO_POLICY_ROLE", "")

	overrides := parsePolicyOverrides()
	require.Empty(t, overrides)
}

func TestParsePolicyOverrides_FallbackRegex(t *testing.T) {
	// Value with no '=' -- treated as regex fallback, disable on match.
	t.Setenv("OPENSEARCH_GO_POLICY_ROLE", "chain.*role")

	overrides := parsePolicyOverrides()
	require.Len(t, overrides, 1)

	m := overrides[0].matchers[0]
	require.False(t, m.enable, "fallback should disable")
	require.NotNil(t, m.regex, "expected regex to be compiled")
	require.True(t, m.regex.MatchString("chain[0].role[0]"))
}

// --- buildPolicyPaths ---

func TestBuildPolicyPaths_SmartPolicy(t *testing.T) {
	root := NewSmartPolicy()

	paths := buildPolicyPaths(root)
	require.NotEmpty(t, paths)

	// NewSmartPolicy returns an IfEnabledPolicy at the root.
	rootPath, ok := paths[root]
	require.True(t, ok, "root policy not in paths map")
	require.Equal(t, "ifenabled[0]", rootPath)
}

func TestBuildPolicyPaths_RoundRobinDefault(t *testing.T) {
	root := NewRoundRobinDefaultPolicy()

	paths := buildPolicyPaths(root)

	// NewRoundRobinDefaultPolicy wraps in NewPolicy() which returns PolicyChain.
	rootPath := paths[root]
	require.Equal(t, "chain[0]", rootPath)

	// Verify the roundrobin policy has a non-empty path.
	for p, path := range paths {
		if _, ok := p.(*RoundRobinPolicy); ok {
			require.NotEmpty(t, path, "roundrobin policy path should not be empty")
		}
	}
}

func TestBuildPolicyPaths_Deterministic(t *testing.T) {
	// Building paths twice should produce identical results.
	root := NewSmartPolicy()

	paths1 := buildPolicyPaths(root)
	paths2 := buildPolicyPaths(root)

	require.Len(t, paths1, len(paths2), "path maps should have same length")

	for p, path1 := range paths1 {
		path2, ok := paths2[p]
		require.True(t, ok, "policy %T missing from second path map", p)
		require.Equal(t, path1, path2, "path mismatch for %T", p)
	}
}

// --- applyPolicyOverrides ---

func TestApplyPolicyOverrides_DisableAllRole(t *testing.T) {
	root := NewSmartPolicy()

	b := false
	overrides := []policyOverride{{
		typeName: "role",
		envKey:   "OPENSEARCH_GO_POLICY_ROLE",
		applyAll: &b,
	}}

	applyPolicyOverrides(root, overrides)

	// Walk the tree and verify all RolePolicies are env-disabled.
	paths := buildPolicyPaths(root)
	for p := range paths {
		if rp, ok := p.(*RolePolicy); ok {
			require.NotZero(t, rp.policyState.Load()&psEnvDisabled,
				"RolePolicy %q should be env-disabled", rp.requiredRoleKey)
		}
	}
}

func TestApplyPolicyOverrides_EnableNoOp(t *testing.T) {
	root := NewSmartPolicy()

	b := true
	overrides := []policyOverride{{
		typeName: "role",
		envKey:   "OPENSEARCH_GO_POLICY_ROLE",
		applyAll: &b,
	}}

	applyPolicyOverrides(root, overrides)

	// Walk the tree and verify all RolePolicies have psEnvEnabled set
	// but are NOT force-disabled.
	paths := buildPolicyPaths(root)
	for p := range paths {
		if rp, ok := p.(*RolePolicy); ok {
			state := rp.policyState.Load()
			require.Zero(t, state&psEnvDisabled,
				"RolePolicy %q should NOT be env-disabled", rp.requiredRoleKey)
			require.NotZero(t, state&psEnvEnabled,
				"RolePolicy %q should have psEnvEnabled set", rp.requiredRoleKey)
		}
	}
}

func TestApplyPolicyOverrides_PathMatch(t *testing.T) {
	root := NewSmartPolicy()

	paths := buildPolicyPaths(root)

	// Find the first RolePolicy path.
	var targetPath string
	var targetPolicy *RolePolicy
	for p, path := range paths {
		if rp, ok := p.(*RolePolicy); ok {
			targetPath = path
			targetPolicy = rp
			break
		}
	}
	require.NotNil(t, targetPolicy, "no RolePolicy found in smart policy tree")

	overrides := []policyOverride{{
		typeName: "role",
		envKey:   "OPENSEARCH_GO_POLICY_ROLE",
		matchers: []pathMatcher{{
			raw:     targetPath + "=false",
			pattern: targetPath,
			enable:  false,
		}},
	}}

	applyPolicyOverrides(root, overrides)

	// The targeted policy should be disabled.
	require.NotZero(t, targetPolicy.policyState.Load()&psEnvDisabled,
		"targeted RolePolicy at %q should be env-disabled", targetPath)

	// Other RolePolicies should NOT be disabled.
	for p, path := range paths {
		if rp, ok := p.(*RolePolicy); ok && rp != targetPolicy {
			require.Zero(t, rp.policyState.Load()&psEnvDisabled,
				"RolePolicy at %q should NOT be env-disabled", path)
		}
	}
}

func TestApplyPolicyOverrides_RegexMatch(t *testing.T) {
	root := NewSmartPolicy()

	overrides := parsePolicyOverridesForTest(t, "OPENSEARCH_GO_POLICY_ROUNDROBIN", ".*roundrobin.*=false")

	applyPolicyOverrides(root, overrides)

	// All roundrobin policies should be disabled.
	paths := buildPolicyPaths(root)
	for p := range paths {
		if rr, ok := p.(*RoundRobinPolicy); ok {
			require.NotZero(t, rr.policyState.Load()&psEnvDisabled,
				"RoundRobinPolicy should be env-disabled via regex match")
		}
	}
}

// --- ForceDisabled behavior ---

func TestForceDisabled_IsEnabled(t *testing.T) {
	rr := NewRoundRobinPolicy().(*RoundRobinPolicy)
	// Simulate having connections.
	psSetEnabled(&rr.policyState, true)

	require.True(t, rr.IsEnabled(), "should be enabled before override")

	rr.setEnvOverride(false)

	require.False(t, rr.IsEnabled(), "should be disabled after setEnvOverride(false)")
}

func TestForceDisabled_Eval(t *testing.T) {
	rr := NewRoundRobinPolicy().(*RoundRobinPolicy)
	psSetEnabled(&rr.policyState, true)

	rr.setEnvOverride(false)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://localhost:9200/", nil)
	pool, err := rr.Eval(context.Background(), req)
	require.NoError(t, err)
	require.Nil(t, pool, "force-disabled Eval should return nil pool")
}

func TestForceDisabled_DiscoveryUpdate_Leaf(t *testing.T) {
	rr := NewRoundRobinPolicy().(*RoundRobinPolicy)
	rr.setEnvOverride(false)

	conn := &Connection{}
	// DiscoveryUpdate should be a no-op when force-disabled.
	err := rr.DiscoveryUpdate([]*Connection{conn}, nil, nil)
	require.NoError(t, err)

	// The policy should still report as disabled.
	require.False(t, rr.IsEnabled(), "force-disabled policy should remain disabled after DiscoveryUpdate")
}

func TestForceDisabled_NullPolicy(t *testing.T) {
	np := NewNullPolicy().(*NullPolicy)

	require.True(t, np.IsEnabled(), "NullPolicy should be enabled by default")

	np.setEnvOverride(false)

	require.False(t, np.IsEnabled(), "NullPolicy should be disabled after setEnvOverride(false)")
}

func TestForceDisabled_CoordinatorPolicy(t *testing.T) {
	cp := NewCoordinatorPolicy().(*CoordinatorPolicy)
	psSetEnabled(&cp.policyState, true)

	cp.setEnvOverride(false)

	require.False(t, cp.IsEnabled(), "CoordinatorPolicy should be disabled after setEnvOverride(false)")

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://localhost:9200/", nil)
	pool, err := cp.Eval(context.Background(), req)
	require.NoError(t, err)
	require.Nil(t, pool, "force-disabled Eval should return nil pool")
}

// --- policyState bitfield ---

func TestPolicyState_Bitfield(t *testing.T) {
	t.Run("env_disable_overrides_dynamic_enable", func(t *testing.T) {
		var s int32
		s |= psEnabled
		s |= psEnvDisabled
		require.False(t, psIsEnabled(s), "psEnvDisabled should override psEnabled")
	})

	t.Run("dynamic_enable_without_env", func(t *testing.T) {
		var s int32
		s |= psEnabled
		require.True(t, psIsEnabled(s), "psEnabled alone should be enabled")
	})

	t.Run("env_enable_is_observability_only", func(t *testing.T) {
		var s int32
		s |= psEnvEnabled
		// psEnvEnabled without psEnabled should still be false.
		require.False(t, psIsEnabled(s), "psEnvEnabled without psEnabled should be false")
	})

	t.Run("default_is_false", func(t *testing.T) {
		require.False(t, psIsEnabled(0), "zero state should be disabled")
	})
}

// --- Integration: full env var flow ---

func TestPolicyOverride_FullFlow(t *testing.T) {
	t.Setenv("OPENSEARCH_GO_POLICY_ROUNDROBIN", "false")

	root := NewRoundRobinDefaultPolicy()
	overrides := parsePolicyOverrides()
	applyPolicyOverrides(root, overrides)

	// Walk the tree and verify all RoundRobinPolicies are force-disabled.
	paths := buildPolicyPaths(root)
	found := false
	for p := range paths {
		if rr, ok := p.(*RoundRobinPolicy); ok {
			found = true
			require.NotZero(t, rr.policyState.Load()&psEnvDisabled,
				"RoundRobinPolicy should be env-disabled via OPENSEARCH_GO_POLICY_ROUNDROBIN=false")
		}
	}
	require.True(t, found, "no RoundRobinPolicy found in tree")
}

// parsePolicyOverridesForTest is a test helper that sets an env var and returns parsed overrides.
func parsePolicyOverridesForTest(t *testing.T, envKey, envVal string) []policyOverride {
	t.Helper()
	t.Setenv(envKey, envVal)
	return parsePolicyOverrides()
}
