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

The release process is run by a release manager volunteering from amongst [MAINTAINERS](MAINTAINERS.md). A release is two pull requests, both merged with **Squash and merge**:

- **Prepare**: the [CHANGELOG](CHANGELOG.md) and [version.go](internal/version/version.go).
- **Pin**: every nested module requires the root module at the prepare PR's squash commit. The root tag goes on this PR's squash commit.

The make targets below only edit files and check state. You run every `git commit`, `git push` and `gh pr create` yourself, and file the tag request yourself. Start with:

```sh
make release release-tag=vX.Y.Z
```

It prints the whole runbook for that release with the branch names, commit messages, pull request titles and bodies filled in, and marks the step you are on, worked out from `upstream/main`. Run it again after each step. It also warns when the nested modules on `upstream/main` require a root version the go command cannot resolve, as happens when the commit they pin was squashed away; the release's pin step repairs that. Without `release-tag`, it prints the latest tag and the commands for the next patch and minor release. `UPSTREAM_REMOTE` (default `upstream`) and `RELEASE_REMOTE` (default `public-fork`, the fork you open pull requests from) name the remotes. Every printed command is safe to paste as-is.

1. For major version releases, ensure that all references are up-to-date, e.g. `github.com/opensearch-project/opensearch-go/v3`, see [opensearch-go#444](https://github.com/opensearch-project/opensearch-go/pull/444).
2. **Prepare.** On a branch off `upstream/main`, `make release-prepare release-tag=vX.Y.Z` moves the `Unreleased` CHANGELOG entries to `[X.Y.Z]`, drops empty subsections, starts a fresh `Unreleased`, adds the comparison link (and the previous version's, if it is missing), and sets `version.go` to `X.Y.Z`. It does not commit. Edit the release notes, then commit, push, open the pull request and squash-merge it, using the commands `make release` prints. `make release` refuses to show the commit as ready while `[X.Y.Z]` has no entries, unless you pass `allow-empty=1`.
3. **Pin.** On a new branch off `upstream/main`, `make release-pin release-tag=vX.Y.Z` finds the prepare PR's squash commit on `upstream/main`, asks `go list -m` for its pseudo-version, and in each nested module that requires the root module runs `go mod edit -require=<root>@<that version>`, `go mod tidy`, `go build ./...` and `go vet ./...`, echoing each command. It refuses if the root module on `upstream/main` has changed since the prepare commit, since the tag would then carry changes the nested modules do not pin; release the next version instead. Commit, push, open the pull request and squash-merge it. Merge no other pull request that changes the root module between the prepare and pin merges.
4. **Verify and request the tag.** `make release-verify release-tag=vX.Y.Z` is read-only. It finds the pin PR's squash commit and checks that every nested module requires the root at the prepare commit's pseudo-version, that `go list -m` names the prepare commit with that pseudo-version, that the root module content at the pin commit equals the prepare commit's, that `version.go` is `X.Y.Z`, and that each nested module passes `go mod tidy -diff`, `go build` and `go vet` against the real module proxy and checksum database. It then prints a link to the [GitHub Request](https://github.com/opensearch-project/.github/issues/new?template=GITHUB_REQUEST_TEMPLATE.yaml) form in opensearch-project/.github with the `[GitHub Request] Tag new opensearch-go vX.Y.Z release` title filled in, and the text to paste into each field, naming the pin commit and linking the `version.go` and CHANGELOG changes in the prepare PR. File it through the form, which adds the `github-request` label the admins triage from; `gh issue create --label` drops the label unless you have push access to opensearch-project/.github. Contributors cannot push signed tags; a member of @opensearch-project/admin creates it, which [makes the new version available](https://go.dev/doc/modules/publishing) on [pkg.go.dev](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5). Once the tag exists, run it again: it also checks that the tag is signed, points at the pin commit, and is served by the module proxy from that commit.
5. **Publish the GitHub release.** Once the tag exists, `make release` and `make release-verify` print a `gh release create` command whose notes are the tag's CHANGELOG section. Publishing triggers the [Refresh pkg.go.dev](.github/workflows/pkg-go-dev-refresh.yml) workflow, which requests the new tag from the Go module proxy. If that workflow fails, run the same request locally: `go list -m github.com/opensearch-project/opensearch-go/v5@vX.Y.Z`. Then delete the two release branches, locally and on your fork.

### Nested modules

The repository ships nested modules -- `make print-submodules` lists them. A nested module declares its path as the repository, then the subdirectory, then the major-version suffix **last**:

```
module github.com/opensearch-project/opensearch-go/osprom/v5
```

Go reads a `/vN` element as a major version only as the final path element, so a path like `.../v5/osprom` makes Go treat `v5/osprom` as the subdirectory with no major-version suffix. The declared path must end in `/vN`.

Nested modules are not tagged. A consumer names one by commit, and Go resolves it to a pseudo-version: `@latest` is the newest commit on `main`, and a release is the commit the root tag points at:

```sh
go get github.com/opensearch-project/opensearch-go/osprom/v5@latest
go get github.com/opensearch-project/opensearch-go/osprom/v5@<commit tagged vX.Y.Z>
```

`@vX.Y.Z` does not work for a nested module: Go looks for an `osprom/vX.Y.Z` tag.

A release pins each nested module to the root module it ships with. The pin commit requires the root at the pseudo-version of the prepare commit, `vA.B.(C+1)-0.<commit time>-<hash>` where `vA.B.C` is the previous tag. The root module's content is the same at the prepare commit and at the pin commit the tag points at, because the pin commit changes only nested modules, which the root module's zip leaves out. So the nested modules build against exactly what `vX.Y.Z` ships, and a consumer who also requires `vX.Y.Z` gets it, since minimal version selection picks the higher version.

The pin needs two pull requests because it must name a commit that already exists on `main`. A squash merge creates a new commit whose hash depends on the merge time, the merged-into `main` and GitHub's signature, so it cannot be known before the merge, and a commit cannot name its own hash.
