// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

// affinityCacheProvider is implemented by policies that own an
// [indexSlotCache]. Used by [PolicyChain] to walk the policy tree
// and find the cache for a given index.
type affinityCacheProvider interface {
	affinityCache() *indexSlotCache
}

// findAffinityCache walks a policy tree depth-first and returns the
// first [indexSlotCache] found. Returns nil if no affinity-capable
// policy exists in the tree.
func findAffinityCache(p Policy) *indexSlotCache {
	if acp, ok := p.(affinityCacheProvider); ok {
		return acp.affinityCache()
	}
	if walker, ok := p.(policyTreeWalker); ok {
		for _, child := range walker.childPolicies() {
			if cache := findAffinityCache(child); cache != nil {
				return cache
			}
		}
	}
	return nil
}
