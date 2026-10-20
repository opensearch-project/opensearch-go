- [Overview](#overview)
- [Branching](#branching)
  - [Release Branching](#release-branching)
  - [Feature Branches](#feature-branches)
- [Release Labels](#release-labels)
- [Releasing](#releasing)
  - [Nested modules](#nested-modules)

## Overview

This document explains the release strategy for artifacts in this organization.

## Branching

### Release Branching

Given the current major release of 1.0, projects in this organization maintain the following active branches.

- **main**: The next _major_ release. This is the branch where all merges take place and code moves fast.
- **1.x**: The next _minor_ release. Once a change is merged into `main`, decide whether to backport it to `1.x`.
- **1.0**: The _current_ release. In between minor releases, only hotfixes (e.g. security) are backported to `1.0`.

Label PRs with the next major version label (e.g. `2.0.0`) and merge changes into `main`. Label PRs that you believe need to be backported as `1.x` and `1.0`. Backport PRs by checking out the versioned branch, cherry-pick changes and open a PR against each target backport branch.

### Feature Branches

Do not creating branches in the upstream repo, use your fork, for the exception of long lasting feature branches that require active collaboration from multiple developers. Name feature branches `feature/<thing>`. Once the work is merged to `main`, please make sure to delete the feature branch.

## Release Labels

Repositories create consistent release labels, such as `v1.0.0`, `v1.1.0` and `v2.0.0`, as well as `patch` and `backport`. Use release labels to target an issue or a PR for a given release. See [Triage Open Issues](https://github.com/opensearch-project/.github/blob/main/RESPONSIBILITIES.md#triage-open-issues) for more information on triaging issues.

## Releasing

The release process is run by a release manager volunteering from amongst [MAINTAINERS](MAINTAINERS.md). `make release` prints these steps and the commands for the next patch and minor release.

1. For major version releases, ensure that all references are up-to-date, e.g. `github.com/opensearch-project/opensearch-go/v3`, see [opensearch-go#444](https://github.com/opensearch-project/opensearch-go/pull/444).
2. Start the release:

   ```sh
   make start-release release-tag=vX.Y.Z
   ```

   It creates branch `release-vX.Y.Z` off `upstream/main` in a new worktree next to the main checkout (`<checkout>-release-vX.Y.Z`), so the checkout it runs from is untouched. There it moves the [CHANGELOG](CHANGELOG.md) `Unreleased` entries to `[X.Y.Z]`, drops empty subsections, starts a fresh `Unreleased`, adds the comparison link, and sets [version.go](internal/version/version.go) to `X.Y.Z`, leaving the edits uncommitted for review. If `Unreleased` had no entries, `[X.Y.Z]` is left empty for you to fill in. `UPSTREAM_REMOTE` (default `upstream`) and `RELEASE_REMOTE` (default `public-fork`) name the remotes.

3. In the worktree, edit the release notes, then open the pull request:

   ```sh
   make finish-release
   ```

   It refuses if CHANGELOG `[X.Y.Z]` has no entries; `allow-empty=1` releases anyway, with a bare heading. It then makes two commits:

   - **Prepare**: every tracked change in the worktree. Untracked files are listed and left out.
   - **Pin**: every nested module that requires the root module now requires it at the pseudo-version of the prepare commit, with `go.sum` written offline from that commit; see [Nested modules](#nested-modules).

   It then pushes the branch and opens the pull request, labeled `pending-next-release` so CI builds the nested modules against the checkout until the pinned commit is on `main`. `DRY_RUN=1` makes the commits but prints the push and `gh pr create` instead of running them; running `make finish-release` again after that, or after a failed push, picks up from the push.

   `make abort-release` discards the release instead: it removes the worktree and the branch, saving the uncommitted edits as a patch in `$TMPDIR` first. From outside the worktree, pass `release-tag=vX.Y.Z`. It refuses once the branch is on `RELEASE_REMOTE`; close the pull request and delete the remote branch first.

4. Merge the pull request with **Create a merge commit**. Squash and rebase rewrite the prepare commit that the nested modules pin. This needs merge commits allowed on the repository and the `main` ruleset's linear-history rule bypassed for the merge. Merge while the branch is current with `main`: if anything else lands first, the merge carries root changes the pin does not, and step 5 refuses it. To start over, close the pull request, delete the remote branch, run `make abort-release`, and start again.
5. Request the signed tag:

   ```sh
   make release-tag-request release-tag=vX.Y.Z
   ```

   It finds the merge commit on `upstream/main` that set `version.go` to `X.Y.Z` (`MERGE_COMMIT=<sha>` overrides the search) and verifies it: the nested modules all pin one pseudo-version of a commit in its history, the go command names that commit with the same pseudo-version, the root module there has the same content as at the merge, and each nested module tidies, builds and vets against the real module proxy and checksum database. It then files the `[GitHub Request] Tag new opensearch-go vX.Y.Z release` issue in [opensearch-project/.github](https://github.com/opensearch-project/.github/issues), naming the merge commit. Contributors cannot push signed tags; a member of @opensearch-project/admin creates it, which [makes the new version available](https://go.dev/doc/modules/publishing) on [pkg.go.dev](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5). Remove the release worktree afterwards with `git worktree remove`.

6. Once the tag exists, draft and publish a [new GitHub release](https://github.com/opensearch-project/opensearch-go/releases/new) from it, using the CHANGELOG section for that version as the release notes. Publishing triggers the [Refresh pkg.go.dev](.github/workflows/pkg-go-dev-refresh.yml) workflow, which requests the new tag from the Go module proxy. If that workflow fails, run the same request locally: `go list -m github.com/opensearch-project/opensearch-go/v5@vX.Y.Z`.

### Nested modules

The repository ships nested modules -- `make print-submodules` lists them. A nested module declares its path as the repository, then the subdirectory, then the major-version suffix **last**:

```
module github.com/opensearch-project/opensearch-go/osprom/v5
```

Go reads a `/vN` element as a major version only as the final path element, so a path like `.../v5/osprom` makes Go treat `v5/osprom` as the subdirectory with no major-version suffix. The declared path must end in `/vN`.

Nested modules are not tagged. A consumer names one by commit, and Go resolves it to a pseudo-version: `@latest` is the newest commit on `main`, and a release is the merge commit the root tag points at:

```sh
go get github.com/opensearch-project/opensearch-go/osprom/v5@latest
go get github.com/opensearch-project/opensearch-go/osprom/v5@<merge commit of vX.Y.Z>
```

`@vX.Y.Z` does not work for a nested module: Go looks for an `osprom/vX.Y.Z` tag.

A release pins each nested module to the root module it ships with. The pin commit requires the root at the pseudo-version of the prepare commit, `vA.B.(C+1)-0.<commit time>-<hash>` where `vA.B.C` is the previous tag. The root module's content is the same at the prepare commit, the merge commit and the `vX.Y.Z` tag, because the pin commit changes only nested modules, which the root module's zip leaves out. So the nested modules build against exactly what `vX.Y.Z` ships, and a consumer who also requires `vX.Y.Z` gets it, since minimal version selection picks the higher version.

The pseudo-version's checksum cannot come from the module proxy before the prepare commit is on `main`. [`.github/modproxy.go`](.github/modproxy.go) builds the root module zip from that commit, names it the way the go command would, and serves it from a scratch file proxy so `go mod tidy` records the checksum. `make release-tag-request` checks the result against the real proxy and checksum database after the merge.
