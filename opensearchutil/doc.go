// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

/*
Package opensearchutil provides helper utilities to the Go client for OpenSearch.

# Bulk indexing

[NewBulkIndexer] returns a [BulkIndexer], a parallel, asynchronous indexer built on the bulk API. Add
documents one at a time with [BulkIndexer.Add]; the indexer batches them across several workers and
reports the fate of each through the item's OnSuccess and OnFailure callbacks, exactly one of which
fires when the item is final. [BulkIndexer.Flush] drains what has been added and keeps the
indexer open, [BulkIndexer.Close] waits for every item and releases it, and [BulkIndexer.Cancel]
stops it immediately.

A [BulkIndexerConfig] can also set:

  - RetryPolicy, a [RetryPolicy] that decides whether a failed item is sent again, told whether the
    whole request failed or one item did ([FailureScope]) and how ([FailureKind]). Retrying needs
    deterministic document ids, so an item without one fails with [ErrMissingDocumentID].
  - Fingerprinter, a [DocumentFingerprinter] that derives an id for an item that has none.
  - Sizing, a [BulkSizing] that tunes how the size of the batches follows what each node can take.
  - Observer, a [BulkIndexerObserver] that receives an event for each request, each failure, and each
    item's final state, for statistics and metrics.

[BulkIndexerItem].Metadata carries opaque data, such as a stream offset, back to the callbacks
without being sent to the cluster.

# Batch sizing

Unless told otherwise, the indexer treats FlushBytes as a ceiling and sizes the buffer of each shard
by how fast the node serves it next to the node's other shards: a shard slower than the rest of its
node, past its headroom and by more than its own usual variation, is given smaller batches by a PID
controller, so it gives way to the others. A slowdown the whole node shares is its load. Separately,
each node has a window, the number of bytes the indexer will have out to it at once, which starts
wide open and is cut when the node answers 429: to half of what was out when the 429 arrived, and
then it grows back slowly. A buffer that would take a node past its window waits for the requests
out to be answered. [BulkSizing] selects fixed or adaptive sizing, plain additive-increase,
multiplicative-decrease (AIMD) in place of the PID controller for the batches, and the PID gains.

An operator can override those settings without a code change, through environment variables read
when the indexer is built. A variable that is set and valid wins over the code:

  - OPENSEARCH_GO_BULK_MODE, fixed or adaptive, overrides [BulkSizing].Fixed.
  - OPENSEARCH_GO_BULK_AIMD, on or off, overrides [BulkSizing].AIMD.
  - OPENSEARCH_GO_BULK_PID_KP, OPENSEARCH_GO_BULK_PID_KI and OPENSEARCH_GO_BULK_PID_KD override
    [BulkSizing].Kp, Ki and Kd; a gain of 0 turns that term off.

# Committing a stream position

[SequenceTracker] reports how far each of several ordered streams can safely advance when records
complete out of order, which is how a [BulkIndexer] finishes them. It reports only the end of a
contiguous prefix.

# JSON bodies

[NewJSONReader] encodes a Go value as a request body.

See the Bulk Indexer guide at
https://github.com/opensearch-project/opensearch-go/blob/main/guides/indexing-bulk_indexer.md.
*/
package opensearchutil
