// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package path_test

import (
	"testing"

	"github.com/opensearch-project/opensearch-go/v5/internal/path"
)

func BenchmarkBuild_Search(b *testing.B) {
	p := path.SearchPath{Indices: []string{"my-index"}}
	b.ReportAllocs()
	for b.Loop() {
		s, _ := p.Build()
		_ = s
	}
}

func BenchmarkBuild_SearchMultiIndex(b *testing.B) {
	p := path.SearchPath{Indices: []string{"idx-1", "idx-2", "idx-3"}}
	b.ReportAllocs()
	for b.Loop() {
		s, _ := p.Build()
		_ = s
	}
}

func BenchmarkBuild_NodesInfo(b *testing.B) {
	p := path.NodesInfoPath{NodeID: []string{"node1", "node2"}, Metric: []string{"jvm", "os"}}
	b.ReportAllocs()
	for b.Loop() {
		s, _ := p.Build()
		_ = s
	}
}

func BenchmarkBuild_IndicesGetAlias(b *testing.B) {
	p := path.IndicesGetAliasPath{Indices: []string{"my-index"}, Name: []string{"my-alias"}}
	b.ReportAllocs()
	for b.Loop() {
		s, _ := p.Build()
		_ = s
	}
}

func BenchmarkBuild_IndicesCreate(b *testing.B) {
	p := path.IndicesCreatePath{Index: "my-index"}
	b.ReportAllocs()
	for b.Loop() {
		s, _ := p.Build()
		_ = s
	}
}
