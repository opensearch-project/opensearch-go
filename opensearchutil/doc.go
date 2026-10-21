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

[NewBulkIndexer] returns a [BulkIndexer], a long-lived, asynchronous indexer built on the bulk API.
Add documents one at a time with [BulkIndexer.Add]; the indexer keeps a buffer for each shard, sends
each full buffer as one bulk request to the node that hosts the shard's primary, and reports the
outcome of each document through the item's OnSuccess and OnError callbacks, exactly one of which
runs when the indexer is done with the item. OnError gets a [BulkError], which says which layer
failed ([ErrorLayer]) and why ([ErrorKind]), and matches their sentinels with errors.Is.
[BulkIndexer.Flush] drains what has been added and keeps the indexer open, [BulkIndexer.Close]
waits for every item and releases the indexer, and [BulkIndexer.Cancel] stops it immediately.

A [BulkIndexerConfig] can also set:

  - RetryPolicy, a [RetryPolicy] that decides whether a failed item is sent again, given the
    [BulkError]. Retrying needs deterministic document IDs, so an item without one fails with
    [ErrMissingDocumentID].
  - Fingerprinter, a [DocumentFingerprinter] that derives an id for an item that has none.
  - Sizing, a [BulkSizing] that tunes how the size of the batches follows what each node can take.
  - Observer, a [BulkIndexerObserver] that receives an event for each request, each error, and each
    item once it is done, for statistics and metrics.
  - OnUnhandledError, called for an error no item callback took.

[BulkIndexerItem].Metadata carries opaque data, such as a stream offset, back to the callbacks
without being sent to the cluster.

# Batch sizing

Two controllers adapt what the indexer sends, each from one signal. FlushBytes is the most a shard's
buffer collects. Beneath it, a PID controller sizes each shard's buffer by how fast the node serves
that shard compared with its other shards: a shard slower than the rest of its node, past its
headroom and by more than its own usual variation, gets smaller batches, so it gives way to the
others. A slowdown the whole node shares means the node is busy, and cuts no shard. Separately, each
node has a window: the number of bytes the indexer may have in flight to it. The window starts wide
open; a 429 from the node cuts it to half of what was in flight when the 429 arrived, and it then
grows back slowly. A buffer that would take a node past its window waits for requests in flight to
be answered. [BulkSizing] turns either controller off, [ShardFixed] for the shard buffers and
[TargetFixed] for the node windows, and sets the PID gains.

The number of requests in flight to a node is limited separately, to NumWorkers, or GOMAXPROCS when
NumWorkers is 0. The write thread pool window that the transport tracks for each node is a signal,
not a limit: it chooses the node for documents that belong to no shard.

An operator can override those settings without a code change, through environment variables read
when the indexer is built. A variable that is set and valid wins over the code:

  - OPENSEARCH_GO_BULK_SHARD, pid or fixed, overrides [BulkSizing].Shard.
  - OPENSEARCH_GO_BULK_TARGET, aimd or fixed, overrides [BulkSizing].Node.
  - OPENSEARCH_GO_BULK_PID_KP, OPENSEARCH_GO_BULK_PID_KI and OPENSEARCH_GO_BULK_PID_KD override
    the Kp, Ki and Kd of [BulkSizing].PID; a gain of 0 turns that term off.

# Committing a stream position

[FrontierTracker] reports the frontier of each of several ordered streams whose records complete
out of order, which is how a [BulkIndexer] finishes them: the lowest sequence not yet complete,
below which every sequence is. It never passes a gap.

# JSON bodies

[NewJSONReader] encodes a Go value as a request body.

See the Bulk Indexer guide at
https://github.com/opensearch-project/opensearch-go/blob/main/guides/indexing-bulk_indexer.md.
*/
package opensearchutil
