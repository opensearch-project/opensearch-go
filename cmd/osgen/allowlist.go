// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
)

// Two generation guards pin a permitted set of generated output in a checked-in
// allowlist: the json.RawMessage guard (see [guardRawMessages]) and the
// duplicate-JSON-tag guard (see [guardTagShadows]). Both share one file format -
// a header, then one "key # comment" line per entry under a banner per schema
// group - and one enforcement shape: rewrite under Update, otherwise check the
// embedded copy (or an override file), warn about entries nothing emits any more,
// and fail on an unlisted entry unless the check is advisory. This file holds the
// format and that shape, so a guard supplies only what it collects and the nouns
// it reports.

// allowlistEntry is one occurrence a guard tracks. Implemented by [rawUse] and
// [tagShadow].
type allowlistEntry interface {
	// key is the allowlist line key, and the only part a check compares.
	key() string
	// groupName is the schema or operation group the entry is banner-grouped
	// under. Not part of the key.
	groupName() string
	// comment is the trailing "# ..." detail written beside the key.
	// Informational: it is stripped on load.
	comment() string
}

// AllowlistConfig controls one guard's allowlist.
type AllowlistConfig struct {
	// AllowlistPath overrides the guard's embedded allowlist with a file,
	// relative to cwd. Empty (the default) checks against the embedded copy.
	// Under Update it is the write target, defaulting to the guard's own
	// filename in cwd when empty.
	AllowlistPath string
	// Update rewrites the allowlist file from the current output instead of
	// checking against it.
	Update bool
	// AllowUnlisted downgrades the fatal check to a warning.
	AllowUnlisted bool
}

// allowlistSource names the allowlist a check consulted, for messages.
// embeddedFile is the guard's checked-in filename, reported when no override is
// set.
func (c AllowlistConfig) allowlistSource(embeddedFile string) string {
	if c.AllowlistPath == "" {
		return "embedded " + embeddedFile
	}
	return fmt.Sprintf("%q", c.AllowlistPath)
}

// guardConfig carries both generation guards' settings. They are named rather
// than passed as two positional AllowlistConfig values so a call site cannot
// transpose them: the two are the same type and swapping them would still
// compile, while checking each guard against the other's allowlist.
type guardConfig struct {
	RawMessage AllowlistConfig
	TagShadow  AllowlistConfig
}

// parseAllowlist parses allowlist bytes into a set of keys. Lines are trimmed,
// '#' comments (whole-line or trailing) are stripped, and blank lines are
// ignored. The key is the first whitespace-delimited token on each line.
func parseAllowlist(data []byte) set[string] {
	allowed := make(set[string])
	for line := range strings.SplitSeq(string(data), "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		allowed.add(strings.Fields(line)[0])
	}
	return allowed
}

// loadAllowlist reads an allowlist file and parses it. noun names the allowlist
// in the not-found error (e.g. "json.RawMessage") and updateFlag is the flag that
// would create it.
func loadAllowlist(path, noun, updateFlag string) (set[string], error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s allowlist %q not found; run with %s to create it: %w", noun, path, updateFlag, err)
		}
		return nil, fmt.Errorf("reading allowlist %q: %w", path, err)
	}
	return parseAllowlist(data), nil
}

// resolveAllowlist returns the key set a check enforces: the guard's embedded
// copy, which cannot fail, or the file named by cfg.AllowlistPath. Under
// AllowUnlisted the check is advisory, so a missing override file yields an empty
// set and every entry falls through to the warning path rather than aborting.
func resolveAllowlist(cfg AllowlistConfig, embedded []byte, noun, updateFlag string) (set[string], error) {
	if cfg.AllowlistPath == "" {
		return parseAllowlist(embedded), nil
	}
	loaded, err := loadAllowlist(cfg.AllowlistPath, noun, updateFlag)
	switch {
	case cfg.AllowUnlisted && errors.Is(err, fs.ErrNotExist):
		return set[string]{}, nil
	case err != nil:
		return nil, err
	default:
		return loaded, nil
	}
}

// sortAllowlistEntries orders entries by group then key, for stable, grouped
// output. Each guard's collect step calls this, so [writeAllowlistFile] can
// assume the order.
func sortAllowlistEntries[T allowlistEntry](entries []T) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].groupName() != entries[j].groupName() {
			return entries[i].groupName() < entries[j].groupName()
		}
		return entries[i].key() < entries[j].key()
	})
}

// writeAllowlistFile rewrites an allowlist from entries: the guard's header, then
// one entry per line under a banner per group, for minimal diffs. entries is
// assumed pre-sorted by [sortAllowlistEntries].
func writeAllowlistFile[T allowlistEntry](path, header string, entries []T) (bool, error) {
	var b strings.Builder
	b.WriteString(header)

	var group string
	first := true
	for _, e := range entries {
		if first || e.groupName() != group {
			group = e.groupName()
			first = false
			label := group
			if label == "" {
				label = "(ungrouped)"
			}
			fmt.Fprintf(&b, "\n# --- %s ---\n", label)
		}
		fmt.Fprintf(&b, "%s # %s\n", e.key(), e.comment())
	}

	return writeIfChanged(path, []byte(b.String()))
}

// reportStaleAllowlist warns to w about allowed keys that no entry emits any
// more. Stale entries are never fatal: they permit nothing, and failing on them
// would break unrelated spec edits.
func reportStaleAllowlist[T allowlistEntry](w io.Writer, allowed set[string], entries []T, noun, updateFlag string) {
	emitted := make(set[string], len(entries))
	for _, e := range entries {
		emitted.add(e.key())
	}

	var stale []string
	for k := range allowed {
		if !emitted.has(k) {
			stale = append(stale, k)
		}
	}
	if len(stale) == 0 {
		return
	}
	sort.Strings(stale)

	fmt.Fprintf(w, "NOTE: %d %s allowlist entr%s no longer present in output; run %s to prune:\n",
		len(stale), noun, plural(len(stale), "y is", "ies are"), updateFlag)
	for _, k := range stale {
		fmt.Fprintf(w, "  - %s\n", k)
	}
}

// unlistedEntries returns the entries no allowlist key permits.
func unlistedEntries[T allowlistEntry](entries []T, allowed set[string]) []T {
	var offenders []T
	for _, e := range entries {
		if !allowed.has(e.key()) {
			offenders = append(offenders, e)
		}
	}
	return offenders
}

// plural picks the singular or plural form based on n.
func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return singular
	}
	return pluralForm
}
