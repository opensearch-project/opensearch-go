// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseMajor(t *testing.T) {
	tests := []struct {
		in      string
		want    Major
		wantErr bool
	}{
		{"v4", 4, false},
		{"V4", 4, false},
		{"4", 4, false},
		{"v12", 12, false},
		{"", 0, true},
		{"v0", 0, true},
		{"vx", 0, true},
		{"-1", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseMajor(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestMajorOfImport(t *testing.T) {
	tests := []struct {
		path    string
		wantMaj Major
		wantIs  bool
	}{
		{"github.com/opensearch-project/opensearch-go/v4/opensearchapi", 4, true},
		{"github.com/opensearch-project/opensearch-go/v5", 5, true},
		{"github.com/opensearch-project/opensearch-go", 1, true}, // no /vN => v1
		{"github.com/opensearch-project/opensearch-go/opensearchtransport", 1, true},
		{"github.com/stretchr/testify/require", 0, false},
		{"encoding/json", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			maj, is := majorOfImport(tt.path)
			require.Equal(t, tt.wantIs, is)
			if tt.wantIs {
				require.Equal(t, tt.wantMaj, maj)
			}
		})
	}
}

func TestModulePath(t *testing.T) {
	tests := []struct {
		m    Major
		want string
	}{
		{1, "github.com/opensearch-project/opensearch-go"},
		{4, "github.com/opensearch-project/opensearch-go/v4"},
		{5, "github.com/opensearch-project/opensearch-go/v5"},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, modulePath(tt.m))
	}
}

func TestDirFromArg(t *testing.T) {
	// A real directory to point valid cases at.
	tmp := t.TempDir()

	tests := []struct {
		name    string
		arg     string
		want    string
		wantErr bool
	}{
		{name: "plain directory", arg: tmp, want: tmp},
		{name: "wildcard maps to base dir", arg: tmp + "/...", want: tmp},
		{name: "dot-slash wildcard maps to dot", arg: "./...", want: "."},
		{name: "bare wildcard maps to dot", arg: "...", want: "."},
		{name: "nonexistent directory errors", arg: filepath.Join(tmp, "nope"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := dirFromArg(tt.arg)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}

	// A file (not a directory) must be rejected.
	t.Run("file is not a directory", func(t *testing.T) {
		root, err := os.OpenRoot(tmp)
		require.NoError(t, err)
		defer root.Close()
		require.NoError(t, root.WriteFile("a.go", []byte("package a\n"), 0o644))
		_, err = dirFromArg(filepath.Join(tmp, "a.go"))
		require.Error(t, err)
	})
}
