// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build ignore

// changelog moves the Unreleased section of CHANGELOG.md to a release heading,
// drops its empty subsections, starts a fresh Unreleased template, and updates
// the compare links at the bottom. See the release target in the Makefile.
package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
)

const unreleasedTemplate = `## [Unreleased]

### Added

### Changed

### Deprecated

### Removed

### Fixed

### Security

### Dependencies

`

var tagPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

func main() {
	var (
		file = flag.String("file", "CHANGELOG.md", "changelog to rewrite")
		tag  = flag.String("tag", "", "release tag, e.g. v5.1.1")
		prev = flag.String("prev", "", "previous release tag, e.g. v5.1.0")
		repo = flag.String("repo", "https://github.com/opensearch-project/opensearch-go", "repository URL for compare links")
	)
	flag.Parse()

	src, err := os.ReadFile(*file)
	if err != nil {
		fail(err)
	}
	out, err := roll(string(src), *tag, *prev, *repo)
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(*file, []byte(out), 0o644); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "changelog:", err)
	os.Exit(1)
}

type subsection struct {
	heading string
	body    []string
}

func roll(src, tag, prev, repo string) (string, error) {
	if !tagPattern.MatchString(tag) || !tagPattern.MatchString(prev) {
		return "", fmt.Errorf("tag %q and prev %q must look like vX.Y.Z", tag, prev)
	}
	version := strings.TrimPrefix(tag, "v")
	lines := strings.Split(src, "\n")

	start, end := -1, len(lines)
	for i, l := range lines {
		switch {
		case l == "## [Unreleased]":
			start = i
		case start >= 0 && i > start && strings.HasPrefix(l, "## ["):
			end = i
		}
		if start >= 0 && end != len(lines) {
			break
		}
	}
	if start < 0 {
		return "", fmt.Errorf("no %q heading", "## [Unreleased]")
	}

	var subs []subsection
	for _, l := range lines[start+1 : end] {
		if strings.HasPrefix(l, "### ") {
			subs = append(subs, subsection{heading: l})
			continue
		}
		if len(subs) > 0 {
			subs[len(subs)-1].body = append(subs[len(subs)-1].body, l)
		}
	}

	var released strings.Builder
	released.WriteString("## [" + version + "]\n\n")
	for _, s := range subs {
		body := strings.TrimSpace(strings.Join(s.body, "\n"))
		if body == "" {
			continue
		}
		released.WriteString(s.heading + "\n\n" + body + "\n\n")
	}

	footerPrefix := "[Unreleased]: "
	footer := -1
	for i := end; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], footerPrefix) {
			footer = i
			break
		}
	}
	if footer < 0 {
		return "", fmt.Errorf("no %q link at the bottom", footerPrefix)
	}
	lines[footer] = fmt.Sprintf("[Unreleased]: %s/compare/%s...HEAD\n[%s]: %s/compare/%s...%s", repo, tag, version, repo, prev, tag)

	head := strings.Join(lines[:start], "\n")
	tail := strings.Join(lines[end:], "\n")
	if head != "" {
		head += "\n"
	}
	return head + unreleasedTemplate + released.String() + tail, nil
}
