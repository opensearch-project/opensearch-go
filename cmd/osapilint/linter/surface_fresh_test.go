// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/cmd/osapilint/v5/internal/apirev"
)

const (
	rootModule     = "../../.."
	rootModulePath = "github.com/opensearch-project/opensearch-go/v5"
)

var (
	clientVersionRe = regexp.MustCompile(`(?m)^const Client = "(\d+)\.(\d+)\.(\d+)"`)
	coreSurfacePats = []string{"./opensearchapi", ".", "./opensearchtransport"}
)

// TestSurfaceV5_2Fresh regenerates the v5.2 surfaces from the enclosing tree
// and requires them byte-equal to the committed files, so API drift on the
// development line fails here rather than going unrecorded. The surfaces are
// frozen once the tree reaches the release.
func TestSurfaceV5_2Fresh(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("regenerating surfaces type-checks the whole module")
	}

	gomod, err := os.ReadFile(filepath.Join(rootModule, "go.mod"))
	require.NoError(t, err)
	if !strings.HasPrefix(string(gomod), "module "+rootModulePath+"\n") {
		t.Skipf("enclosing module is not %q", rootModulePath)
	}

	ver, err := os.ReadFile(filepath.Join(rootModule, "internal", "version", "version.go"))
	require.NoError(t, err)
	m := clientVersionRe.FindStringSubmatch(string(ver))
	require.NotNil(t, m, "no `const Client` in internal/version/version.go")
	maj, err := strconv.Atoi(m[1])
	require.NoError(t, err)
	minor, err := strconv.Atoi(m[2])
	require.NoError(t, err)
	if maj > 5 || (maj == 5 && minor >= 2) {
		t.Skipf("tree is at %s.%s.%s; surface_v5_2 is frozen at the 5.2.0 release", m[1], m[2], m[3])
	}

	entries, err := os.ReadDir(filepath.Join(rootModule, "plugins"))
	require.NoError(t, err)
	var pluginPats []string
	for _, e := range entries {
		if e.IsDir() {
			pluginPats = append(pluginPats, "./plugins/"+e.Name())
		}
	}

	for _, tc := range []struct {
		name     string
		file     string
		patterns []string
	}{
		{"core", "surface_v5_2.json", coreSurfacePats},
		{"plugins", "surface_v5_2_plugins.json", pluginPats},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			snap, err := apirev.ExtractFromDir(rootModule, "v5.2", true, tc.patterns...)
			require.NoError(t, err)
			got, err := json.MarshalIndent(snap, "", "  ")
			require.NoError(t, err)

			want, err := os.ReadFile(tc.file)
			require.NoError(t, err)
			require.Equal(t, string(want), string(got)+"\n",
				"%q is stale; regenerate it as described in cmd/osapilint/README.md, section %q (use -version v5.2 -symbols)",
				tc.file, "Adding a hop (e.g. v3 -> v4)")
		})
	}
}
