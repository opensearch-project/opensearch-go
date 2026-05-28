// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit

// SharedParamsFragment renders type aliases for the TimeoutParams and
// DebugParams structs defined in internal/params. Emitted once in the
// core package so that external consumers can reference opensearchapi.TimeoutParams
// and opensearchapi.DebugParams in composite literals.
type SharedParamsFragment struct{}

// Imports returns the imports the shared-params fragment needs.
func (f *SharedParamsFragment) Imports() []Import {
	return []Import{
		{Path: LocalModule + "/internal/params", Alias: "osparams"},
	}
}

// Body renders the TimeoutParams/DebugParams type aliases.
func (f *SharedParamsFragment) Body() (string, error) {
	return sharedParamsBody, nil
}

const sharedParamsBody = `// TimeoutParams holds timeout parameters shared across many operations.
// Embedded in every per-operation Params struct.
type TimeoutParams = osparams.TimeoutParams

// DebugParams holds diagnostic and display parameters.
// Embedded in every per-operation Params struct.
type DebugParams = osparams.DebugParams
`
