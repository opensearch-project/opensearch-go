// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit

import (
	"sort"
	"strings"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

// LocalModule is the module path used to identify local imports for grouping.
const LocalModule = ir.ModulePath

// Import represents one import statement.
type Import struct {
	Path  string
	Alias string
}

// importSet accumulates unique imports and produces a sorted, grouped list.
type importSet struct {
	entries map[string]string // path -> alias
}

// Add adds an import path with no alias.
func (s *importSet) Add(path string) {
	if s.entries == nil {
		s.entries = make(map[string]string)
	}
	if _, ok := s.entries[path]; !ok {
		s.entries[path] = ""
	}
}

// AddAlias adds an import path with a specific alias.
func (s *importSet) AddAlias(path, alias string) {
	if s.entries == nil {
		s.entries = make(map[string]string)
	}
	s.entries[path] = alias
}

// Sorted returns all imports sorted by path for stable output.
func (s *importSet) Sorted() []Import {
	if len(s.entries) == 0 {
		return nil
	}
	result := make([]Import, 0, len(s.entries))
	for path, alias := range s.entries {
		result = append(result, Import{Path: path, Alias: alias})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Path < result[j].Path
	})
	return result
}

// Has returns true if the path is already in the set.
func (s *importSet) Has(path string) bool {
	if s.entries == nil {
		return false
	}
	_, ok := s.entries[path]
	return ok
}

// Grouped returns imports sorted and grouped like goimports -local:
// [0] = stdlib, [1] = third-party, [2] = local module.
// Empty groups are included to preserve index semantics but will have nil slices.
func (s *importSet) Grouped() [3][]Import {
	var groups [3][]Import
	for path, alias := range s.entries {
		imp := Import{Path: path, Alias: alias}
		switch importGroup(path) {
		case 0:
			groups[0] = append(groups[0], imp)
		case 1:
			groups[1] = append(groups[1], imp)
		case 2:
			groups[2] = append(groups[2], imp)
		}
	}
	for i := range groups {
		sort.Slice(groups[i], func(a, b int) bool {
			return groups[i][a].Path < groups[i][b].Path
		})
	}
	return groups
}

// importGroup classifies an import path:
// 0 = stdlib, 1 = third-party, 2 = local module.
func importGroup(path string) int {
	if strings.HasPrefix(path, LocalModule) {
		return 2
	}
	if strings.Contains(path, ".") {
		return 1
	}
	return 0
}
