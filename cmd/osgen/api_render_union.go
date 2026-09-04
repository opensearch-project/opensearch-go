// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import "github.com/opensearch-project/opensearch-go/cmd/osgen/v5/ir"

// branchesCollideOnTokenClass reports whether any two branches decode from the
// same JSON token, which makes the first byte insufficient to pick one.
//
// This is the FALLBACK test, consulted only for unions the spec leaves
// undiscriminated. A union that declares an OpenAPI `discriminator` reads its
// branch from a named property (see discriminatorValues) and never consults the
// token class at all -- most of the OpenSearch DSL unions are all-object and so
// collide here trivially, which is exactly why the spec bothers to declare a
// discriminator for them.
//
// When branches DO collide and no discriminator resolves, the union is
// ir.TypeAmbiguousWire and classifyUnions picks the payload-inspection strategy:
// a key-presence merge, request selection, or the try-each decoder of last
// resort.
func branchesCollideOnTokenClass(branches []unionBranch) bool {
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
