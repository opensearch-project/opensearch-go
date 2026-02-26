// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Temporary test to dump the policy tree structure for verification.

//go:build !integration

package opensearchtransport

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDumpSmartPolicyTree(t *testing.T) {
	router := NewMuxRouter()
	chain := router.(*PolicyChain)

	fmt.Println("=== BEFORE configurePolicySettings ===")
	fmt.Println(describePolicyTree(chain, 0))

	// Configure policy settings (simulates what New() does)
	err := chain.configurePolicySettings(createTestConfig())
	require.NoError(t, err)

	fmt.Println("=== AFTER configurePolicySettings ===")
	fmt.Println(describePolicyTree(chain, 0))

	// Collect pool snapshots
	snapshots := chain.poolSnapshots()
	fmt.Printf("\n=== Pool Snapshots (%d pools) ===\n", len(snapshots))
	for _, snap := range snapshots {
		fmt.Printf("  %s\n", snap)
	}

	// Verify expected pool names exist
	names := make(map[string]bool)
	for _, snap := range snapshots {
		names[snap.Name] = true
	}
	require.True(t, names["role:coordinating_only"], "missing coordinating_only pool")
	require.True(t, names["role:ingest"], "missing ingest pool")
	require.True(t, names["role:search"], "missing search pool")
	require.True(t, names["role:warm"], "missing warm pool")
	require.True(t, names["roundrobin"], "missing roundrobin pool")

	// data appears three times (search fallback + warm fallback + direct data for shard maintenance)
	dataCount := 0
	for _, snap := range snapshots {
		if snap.Name == "role:data" {
			dataCount++
		}
	}
	require.Equal(t, 3, dataCount, "expected 3 role:data pools")
}

func TestDumpDefaultPolicyTree(t *testing.T) {
	router := NewRoundRobinRouter()
	chain := router.(*PolicyChain)

	err := chain.configurePolicySettings(createTestConfig())
	require.NoError(t, err)

	fmt.Println(describePolicyTree(chain, 0))

	snapshots := chain.poolSnapshots()
	fmt.Printf("\n=== Pool Snapshots (%d pools) ===\n", len(snapshots))
	for _, snap := range snapshots {
		fmt.Printf("  %s\n", snap)
	}

	require.Len(t, snapshots, 2) // coordinating_only + roundrobin
}

func describePolicyTree(node any, depth int) string {
	indent := strings.Repeat("  ", depth)
	var b strings.Builder

	switch v := node.(type) {
	case *PolicyChain:
		fmt.Fprintf(&b, "%sPolicyChain (isEnabled=%v, policies=%d)\n", indent, psIsEnabled(v.policyState.Load()), len(v.policies))
		for i, p := range v.policies {
			fmt.Fprintf(&b, "%s  [%d] ", indent, i)
			b.WriteString(describePolicyTree(p, depth+2))
		}

	case *IfEnabledPolicy:
		fmt.Fprintf(&b, "IfEnabledPolicy (isEnabled=%v)\n", psIsEnabled(v.policyState.Load()))
		fmt.Fprintf(&b, "%s  truePolicy:  ", indent)
		b.WriteString(describePolicyTree(v.truePolicy, depth+2))
		fmt.Fprintf(&b, "%s  falsePolicy: ", indent)
		b.WriteString(describePolicyTree(v.falsePolicy, depth+2))

	case *MuxPolicy:
		fmt.Fprintf(&b, "MuxPolicy (isEnabled=%v, uniquePolicies=%d)\n", psIsEnabled(v.policyState.Load()), len(v.uniquePolicies))
		i := 0
		for p := range v.uniquePolicies {
			fmt.Fprintf(&b, "%s  [%d] ", indent, i)
			b.WriteString(describePolicyTree(p, depth+2))
			i++
		}

	case *RolePolicy:
		hasPool := v.pool != nil
		poolName := ""
		if hasPool {
			poolName = v.pool.name
		}
		fmt.Fprintf(&b, "RolePolicy(key=%q, pool=%q, enabled=%v)\n",
			v.requiredRoleKey, poolName, psIsEnabled(v.policyState.Load()))

	case *RoundRobinPolicy:
		hasPool := v.pool != nil
		poolName := ""
		if hasPool {
			poolName = v.pool.name
		}
		fmt.Fprintf(&b, "RoundRobinPolicy(pool=%q, enabled=%v)\n",
			poolName, psIsEnabled(v.policyState.Load()))

	case *CoordinatorPolicy:
		hasPool := v.pool != nil
		poolName := ""
		if hasPool {
			poolName = v.pool.name
		}
		fmt.Fprintf(&b, "CoordinatorPolicy(pool=%q, enabled=%v)\n",
			poolName, psIsEnabled(v.policyState.Load()))

	case *NullPolicy:
		fmt.Fprintf(&b, "NullPolicy\n")

	default:
		fmt.Fprintf(&b, "UNKNOWN(%T)\n", node)
	}

	return b.String()
}
