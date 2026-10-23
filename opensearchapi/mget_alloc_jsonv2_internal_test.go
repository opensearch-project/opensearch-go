// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build goexperiment.jsonv2 && !integration

package opensearchapi

// mgetMaxAllocs is the most allocations one mget of keys documents may make, by
// mode and keys, when encoding/json runs on the json/v2 engine: the default
// from Go 1.27, and Go 1.26 with GOEXPERIMENT=jsonv2. Measured on go1.27.3,
// plus 1% or 2, whichever is more, for sync.Pool entries a garbage collection
// drops during the run.
var mgetMaxAllocs = map[string]map[int]float64{
	"coordinator":         {1: 28, 10: 72, 100: 444, 1000: 4092},
	"direct/fewest-nodes": {1: 48, 10: 123, 100: 504, 1000: 4215},
	"direct/least-loaded": {1: 48, 10: 153, 100: 547, 1000: 4213},
}
