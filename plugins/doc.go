// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

/*
Package plugins is the parent of the OpenSearch plugin API packages.

It contains no exported symbols of its own. Each subpackage wraps a shared
opensearch.Client to provide strongly-typed access to a single OpenSearch plugin's
REST API, and is generated from the same OpenSearch API specification as the core
opensearchapi package. Subpackages include knn, ml, ism, security, security_analytics,
notifications, observability, and others.

All plugin clients follow the same constructor pattern: create a shared
opensearch.Client, then pass it to the plugin's NewClient. See the README.md in this
directory for the full usage pattern and the list of available plugins.
*/
package plugins
