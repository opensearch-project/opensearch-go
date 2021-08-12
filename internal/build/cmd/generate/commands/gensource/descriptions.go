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

package gensource

var apiDescriptionsYAML = `
---
bulk:
  description: |-
    Allows to perform multiple index/update/delete operations in a single request.

cat.aliases:
  description: |-
    Shows information about currently configured aliases to indices including filter and routing infos.

cat.allocation:
  description: |-
    Provides a snapshot of how many shards are allocated to each data node and how much disk space they are using.

cat.count:
  description: |-
    Provides quick access to the document count of the entire cluster, or individual indices.

cat.fielddata:
  description: |-
    Shows how much heap memory is currently being used by fielddata on every data node in the cluster.

cat.health:
  description: |-
    Returns a concise representation of the cluster health.

cat.help:
  description:
    Returns help for the Cat APIs.

cat.indices:
  description: |-
    Returns information about indices: number of primaries and replicas, document counts, disk size, ...

cat.master:
  description: |-
    Returns information about the master node.

cat.nodeattrs:
  description: |-
    Returns information about custom node attributes.

cat.nodes:
  description: |-
    Returns basic statistics about performance of cluster nodes.

cat.pending_tasks:
  description: |-
    Returns a concise representation of the cluster pending tasks.

cat.plugins:
  description: |-
    Returns information about installed plugins across nodes node.

cat.recovery:
  description: |-
    Returns information about index shard recoveries, both on-going completed.

cat.repositories:
  description: |-
    Returns information about snapshot repositories registered in the cluster.

cat.segments:
  description: |-
    Provides low-level information about the segments in the shards of an index.

cat.shards:
  description: |-
    Provides a detailed view of shard allocation on nodes.

cat.snapshots:
  description: |-
    Returns all snapshots in a specific repository.

cat.tasks:
  description: |-
    Returns information about the tasks currently executing on one or more nodes in the cluster.

cat.templates:
  description: |-
    Returns information about existing templates.

cat.thread_pool:
  description: |-
    Returns cluster-wide thread pool statistics per node.
    By default the active, queue and rejected statistics are returned for all thread pools.

clear_scroll:
  description: |-
    Explicitly clears the search context for a scroll.

cluster.allocation_explain:
  description: |-
    Provides explanations for shard allocations in the cluster.

cluster.get_settings:
  description: |-
    Returns cluster settings.

cluster.health:
  description: |-
    Returns basic information about the health of the cluster.

cluster.pending_tasks:
  description: |-
    Returns a list of any cluster-level changes (e.g. create index, update mapping,
    allocate or fail shard) which have not yet been executed.

cluster.put_settings:
  description: |-
    Updates the cluster settings.

cluster.remote_info:
  description: |-
    Returns the information about configured remote clusters.

cluster.reroute:
  description: |-
    Allows to manually change the allocation of individual shards in the cluster.

cluster.state:
  description: |-
    Returns a comprehensive information about the state of the cluster.

cluster.stats:
  description: |-
    Returns high-level overview of cluster statistics.

count:
  description: |-
    Returns number of documents matching a query.

create:
  description: |-
    Creates a new document in the index.

    Returns a 409 response when a document with a same ID already exists in the index.

delete:
  description: |-
    Removes a document from the index.

delete_by_query:
  description: |-
    Deletes documents matching the provided query.

delete_by_query_rethrottle:
  description: |-
    Changes the number of requests per second for a particular Delete By Query operation.

delete_script:
  description: |-
    Deletes a script.

exists:
  description: |-
    Returns information about whether a document exists in an index.

exists_source:
  description: |-
    Returns information about whether a document source exists in an index.

explain:
  description: |-
    Returns information about why a specific matches (or doesn't match) a query.

field_caps:
  description: |-
    Returns the information about the capabilities of fields among multiple indices.

get:
  description: |-
    Returns a document.

get_script:
  description: |-
    Returns a script.

get_source:
  description: |-
    Returns the source of a document.

index:
  description: |-
    Creates or updates a document in an index.

indices.analyze:
  description: |-
    Performs the analysis process on a text and return the tokens breakdown of the text.

indices.clear_cache:
  description: |-
    Clears all or specific caches for one or more indices.

indices.clone:
  description: |-
    Clones an existing index into a new index.

indices.close:
  description: |-
    Closes an index.

indices.create:
  description: |-
    Creates an index with optional settings and mappings.

indices.delete:
  description: |-
    Deletes an index.

indices.delete_alias:
  description: |-
    Deletes an alias.

indices.delete_template:
  description: |-
    Deletes an index template.

indices.exists:
  description: |-
    Returns information about whether a particular index exists.

indices.exists_alias:
  description: |-
    Returns information about whether a particular alias exists.

indices.exists_template:
  description: |-
    Returns information about whether a particular index template exists.

indices.exists_type:
  description: |-
    Returns information about whether a particular document type exists. (DEPRECATED)

indices.flush:
  description: |-
    Performs the flush operation on one or more indices.

indices.flush_synced:
  description: |-
    Performs a synced flush operation on one or more indices.

indices.forcemerge:
  description: |-
    Performs the force merge operation on one or more indices.

indices.get:
  description: |-
    Returns information about one or more indices.

indices.get_alias:
  description: |-
    Returns an alias.

indices.get_field_mapping:
  description: |-
    Returns mapping for one or more fields.

indices.get_mapping:
  description: |-
    Returns mappings for one or more indices.

indices.get_settings:
  description: |-
    Returns settings for one or more indices.

indices.get_template:
  description: |-
    Returns an index template.

indices.get_upgrade:
  description: |-
    The _upgrade API is no longer useful and will be removed.

indices.open:
  description: |-
    Opens an index.

indices.put_alias:
  description: |-
    Creates or updates an alias.

indices.put_mapping:
  description: |-
    Updates the index mappings.

indices.put_settings:
  description: |-
    Updates the index settings.

indices.put_template:
  description: |-
    Creates or updates an index template.

indices.recovery:
  description: |-
    Returns information about ongoing index shard recoveries.

indices.refresh:
  description: |-
    Performs the refresh operation in one or more indices.

indices.rollover:
  description: |-
    Updates an alias to point to a new index when the existing index
    is considered to be too large or too old.

indices.segments:
  description: |-
    Provides low-level information about segments in a Lucene index.

indices.shard_stores:
  description: |-
    Provides store information for shard copies of indices.

indices.shrink:
  description: |-
    Allow to shrink an existing index into a new index with fewer primary shards.

indices.split:
  description: |-
    Allows you to split an existing index into a new index with more primary shards.

indices.stats:
  description: |-
    Provides statistics on operations happening in an index.

indices.update_aliases:
  description: |-
    Updates index aliases.

indices.upgrade:
  description: |-
    The _upgrade API is no longer useful and will be removed.

indices.validate_query:
  description: |-
    Allows a user to validate a potentially expensive query without executing it.

info:
  description: |-
    Returns basic information about the cluster.

ingest.delete_pipeline:
  description: |-
    Deletes a pipeline.

ingest.get_pipeline:
  description: |-
    Returns a pipeline.

ingest.processor_grok:
  description: |-
    Returns a list of the built-in patterns.

ingest.put_pipeline:
  description: |-
    Creates or updates a pipeline.

ingest.simulate:
  description: |-
    Allows to simulate a pipeline with example documents.

mget:
  description: |-
    Allows to get multiple documents in one request.

msearch:
  description: |-
    Allows to execute several search operations in one request.

msearch_template:
  description: |-
    Allows to execute several search template operations in one request.

mtermvectors:
  description: |-
    Returns multiple termvectors in one request.

nodes.hot_threads:
  description: |-
    Returns information about hot threads on each node in the cluster.

nodes.info:
  description: |-
    Returns information about nodes in the cluster.

nodes.reload_secure_settings:
  description: |-
    Reloads secure settings.

nodes.stats:
  description: |-
    Returns statistical information about nodes in the cluster.

nodes.usage:
  description: |-
    Returns low-level information about REST actions usage on nodes.

ping:
  description:
    Returns whether the cluster is running.

put_script:
  description: |-
    Creates or updates a script.

rank_eval:
  description: |-
    Allows to evaluate the quality of ranked search results over a set of typical search queries

reindex:
  description: |-
    Allows to copy documents from one index to another, optionally filtering the source
    documents by a query, changing the destination index settings, or fetching the
    documents from a remote cluster.

reindex_rethrottle:
  description: |-
    Changes the number of requests per second for a particular Reindex operation.

render_search_template:
  description: |-
    Allows to use the Mustache language to pre-render a search definition.

scripts_painless_execute:
  description: |-
    Allows an arbitrary script to be executed and a result to be returned

scripts_painless_context:
  description: |-
    Allows to query context information.

scroll:
  description: |-
    Allows to retrieve a large numbers of results from a single search request.

search:
  description: |-
    Returns results matching a query.

search_shards:
  description: |-
    Returns information about the indices and shards that a search request would be executed against.

search_template:
  description: |-
    Allows to use the Mustache language to pre-render a search definition.

snapshot.create:
  description: |-
    Creates a snapshot in a repository.

snapshot.create_repository:
  description: |-
    Creates a repository.

snapshot.delete:
  description: |-
    Deletes a snapshot.

snapshot.delete_repository:
  description: |-
    Deletes a repository.

snapshot.get:
  description: |-
    Returns information about a snapshot.

snapshot.get_repository:
  description: |-
    Returns information about a repository.

snapshot.restore:
  description: |-
    Restores a snapshot.

snapshot.status:
  description: |-
    Returns information about the status of a snapshot.

snapshot.verify_repository:
  description: |-
    Verifies a repository.

tasks.cancel:
  description: |-
    Cancels a task, if it can be cancelled through an API.

tasks.get:
  description: |-
    Returns information about a task.

tasks.list:
  description: |-
    Returns a list of tasks.

termvectors:
  description: |-
    Returns information and statistics about terms in the fields of a particular document.

update:
  description: |-
    Updates a document with a script or partial document.

update_by_query:
  description: |-
    Performs an update on every document in the index without changing the source,
    for example to pick up a mapping change.

update_by_query_rethrottle:
  description: |-
    Changes the number of requests per second for a particular Update By Query operation.
`
