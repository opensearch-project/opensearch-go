// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build race && !integration

package opensearchtransport

// raceEnabled reports whether the race detector is on, which allocates on its
// own and so changes what an allocation count measures.
const raceEnabled = true
