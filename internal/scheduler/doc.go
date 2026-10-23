// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package scheduler holds the per-node congestion control shared by the clients
// that send work straight to the nodes that hold it: the bulk client in
// opensearchutil and the direct mget driver in opensearchapi. A [Window] bounds
// how much work may be outstanding to one node and answers a 429 the way TCP
// answers a lost packet; [NodeWindows] keeps one window per node and makes a
// sender wait for room.
package scheduler
