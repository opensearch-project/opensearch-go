#!/usr/bin/env bash

# See the LICENSE file in the project root for more information.

# Release automation for the root module. See RELEASING.md.
#
#   release.sh overview     Show how to cut a release, and the next release tags.
#   release.sh start        Stage RELEASE_TAG in a new worktree for review.
#   release.sh finish       From that worktree: commit, pin nested modules, push, open the PR.
#   release.sh abort        Discard a started release.
#   release.sh tag-request  After the PR is merged, verify the merge commit and
#                           file the signed-tag request for it.
#
# Environment:
#   RELEASE_TAG      tag to release, e.g. v5.2.0 (start and tag-request; abort outside the worktree)
#   SUBMODULES       nested module directories (set by the Makefile)
#   UPSTREAM_REMOTE  remote for opensearch-project/opensearch-go (default: upstream)
#   RELEASE_REMOTE   remote to push the release branch to (default: public-fork)
#   ALLOW_EMPTY      finish only, non-empty: release with no CHANGELOG entries for RELEASE_TAG
#   MERGE_COMMIT     tag-request only: merge commit to use instead of searching
#   DRY_RUN          non-empty: print push, PR and issue commands instead of running them

set -o errexit -o nounset -o pipefail

UPSTREAM_REMOTE=${UPSTREAM_REMOTE:-upstream}
RELEASE_REMOTE=${RELEASE_REMOTE:-public-fork}
RELEASE_TAG=${RELEASE_TAG:-}
SUBMODULES=${SUBMODULES:-}
ALLOW_EMPTY=${ALLOW_EMPTY:-}
MERGE_COMMIT=${MERGE_COMMIT:-}
DRY_RUN=${DRY_RUN:-}

TOP=$(cd "$(dirname "$0")/.." >/dev/null && pwd -P)
cd "$TOP"

ROOT=$(awk '$1 == "module" { print $2 }' go.mod)
MAJOR=${ROOT##*/}
SLUG=${ROOT#github.com/}
SLUG=${SLUG%/"$MAJOR"}
TAG_REPO=opensearch-project/.github
PIN_SUBJECT="chore(release): pin nested modules to $ROOT@"

die() { printf '\033[31m[!] %s\033[0m\n' "$*" >&2; exit 1; }
step() { printf '\033[2m-> %s\033[0m\n' "$*"; }

# run executes a command that changes shared state, or prints it under DRY_RUN.
run() {
    if [ -n "$DRY_RUN" ]; then
        printf '   [dry-run]'; printf ' %q' "$@"; printf '\n'
    else
        "$@"
    fi
}

latest_tag() {
    git tag --list "$MAJOR.*" --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -n 1
}

next_tags() {
    local latest major minor patch
    latest=$(latest_tag)
    [ -n "$latest" ] || return 0
    IFS=. read -r major minor patch <<<"${latest#v}"
    printf 'Latest tag in %s: %s\n' "$UPSTREAM_REMOTE" "$latest"
    printf '  patch release: make %s release-tag=v%s.%s.%s\n' "$1" "$major" "$minor" "$((patch + 1))"
    printf '  minor release: make %s release-tag=v%s.%s.0\n' "$1" "$major" "$((minor + 1))"
}

usage() {
    printf 'Usage:   make %s release-tag=%s.Y.Z\n' "$1" "$MAJOR"
    next_tags "$1"
    exit 2
}

overview() {
    fetch_upstream
    local branch tag
    branch=$(git symbolic-ref --quiet --short HEAD 2>/dev/null || true)
    if tag=$(release_branch_tag "$branch"); then
        printf '\n\033[1mYou are staging %s in %s, on branch %s.\033[0m\n' "$tag" "$TOP" "$branch"
        printf 'Review and edit the release notes in CHANGELOG.md, then:\n\n'
        printf '  make finish-release   # commit, pin nested modules, push, open the PR\n'
        printf '  make abort-release    # discard this release\n\n'
        return
    fi
    cat <<USAGE

Cutting a release of $ROOT:

  1. make start-release release-tag=$MAJOR.Y.Z
       Creates branch release-$MAJOR.Y.Z off $UPSTREAM_REMOTE/main in a new worktree next to
       this checkout, with the CHANGELOG and version.go updated for review.
  2. In that worktree, edit the release notes, then:
       make finish-release     commit, pin nested modules, push, open the PR
                               (allow-empty=1 to release with no CHANGELOG entries)
       make abort-release      discard the release instead
  3. Merge the PR with "Create a merge commit".
  4. make release-tag-request release-tag=$MAJOR.Y.Z
       Verifies the merge commit and files the signed-tag request.

DRY_RUN=1 prints the push, PR and issue commands instead of running them.
See RELEASING.md.

USAGE
    next_tags start-release
}

# nested_requiring prints each nested module whose go.mod (in directory $1)
# requires the root module, with the version it requires.
nested_requiring() {
    local dir=$1 mod v
    for mod in $SUBMODULES; do
        [ -f "$dir/$mod/go.mod" ] || continue
        v=$(awk -v m="$ROOT" '$1 == m { print $2 } $1 == "require" && $2 == m { print $3 }' "$dir/$mod/go.mod")
        [ -z "$v" ] || printf '%s %s\n' "$mod" "$v"
    done
}

# version_gt reports whether vX.Y.Z $1 is greater than $2.
version_gt() {
    [ "$1" != "$2" ] && [ "$(printf '%s\n%s\n' "$1" "$2" | sort -t. -k1.2,1n -k2,2n -k3,3n | tail -n 1)" = "$1" ]
}

remote_owner() {
    local url
    url=$(git remote get-url "$1")
    url=${url%.git}
    url=${url%/*}
    printf '%s\n' "${url##*[:/]}"
}

main_worktree() {
    dirname "$(git rev-parse --path-format=absolute --git-common-dir)"
}

fetch_upstream() {
    command -v go >/dev/null || die "go is not on PATH"
    git remote get-url "$UPSTREAM_REMOTE" >/dev/null 2>&1 || die "no remote named $UPSTREAM_REMOTE (set UPSTREAM_REMOTE)"
    step "Fetching $UPSTREAM_REMOTE"
    git fetch --quiet --tags "$UPSTREAM_REMOTE" main
}

require_tag() {
    [ -n "$RELEASE_TAG" ] || usage "$1"
    [[ "$RELEASE_TAG" =~ ^${MAJOR}\.[0-9]+\.[0-9]+$ ]] || {
        printf '\033[31m[!] release-tag=%s is not a %s.Y.Z tag\033[0m\n' "$RELEASE_TAG" "$MAJOR" >&2
        usage "$1"
    }
}

require_gh() {
    [ -z "$DRY_RUN" ] || return 0
    command -v gh >/dev/null || die "gh is not on PATH"
    gh auth status >/dev/null 2>&1 || die "gh is not authenticated; run gh auth login"
}

# release_branch_tag prints the tag a release-vX.Y.Z branch name stands for.
release_branch_tag() {
    [[ "$1" =~ ^release-(${MAJOR}\.[0-9]+\.[0-9]+)$ ]] || return 1
    printf '%s\n' "${BASH_REMATCH[1]}"
}

pushed() {
    git remote get-url "$RELEASE_REMOTE" >/dev/null 2>&1 && [ -n "$(git ls-remote --heads "$RELEASE_REMOTE" "$1")" ]
}

# notes_empty reports whether CHANGELOG file $1 has no entries under "## [$2]".
notes_empty() {
    awk -v h="## [$2]" '$0 == h { in_section = 1; next } in_section && /^## \[/ { exit } in_section && NF && !/^### / { found = 1; exit } END { exit found }' "$1"
}

start() {
    fetch_upstream
    require_tag start-release
    local tag=$RELEASE_TAG version=${RELEASE_TAG#v} branch=release-$RELEASE_TAG prev wt
    prev=$(latest_tag)
    [ -n "$prev" ] || die "no $MAJOR.*.* tag found in $UPSTREAM_REMOTE"
    git rev-parse -q --verify "refs/tags/$tag" >/dev/null && die "tag $tag already exists"
    version_gt "$tag" "$prev" || die "$tag is not newer than the latest tag $prev"
    wt="$(main_worktree)-$branch"
    if git rev-parse -q --verify "refs/heads/$branch" >/dev/null; then
        die "branch $branch already exists. If it is a release in progress, continue it in its worktree ($wt) or run make abort-release release-tag=$tag; otherwise rename it: git branch -m $branch <name>"
    fi
    [ ! -e "$wt" ] || die "$wt already exists"
    ! pushed "$branch" || die "$RELEASE_REMOTE already has branch $branch"

    step "Creating $branch from $UPSTREAM_REMOTE/main ($(git rev-parse --short "$UPSTREAM_REMOTE/main")) in $wt"
    git worktree add --quiet -b "$branch" "$wt" "$UPSTREAM_REMOTE/main"
    # Remove what was created if the edits below fail, so start can be re-run.
    trap 'git worktree remove --force "$wt" 2>/dev/null; git branch -q -D "$branch" 2>/dev/null' EXIT

    step "Staging CHANGELOG.md and version.go for $tag"
    GOWORK=off go run .github/changelog.go -file "$wt/CHANGELOG.md" -tag "$tag" -prev "$prev"
    sed -e "s/^const Client = \".*\"\$/const Client = \"$version\"/" "$wt/internal/version/version.go" >"$wt/internal/version/version.go.new"
    grep -q "^const Client = \"$version\"\$" "$wt/internal/version/version.go.new" || die "could not set the version in internal/version/version.go"
    mv "$wt/internal/version/version.go.new" "$wt/internal/version/version.go"
    trap - EXIT

    printf '\n'
    git -C "$wt" --no-pager diff --stat
    printf '\n\033[1mYou are staging %s in %s, on branch %s.\033[0m\n' "$tag" "$wt" "$branch"
    if notes_empty "$wt/CHANGELOG.md" "$version"; then
        printf '\033[33mUnreleased had no entries, so [%s] in CHANGELOG.md is empty. Add the release notes there,\nor pass allow-empty=1 to make finish-release to release without any.\033[0m\n' "$version"
    fi
    printf 'Review and edit the release notes in CHANGELOG.md, then from that directory:\n\n'
    printf '  cd %s\n' "$wt"
    printf '  make finish-release   # commit, pin nested modules, push, open the PR\n'
    printf '  make abort-release    # discard this release\n\n'
}

finish() {
    local branch tag version
    branch=$(git symbolic-ref --quiet --short HEAD) || die "not on a branch; run make finish-release in the worktree make start-release created"
    tag=$(release_branch_tag "$branch") || die "$branch is not a release branch; run make finish-release in the worktree make start-release created"
    version=${tag#v}
    grep -qxF "## [$version]" CHANGELOG.md || die "CHANGELOG.md has no [$version] section"
    if notes_empty CHANGELOG.md "$version" && [ -z "$ALLOW_EMPTY" ]; then
        die "CHANGELOG.md has no entries under [$version]; add the release notes, or run make finish-release allow-empty=1 to release without any"
    fi
    fetch_upstream
    require_gh
    git rev-parse -q --verify "refs/tags/$tag" >/dev/null && die "tag $tag already exists"
    git merge-base --is-ancestor "$UPSTREAM_REMOTE/main" HEAD ||
        die "$UPSTREAM_REMOTE/main has moved since this release started; run make abort-release, then make start-release release-tag=$tag again"
    grep -qxF "const Client = \"$version\"" internal/version/version.go || die "internal/version/version.go is not $version"

    TMP=$(mktemp -d)
    trap 'chmod -R u+w "$TMP" 2>/dev/null; rm -rf "$TMP"' EXIT
    local tmp=$TMP pv prep

    if [[ "$(git show -s --format=%s HEAD)" == "$PIN_SUBJECT"* ]] && git diff --quiet HEAD; then
        # A previous finish made both commits; only the push and PR remain.
        pv=$(git show -s --format=%s HEAD)
        pv=${pv#"$PIN_SUBJECT"}
        prep=$(git rev-parse HEAD^)
        step "Commits already made; pushing $(git rev-parse --short HEAD)"
    else
        git diff --quiet HEAD && [ "$(git rev-list --count "$UPSTREAM_REMOTE/main..HEAD")" = 0 ] && die "nothing to release: no changes since $UPSTREAM_REMOTE/main"
        local untracked
        untracked=$(git ls-files --others --exclude-standard)
        [ -z "$untracked" ] || printf '\033[33mNot included (untracked):\033[0m\n%s\n' "$(printf '%s\n' "$untracked" | sed 's/^/   /')"
        if git diff --quiet HEAD; then
            step "Commit 1: nothing uncommitted; $(git show -s --format='%h %s' HEAD) is the prepare commit"
        else
            step "Commit 1: prepare $tag"
            git --no-pager diff --stat HEAD
            git add --update
            git commit --quiet --signoff --message "chore(release): prepare $tag"
        fi
        prep=$(git rev-parse HEAD)

        pv=$(GOWORK=off go run .github/modproxy.go -repo "$TOP" -rev "$prep" -module "$ROOT" -pseudo -out "$tmp/proxy")
        step "Commit 2: nested modules require $ROOT@$pv"
        local mod v pinned=()
        while read -r mod v; do
            printf '   %s (%s -> %s)\n' "$mod" "$v" "$pv"
            (
                cd "$TOP/$mod"
                goflags=$(go env GOFLAGS)
                goproxy=$(go env GOPROXY)
                export GOWORK=off GOMODCACHE="$tmp/mod" GOFLAGS="$goflags -modcacherw"
                export GOPROXY="file://$tmp/proxy,$goproxy" GONOSUMDB="$ROOT${GONOSUMDB:+,$GONOSUMDB}"
                go mod edit -require="$ROOT@$pv"
                go mod tidy
                go build ./...
                go vet ./...
            )
            pinned+=("$mod")
        done < <(nested_requiring "$TOP")
        [ ${#pinned[@]} -gt 0 ] || die "no nested module requires $ROOT"
        for mod in "${pinned[@]}"; do git add "$mod/go.mod" "$mod/go.sum"; done
        git commit --quiet --signoff --message "$PIN_SUBJECT$pv"
    fi

    local owner body=$tmp/pr.md mods
    mods=$(nested_requiring "$TOP" | awk '{ printf "%s%s", sep, $1; sep = ", " }')
    owner=$(remote_owner "$RELEASE_REMOTE" 2>/dev/null || echo "<owner>")
    {
        printf 'Release %s.\n\n' "$tag"
        printf -- '- %s: moves `Unreleased` to `[%s]`, and `internal/version/version.go` is `%s`.\n' "$(git rev-parse --short "$prep")" "$version" "$version"
        printf -- '- %s: the nested modules (%s) require `%s@%s`, the pseudo-version of the first commit, so they build against the same root content that `%s` will tag.\n\n' \
            "$(git rev-parse --short HEAD)" "$mods" "$ROOT" "$pv" "$tag"
        printf '**Merge with "Create a merge commit".** Squash and rebase rewrite %s, the commit the nested modules pin. After merging, run `make release-tag-request release-tag=%s`.\n\n' \
            "$(git rev-parse --short "$prep")" "$tag"
        printf 'By submitting this pull request, I confirm that my contribution is made under the terms of the Apache 2.0 license.\n'
        printf 'For more information on following Developer Certificate of Origin and signing off your commits, please check [here](https://github.com/opensearch-project/OpenSearch/blob/main/CONTRIBUTING.md#developer-certificate-of-origin).\n'
    } >"$body"

    step "Pushing $branch and opening the release PR"
    git remote get-url "$RELEASE_REMOTE" >/dev/null 2>&1 || die "no remote named $RELEASE_REMOTE (set RELEASE_REMOTE)"
    run git push --quiet --set-upstream "$RELEASE_REMOTE" "$branch"
    run gh pr create --repo "$SLUG" --base main --head "$owner:$branch" \
        --title "chore(release): cut $tag" --label pending-next-release --body-file "$body"
    if [ -n "$DRY_RUN" ]; then
        printf '\n'; sed 's/^/   | /' "$body"; printf '\n'
        git --no-pager log --format='   %h %s' "$UPSTREAM_REMOTE/main..HEAD"
    fi
    printf '\nAfter the PR is merged with "Create a merge commit":\n\n'
    printf '  make release-tag-request release-tag=%s\n\n' "$tag"
    printf 'Then remove this worktree:\n\n'
    printf '  cd %s && git worktree remove %s && git branch -D %s\n\n' "$(main_worktree)" "$TOP" "$branch"
}

abort() {
    local branch tag wt
    if [ -n "$RELEASE_TAG" ]; then
        require_tag abort-release
        tag=$RELEASE_TAG
        branch=release-$tag
    else
        branch=$(git symbolic-ref --quiet --short HEAD) || die "not on a branch; pass release-tag=vX.Y.Z"
        tag=$(release_branch_tag "$branch") || die "$branch is not a release branch; pass release-tag=vX.Y.Z"
    fi
    git rev-parse -q --verify "refs/heads/$branch" >/dev/null || die "no branch $branch"
    wt=$(git worktree list --porcelain | awk -v b="refs/heads/$branch" '$1 == "worktree" { w = substr($0, 10) } $1 == "branch" && $2 == b { print w }')
    if pushed "$branch"; then
        die "$branch is on $RELEASE_REMOTE; close its PR and delete the remote branch (git push $RELEASE_REMOTE --delete $branch) first"
    fi

    local patch
    patch=$(mktemp "${TMPDIR:-/tmp}"/opensearch-go-"$branch"-aborted-XXXXXX)
    patch=${patch//\/\//\/}
    {
        git --no-pager log --format='commit %H%n%s%n' "$UPSTREAM_REMOTE/main..$branch" 2>/dev/null || true
        [ -z "$wt" ] || git -C "$wt" --no-pager diff HEAD
    } >"$patch"
    step "Discarding $tag"
    git --no-pager log --format='   commit %h %s' "$UPSTREAM_REMOTE/main..$branch" 2>/dev/null || true
    [ -z "$wt" ] || git -C "$wt" status --short | sed 's/^/   /'
    printf '   saved to %s\n' "$patch"

    local main
    main=$(main_worktree)
    [ -z "$wt" ] || git -C "$main" worktree remove --force "$wt"
    git -C "$main" branch -q -D "$branch"
    step "Removed branch $branch${wt:+ and worktree $wt}"
    [ "$TOP" != "$wt" ] || printf '\nThis directory is gone; cd %s\n' "$main"
}

# find_merge prints the first-parent merge commit on upstream/main that set
# version.go to $1.
find_merge() {
    local want="const Client = \"$1\"" m
    for m in $(git rev-list --first-parent --merges -n 200 "$UPSTREAM_REMOTE/main"); do
        git show "$m:internal/version/version.go" 2>/dev/null | grep -qxF "$want" || continue
        git show "$m^1:internal/version/version.go" 2>/dev/null | grep -qxF "$want" && continue
        printf '%s\n' "$m"
        return
    done
}

tag_request() {
    fetch_upstream
    require_tag release-tag-request
    require_gh
    local tag=$RELEASE_TAG version=${RELEASE_TAG#v} merge
    git rev-parse -q --verify "refs/tags/$tag" >/dev/null && die "tag $tag already exists"
    merge=${MERGE_COMMIT:-$(find_merge "$version")}
    [ -n "$merge" ] || die "no merge commit on $UPSTREAM_REMOTE/main sets version.go to $version; set MERGE_COMMIT=<sha> if it was merged differently"
    merge=$(git rev-parse --verify "$merge^{commit}")
    git merge-base --is-ancestor "$merge" "$UPSTREAM_REMOTE/main" || die "$merge is not on $UPSTREAM_REMOTE/main"
    step "Release merge commit: $(git show -s --format='%h %s' "$merge")"

    TMP=$(mktemp -d)
    WT=$TMP/worktree
    trap 'git worktree remove --force "$WT" 2>/dev/null; chmod -R u+w "$TMP" 2>/dev/null; rm -rf "$TMP"' EXIT
    local tmp=$TMP wt=$WT
    git worktree add --quiet --detach "$wt" "$merge"

    grep -qxF "const Client = \"$version\"" "$wt/internal/version/version.go" || die "version.go at $merge is not $version"
    grep -qxF "## [$version]" "$wt/CHANGELOG.md" || die "CHANGELOG.md at $merge has no [$version] section"

    local mod v pv='' mods=()
    while read -r mod v; do
        [ -z "$pv" ] || [ "$v" = "$pv" ] || die "nested modules disagree on the root version: $mod requires $v, others $pv"
        pv=$v
        mods+=("$mod")
    done < <(nested_requiring "$wt")
    [ -n "$pv" ] || die "no nested module requires $ROOT at $merge"
    [[ "$pv" =~ [-.][0-9]{14}-[0-9a-f]{12}$ ]] || die "nested modules require $ROOT@$pv, not a pseudo-version"

    local prep
    prep=$(git rev-parse --verify "${pv##*-}^{commit}") || die "commit ${pv##*-} from $pv is not in this repository"
    git merge-base --is-ancestor "$prep" "$merge" || die "$prep, pinned by $pv, is not an ancestor of $merge"
    step "Nested modules pin $(git show -s --format='%h %s' "$prep")"

    local exclude=()
    for mod in $SUBMODULES; do exclude+=(":(exclude)$mod"); done
    if ! git diff --quiet "$prep" "$merge" -- . "${exclude[@]}"; then
        git --no-pager diff --stat "$prep" "$merge" -- . "${exclude[@]}" >&2
        die "the root module at $merge differs from $prep, so $tag would not match $pv"
    fi
    step "Root module content at $(git rev-parse --short "$merge") matches $(git rev-parse --short "$prep")"

    step "Checking against the Go module proxy and checksum database"
    local canon
    canon=$(cd "$tmp" && GOWORK=off GOMODCACHE="$tmp/mod" GOFLAGS="$(go env GOFLAGS) -modcacherw" go list -m "$ROOT@$prep")
    [ "$canon" = "$ROOT $pv" ] || die "the go command names $prep as ${canon#* }, but the nested modules require $pv"
    for mod in "${mods[@]}"; do
        printf '   %s\n' "$mod"
        (
            cd "$wt/$mod"
            goflags=$(go env GOFLAGS)
            export GOWORK=off GOMODCACHE="$tmp/mod" GOFLAGS="$goflags -modcacherw"
            go mod tidy -diff
            go build ./...
            go vet ./...
        )
    done

    local pr body=$tmp/issue.md title="[GitHub Request] Tag new opensearch-go $tag release"
    pr=$(gh api "repos/$SLUG/commits/$merge/pulls" --jq '.[0].html_url // empty' 2>/dev/null) || pr=
    [[ "$pr" == https://* ]] || pr=
    {
        printf '### What is the type of request?\n\nRepository Management\n\n'
        printf '### Details of the request\n\n'
        printf 'Please create a signed `%s` tag for `opensearch-go` from `%s` on the `main` branch.\n\n' "$tag" "$merge"
        printf -- '- repository: https://github.com/%s\n- target branch: `main`\n- module path: `%s`\n\n' "$SLUG" "$ROOT"
        printf '```\ngit tag -s %s %s -m "Tag %s"\n```\n\n' "$tag" "$merge" "$tag"
        printf '### Additional information to support your request\n\n'
        if [ -n "$pr" ]; then printf '[Release PR](%s)\n\n' "$pr"; else printf 'Release merge commit: https://github.com/%s/commit/%s\n\n' "$SLUG" "$merge"; fi
        printf '### When does this request need to be completed?\n\nASAP\n'
    } >"$body"

    step "Filing the tag request in $TAG_REPO"
    run gh issue create --repo "$TAG_REPO" --title "$title" --label github-request --body-file "$body"
    if [ -n "$DRY_RUN" ]; then
        printf '\n   %s\n' "$title"; sed 's/^/   | /' "$body"
    fi
}

case "${1:-}" in
overview) overview ;;
start) start ;;
finish) finish ;;
abort) abort ;;
tag-request) tag_request ;;
*) die "usage: $0 overview|start|finish|abort|tag-request" ;;
esac
