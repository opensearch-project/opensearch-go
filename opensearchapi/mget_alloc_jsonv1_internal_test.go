// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !goexperiment.jsonv2 && !integration

package opensearchapi

// mgetMaxAllocs is the most allocations one mget of keys documents may make, by
// mode and keys, when encoding/json runs on its original engine: Go 1.26, and
// Go 1.27 with GOEXPERIMENT=nojsonv2. Measured on go1.26.9, plus 1% or 2,
// whichever is more, for sync.Pool entries a garbage collection drops during
// the run.
var mgetMaxAllocs = map[string]map[int]float64{
	"coordinator":         {1: 42, 10: 148, 100: 1157, 1000: 11168},
	"direct/fewest-nodes": {1: 62, 10: 206, 100: 1224, 1000: 11303},
	"direct/least-loaded": {1: 62, 10: 242, 100: 1272, 1000: 11301},
}
