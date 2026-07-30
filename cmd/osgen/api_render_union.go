// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import "github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"

// unionNeedsTryEach returns true if any two branches share the same token class,
// meaning byte-prefix discrimination is insufficient for at least one pair.
func unionNeedsTryEach(branches []unionBranch) bool {
	if len(branches) < 2 {
		return false
	}
	seen := make(map[ir.TokenClass]bool, len(branches))
	for _, b := range branches {
		if seen[b.TokenClass] {
			return true
		}
		seen[b.TokenClass] = true
	}
	return false
}
