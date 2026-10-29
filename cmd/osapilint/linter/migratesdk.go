// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"context"
	"fmt"
)

// SDKConfig configures a programmatic opensearch-go SDK migration.
type SDKConfig struct {
	Dir   string // module directory to migrate (the tool loads "./..." within it)
	Src   Major  // source major, migrated from its first release line; 0 means auto-detect from the module
	Dst   Major  // target major, migrated to its newest release line; 0 means newest known
	Write bool   // false = dry run (compute edits, write nothing)
}

// SDKResult is the outcome of one adjacent hop within the migration chain.
type SDKResult struct {
	// From and To are the majors of the lines the hop joins, so they are equal for
	// a hop within one major; FromVersion and ToVersion tell such hops apart.
	From, To Major
	// FromVersion and ToVersion label the release lines the hop joins, such as
	// "v4" and "v5", or "v5" and "v5.2" for a hop within one major.
	FromVersion, ToVersion string
	Files                  []Result // per-file edits, the same Result type Walk returns
	Followups              []string // behavioral changes not rewritten automatically
	// Warnings carries source-detection notes (e.g. the module imports multiple
	// majors). It is populated only on the first hop, and only when the source was
	// auto-detected (Src == 0); it is nil otherwise.
	Warnings []string
}

// MigrateSDK runs the opensearch-go SDK migration over cfg.Dir, applying each
// adjacent hop from source to target in series with full type resolution,
// rebuilding between hops when Write is set (so the next hop resolves types
// against the intermediate version). It is the library entrypoint behind the
// `osapilint rewrite` command: same planning, same hops, no stdout, no flags.
//
// Src/Dst of 0 mean auto-detect / newest-known, matching the CLI defaults. When
// the source already meets or exceeds the target there is no work: MigrateSDK
// returns (nil, nil). Otherwise it returns one SDKResult per hop applied. A
// migration failure is a normal error; there is no *UsageError from this path
// (the caller supplies validated config, not a command line). On a mid-chain
// failure the hops completed so far are returned alongside the error.
func MigrateSDK(ctx context.Context, cfg SDKConfig) ([]SDKResult, error) {
	dst := newestKnownTarget()
	if cfg.Dst != 0 {
		v, ok := newestLineOf(cfg.Dst)
		if !ok {
			return nil, noSurfaceError(fmt.Sprintf("v%d", cfg.Dst))
		}
		dst = v
	}

	var src version
	var warnings []string
	if cfg.Src == 0 {
		var err error
		src, warnings, err = detectSource(ctx, cfg.Dir)
		if err != nil {
			return nil, err
		}
	} else {
		v, ok := firstLineOf(cfg.Src)
		if !ok {
			return nil, noSurfaceError(fmt.Sprintf("v%d", cfg.Src))
		}
		src = v
	}
	return migrate(ctx, cfg.Dir, src, dst, cfg.Write, warnings)
}

// migrate applies the hops from src to dst; see MigrateSDK.
func migrate(ctx context.Context, dir string, src, dst version, write bool, warnings []string) ([]SDKResult, error) {
	if !src.less(dst) {
		return nil, nil
	}

	plans, err := planChain(src, dst)
	if err != nil {
		return nil, err
	}

	results := make([]SDKResult, 0, len(plans))
	for i, p := range plans {
		rr, err := runTypeAwareRewrite(rewriteConfig{
			dir:            dir,
			patterns:       []string{patternAll},
			delta:          p.delta,
			renames:        p.renames,
			regroups:       p.regroups,
			removedHelpers: p.removedHelpers,
			importPrefixes: p.importPrefixes,
			write:          write,
		})
		if err != nil {
			return results, fmt.Errorf("%q -> %q: %w", p.from, p.to, err)
		}

		files := make([]Result, len(rr))
		for j, r := range rr {
			files[j] = Result{Path: r.path, Edits: r.edits}
		}
		results = append(results, SDKResult{
			From:        p.from.major,
			To:          p.to.major,
			FromVersion: p.from.String(),
			ToVersion:   p.to.String(),
			Files:       files,
			Followups:   p.followups,
		})

		// Between hops, point the module at the intermediate version and rebuild so
		// the next hop's type-aware pass resolves against it. Dry runs skip this.
		isLast := i == len(plans)-1
		if write && !isLast {
			if err := bumpAndBuild(ctx, dir, p.to); err != nil {
				return results, fmt.Errorf("preparing %q before the next hop: %w", p.to, err)
			}
		}
	}

	if len(results) > 0 {
		results[0].Warnings = warnings
	}
	return results, nil
}
