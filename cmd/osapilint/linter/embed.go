// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import _ "embed"

// The committed surfaces are embedded so the tool is self-contained: a consumer
// runs the binary without needing the surface files alongside. Regenerate with
// cmd/gensurface and re-embed when bumping the pinned opensearch-go versions.
// Register each embedded surface in the surfaces map (transitions.go). The v4
// and v5 surfaces have plugin companions, generated with the same command over
// ./plugins/security and ./plugins/ism and registered in pluginSurfaces.
//
//go:embed surface_v2.json
var surfaceV2JSON []byte

//go:embed surface_v3.json
var surfaceV3JSON []byte

//go:embed surface_v4.json
var surfaceV4JSON []byte

//go:embed surface_v5.json
var surfaceV5JSON []byte

//go:embed surface_v4_plugins.json
var surfaceV4PluginsJSON []byte

//go:embed surface_v5_plugins.json
var surfaceV5PluginsJSON []byte
