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

package gentests

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v2"
)

var skipTests map[string][]string

func init() {
	err := yaml.NewDecoder(strings.NewReader(skipTestsYAML)).Decode(&skipTests)
	if err != nil {
		panic(fmt.Sprintf("ERROR: %v", err))
	}
}

var skipFiles = []string{
	"update/85_fields_meta.yml",            // Uses non-existing API property
	"update/86_fields_meta_with_types.yml", // --||--

	"search.highlight/20_fvh.yml", // bad backslash
}

// TODO: Comments into descriptions for `Skip()`
//
var skipTestsYAML = `
---
# Cannot distinguish between missing value for refresh and an empty string
bulk/50_refresh.yml:
  - refresh=empty string immediately makes changes are visible in search
bulk/51_refresh_with_types.yml:
  - refresh=empty string immediately makes changes are visible in search
create/60_refresh.yml:
  - When refresh url parameter is an empty string that means "refresh immediately"
create/61_refresh_with_types.yml:
  - When refresh url parameter is an empty string that means "refresh immediately"
delete/50_refresh.yml:
  - When refresh url parameter is an empty string that means "refresh immediately"
delete/51_refresh_with_types.yml:
  - When refresh url parameter is an empty string that means "refresh immediately"
index/60_refresh.yml:
  - When refresh url parameter is an empty string that means "refresh immediately"
index/61_refresh_with_types.yml:
  - When refresh url parameter is an empty string that means "refresh immediately"
update/60_refresh.yml:
  - When refresh url parameter is an empty string that means "refresh immediately"
update/61_refresh_with_types.yml:
  - When refresh url parameter is an empty string that means "refresh immediately"

# Stash in value
cluster.reroute/11_explain.yml:
nodes.info/30_settings.yml:
nodes.stats/20_response_filtering.yml:
nodes.stats/30_discovery.yml:
  - Discovery stats
nodes.discovery/30_discovery.yml:
  - Discovery stats

# Arbitrary key
indices.shrink/10_basic.yml:
indices.shrink/20_source_mapping.yml:
indices.shrink/30_copy_settings.yml:
indices.split/30_copy_settings.yml:
nodes.info/10_basic.yml:
nodes.reload_secure_settings/10_basic.yml:
nodes.stats/50_indexing_pressure.yml:
nodes.stats/40_store_stats.yml:
  - Store stats

# Parsed response is YAML: value is map[interface {}]interface {}, not map[string]interface {}
cat.aliases/20_headers.yml:
  - Simple alias with yaml body through Accept header

# Incorrect int instead of float in match (aggregations.date_range.buckets.0.from: 1000000); TODO: PR
search.aggregation/40_range.yml:
  - Date range

# Mismatch in number parsing, 8623000 != 8.623e+06
search.aggregation/340_geo_distance.yml:
  - avg_bucket

# Tries to match on "Cluster Get Settings" output, but that's an empty map
search/320_disallow_queries.yml:

# No support for headers per request yet
tasks.list/10_basic.yml:
  - tasks_list headers

# Node Selector feature not implemented
cat.aliases/10_basic.yml:
  - "Help (pre 7.4.0)"
  - "Simple alias (pre 7.4.0)"
  - "Complex alias (pre 7.4.0)"
  - "Column headers (pre 7.4.0)"
  - "Alias against closed index (pre 7.4.0)"

indices.put_mapping/10_basic.yml:
  - "Put mappings with explicit _doc type bwc"

# Test fails with: [400 Bad Request] illegal_argument_exception, "template [test] has index patterns [test-*] matching patterns from existing index templates [test2,test] with patterns (test2 => [test-*],test => [test-*, test2-*]), use index templates (/_index_template) instead"
test/indices.put_template/10_basic.yml:

# Incompatible regex
cat.templates/10_basic.yml:
  - "Sort templates"
  - "Multiple template"

# Missing test setup
cluster.voting_config_exclusions/10_basic.yml:
  - "Add voting config exclusion by unknown node name"

# Not relevant
search/issue4895.yml:
search/issue9606.yml:


# FIXME
bulk/80_cas.yml:
bulk/81_cas_with_types.yml:

# Cannot compare float64 ending with .0 reliably due to inconsistent serialisation (https://github.com/golang/go/issues/26363)
search/330_fetch_fields.yml:
  - Test nested field inside object structure

search/350_point_in_time.yml:
  - msearch

nodes.stats/11_indices_metrics.yml:
  - Metric - http

searchable_snapshots/10_usage.yml:
  - Tests searchable snapshots usage stats with full_copy and shared_cache indices

# Expects count 2 but returns only 1
service_accounts/10_basic.yml:
  - Test service account tokens
`
