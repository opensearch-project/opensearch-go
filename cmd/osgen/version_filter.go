// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"fmt"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/ir"
)

// VersionBound represents a version constraint with a comparison operator.
type VersionBound struct {
	Version  string // normalized semver (e.g. "1.0.0"); empty means unbounded
	Operator string // one of ">=", ">", "<=", "<"
}

// VersionRange defines the version window for code generation filtering.
type VersionRange struct {
	Min              VersionBound
	Max              VersionBound
	RemoveDeprecated VersionBound // treat deprecated-at <= this version as removed
	PreserveOptional bool
}

const (
	// versionLatest is the magic value for --max-version meaning "no ceiling".
	versionLatest = "latest"

	// versionEpoch is the magic value for --min-version meaning "no floor".
	versionEpoch = "epoch"

	// Breadcrumb mode flag values.
	breadcrumbModeAll   = "all"
	breadcrumbModeOlder = "older"
	breadcrumbModeNewer = "newer"
)

// BreadcrumbMode controls which excluded items get breadcrumb comments.
type BreadcrumbMode int

const (
	BreadcrumbAll   BreadcrumbMode = iota // emit for all excluded items
	BreadcrumbOlder                       // only items excluded by min-version (removed before floor)
	BreadcrumbNewer                       // only items excluded by max-version (added after ceiling)
)

// BreadcrumbConfig holds breadcrumb mode settings for each category.
type BreadcrumbConfig struct {
	Operations BreadcrumbMode
	Types      BreadcrumbMode
	Fields     BreadcrumbMode
	Paths      BreadcrumbMode
	Params     BreadcrumbMode
}

// IsDefault reports whether all breadcrumb modes are at their default
// (BreadcrumbAll). Used to detect non-default flag values that target a
// subcommand which has not yet wired breadcrumb emission through.
func (bc BreadcrumbConfig) IsDefault() bool {
	return bc.Operations == BreadcrumbAll &&
		bc.Types == BreadcrumbAll &&
		bc.Fields == BreadcrumbAll &&
		bc.Paths == BreadcrumbAll &&
		bc.Params == BreadcrumbAll
}

// ParseBreadcrumbMode parses a flag value into a BreadcrumbMode.
func ParseBreadcrumbMode(s string) (BreadcrumbMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case breadcrumbModeAll:
		return BreadcrumbAll, nil
	case breadcrumbModeOlder:
		return BreadcrumbOlder, nil
	case breadcrumbModeNewer:
		return BreadcrumbNewer, nil
	default:
		return BreadcrumbAll, fmt.Errorf(
			"invalid breadcrumb mode %q: must be %s, %s, or %s",
			s, breadcrumbModeAll, breadcrumbModeOlder, breadcrumbModeNewer,
		)
	}
}

// ParseVersionRange parses min/max/remove-deprecated flag values into a VersionRange.
func ParseVersionRange(minFlag, maxFlag, removeDeprecatedFlag string, preserveOptional bool) (VersionRange, error) {
	var minBound VersionBound
	if minFlag == versionEpoch || minFlag == "" {
		minBound = VersionBound{Version: "", Operator: ">="}
	} else {
		var err error
		minBound, err = parseBound(minFlag, ">=")
		if err != nil {
			return VersionRange{}, fmt.Errorf("--min-version: %w", err)
		}
	}

	var maxBound VersionBound
	if maxFlag == versionLatest || maxFlag == "" {
		maxBound = VersionBound{Version: "", Operator: "<="}
	} else {
		var err error
		maxBound, err = parseBound(maxFlag, "<=")
		if err != nil {
			return VersionRange{}, fmt.Errorf("--max-version: %w", err)
		}
	}

	var removeDepr VersionBound
	if removeDeprecatedFlag == versionEpoch || removeDeprecatedFlag == "" {
		removeDepr = VersionBound{Version: "", Operator: "<="}
	} else {
		var err error
		removeDepr, err = parseBound(removeDeprecatedFlag, "<=")
		if err != nil {
			return VersionRange{}, fmt.Errorf("--remove-deprecated: %w", err)
		}
	}

	return VersionRange{
		Min:              minBound,
		Max:              maxBound,
		RemoveDeprecated: removeDepr,
		PreserveOptional: preserveOptional,
	}, nil
}

// IsAll reports whether this range includes everything (the default state).
func (vr VersionRange) IsAll() bool {
	return vr.Min.Version == "" && vr.Max.Version == "" && vr.RemoveDeprecated.Version == ""
}

// Includes reports whether an item with the given version metadata
// falls within the range. Items without version annotations are always included.
func (vr VersionRange) Includes(versionAdded, versionRemoved, versionDeprecated string) bool {
	if vr.IsAll() {
		return true
	}

	if versionRemoved != "" && vr.Min.Version != "" {
		cmp := cmpVersions(versionRemoved, vr.Min.Version)
		switch vr.Min.Operator {
		case ">=":
			if cmp <= 0 {
				return false
			}
		case ">":
			if cmp <= 0 {
				return false
			}
		}
	}

	if versionAdded != "" && vr.Max.Version != "" {
		cmp := cmpVersions(versionAdded, vr.Max.Version)
		switch vr.Max.Operator {
		case "<=":
			if cmp > 0 {
				return false
			}
		case "<":
			if cmp >= 0 {
				return false
			}
		}
	}

	if versionDeprecated != "" && vr.RemoveDeprecated.Version != "" {
		cmp := cmpVersions(versionDeprecated, vr.RemoveDeprecated.Version)
		switch vr.RemoveDeprecated.Operator {
		case "<=":
			if cmp <= 0 {
				return false
			}
		case "<":
			if cmp < 0 {
				return false
			}
		}
	}

	return true
}

// ExclusionReason returns a human-readable reason why an item was excluded,
// or empty string if it was included. Used for breadcrumb generation.
type ExclusionReason struct {
	Name    string
	Reason  string
	IsOlder bool // true if excluded by min-version (removed), false if by max-version (added after)
}

// Exclusion returns an ExclusionReason if the item is excluded, or nil if included.
func (vr VersionRange) Exclusion(name, versionAdded, versionRemoved, versionDeprecated string) *ExclusionReason {
	if vr.Includes(versionAdded, versionRemoved, versionDeprecated) {
		return nil
	}

	if versionRemoved != "" && vr.Min.Version != "" {
		cmp := cmpVersions(versionRemoved, vr.Min.Version)
		excluded := false
		switch vr.Min.Operator {
		case ">=":
			excluded = cmp <= 0
		case ">":
			excluded = cmp <= 0
		}
		if excluded {
			return &ExclusionReason{
				Name:    name,
				Reason:  fmt.Sprintf("removed in OpenSearch %s", normalizeSemver(versionRemoved)),
				IsOlder: true,
			}
		}
	}

	if versionDeprecated != "" && vr.RemoveDeprecated.Version != "" {
		cmp := cmpVersions(versionDeprecated, vr.RemoveDeprecated.Version)
		excluded := false
		switch vr.RemoveDeprecated.Operator {
		case "<=":
			excluded = cmp <= 0
		case "<":
			excluded = cmp < 0
		}
		if excluded {
			return &ExclusionReason{
				Name:    name,
				Reason:  fmt.Sprintf("deprecated in OpenSearch %s (treated as removed)", normalizeSemver(versionDeprecated)),
				IsOlder: true,
			}
		}
	}

	reason := "excluded by version range"
	if versionAdded != "" {
		reason = fmt.Sprintf("requires OpenSearch >= %s", normalizeSemver(versionAdded))
	}
	return &ExclusionReason{
		Name:    name,
		Reason:  reason,
		IsOlder: false,
	}
}

// ShouldBreadcrumb reports whether an exclusion should be rendered as a
// breadcrumb comment given the mode.
func (mode BreadcrumbMode) ShouldBreadcrumb(exc *ExclusionReason) bool {
	if exc == nil {
		return false
	}
	switch mode {
	case BreadcrumbAll:
		return true
	case BreadcrumbOlder:
		return exc.IsOlder
	case BreadcrumbNewer:
		return !exc.IsOlder
	default:
		return true
	}
}

// shouldKeep reports whether an [ir.Exclusion] should be rendered given the
// mode. Mirrors [BreadcrumbMode.ShouldBreadcrumb] for the IR-side type used
// by api gen exclusions.
func (mode BreadcrumbMode) shouldKeep(exc ir.Exclusion) bool {
	switch mode {
	case BreadcrumbAll:
		return true
	case BreadcrumbOlder:
		return exc.IsOlder
	case BreadcrumbNewer:
		return !exc.IsOlder
	default:
		return true
	}
}

// filterExclusions returns the subset of in that mode admits, preserving
// order. Used by generateAPI to apply --version-breadcrumb-* flags before
// passing exclusions into the IR.
func filterExclusions(in []ir.Exclusion, mode BreadcrumbMode) []ir.Exclusion {
	if len(in) == 0 {
		return nil
	}
	out := make([]ir.Exclusion, 0, len(in))
	for _, e := range in {
		if mode.shouldKeep(e) {
			out = append(out, e)
		}
	}
	return out
}

func parseBound(s string, defaultOp string) (VersionBound, error) {
	op, ver := splitOperator(s)
	if op == "" {
		op = defaultOp
	}

	switch op {
	case ">=", ">", "<=", "<":
	default:
		return VersionBound{}, fmt.Errorf("unsupported operator %q", op)
	}

	if ver == "" {
		return VersionBound{}, fmt.Errorf("empty version string")
	}

	normalized := normalizeSemver(ver)
	if !semver.IsValid("v" + normalized) {
		return VersionBound{}, fmt.Errorf("invalid semver %q", ver)
	}

	return VersionBound{
		Version:  normalized,
		Operator: op,
	}, nil
}

func splitOperator(s string) (string, string) {
	if strings.HasPrefix(s, ">=") {
		return ">=", s[2:]
	}
	if strings.HasPrefix(s, "<=") {
		return "<=", s[2:]
	}
	if strings.HasPrefix(s, ">") {
		return ">", s[1:]
	}
	if strings.HasPrefix(s, "<") {
		return "<", s[1:]
	}
	return "", s
}

func cmpVersions(a, b string) int {
	return semver.Compare("v"+normalizeSemver(a), "v"+normalizeSemver(b))
}

func parseBreadcrumbFlags(ops, types, fields, paths, params string) (BreadcrumbConfig, error) {
	bcOps, err := ParseBreadcrumbMode(ops)
	if err != nil {
		return BreadcrumbConfig{}, fmt.Errorf("--version-breadcrumb-operations: %w", err)
	}
	bcTypes, err := ParseBreadcrumbMode(types)
	if err != nil {
		return BreadcrumbConfig{}, fmt.Errorf("--version-breadcrumb-types: %w", err)
	}
	bcFields, err := ParseBreadcrumbMode(fields)
	if err != nil {
		return BreadcrumbConfig{}, fmt.Errorf("--version-breadcrumb-fields: %w", err)
	}
	bcPaths, err := ParseBreadcrumbMode(paths)
	if err != nil {
		return BreadcrumbConfig{}, fmt.Errorf("--version-breadcrumb-paths: %w", err)
	}
	bcParams, err := ParseBreadcrumbMode(params)
	if err != nil {
		return BreadcrumbConfig{}, fmt.Errorf("--version-breadcrumb-params: %w", err)
	}
	return BreadcrumbConfig{
		Operations: bcOps,
		Types:      bcTypes,
		Fields:     bcFields,
		Paths:      bcPaths,
		Params:     bcParams,
	}, nil
}
