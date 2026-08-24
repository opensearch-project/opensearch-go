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
	Src   Major  // source major; 0 means auto-detect from the module's imports
	Dst   Major  // target major; 0 means newest known
	Write bool   // false = dry run (compute edits, write nothing)
}

// SDKResult is the outcome of one adjacent hop within the migration chain.
type SDKResult struct {
	From, To  Major
	Files     []Result // per-file edits, the same Result type Walk returns
	Followups []string // behavioral changes not rewritten automatically
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
	dst := cfg.Dst
	if dst == 0 {
		dst = newestKnownTarget()
	}

	src := cfg.Src
	var warnings []string
	if src == 0 {
		m, w, err := detectSourceMajor(cfg.Dir)
		if err != nil {
			return nil, err
		}
		src, warnings = m, w
	}

	if src >= dst {
		return nil, nil
	}

	plans, err := planChain(src, dst)
	if err != nil {
		return nil, err
	}

	results := make([]SDKResult, 0, len(plans))
	for i, p := range plans {
		rr, err := runTypeAwareRewrite(rewriteConfig{
			dir:            cfg.Dir,
			patterns:       []string{patternAll},
			delta:          p.delta,
			renames:        p.renames,
			regroups:       p.regroups,
			removedHelpers: p.removedHelpers,
			importPrefixes: p.importPrefixes,
			write:          cfg.Write,
		})
		if err != nil {
			return results, fmt.Errorf("v%d -> v%d: %w", p.from, p.to, err)
		}

		files := make([]Result, len(rr))
		for j, r := range rr {
			files[j] = Result{Path: r.path, Edits: r.edits}
		}
		results = append(results, SDKResult{
			From:      p.from,
			To:        p.to,
			Files:     files,
			Followups: p.followups,
		})

		// Between hops, point the module at the intermediate version and rebuild so
		// the next hop's type-aware pass resolves against it. Dry runs skip this.
		isLast := i == len(plans)-1
		if cfg.Write && !isLast {
			if err := bumpAndBuild(ctx, cfg.Dir, p.to); err != nil {
				return results, fmt.Errorf("preparing v%d before the next hop: %w", p.to, err)
			}
		}
	}

	if len(results) > 0 {
		results[0].Warnings = warnings
	}
	return results, nil
}
