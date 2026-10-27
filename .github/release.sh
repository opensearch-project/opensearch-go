#!/usr/bin/env bash

# See the LICENSE file in the project root for more information.

# Release helper for the root module. See RELEASING.md.
#
# A release is two squash-merged pull requests: a prepare PR (CHANGELOG and
# version.go), then a pin PR that makes every nested module require the root
# at the prepare PR's squash commit. The root tag goes on the pin PR's squash
# commit, whose root module content equals the prepare commit's.
#
# Nothing here commits, pushes, opens pull requests or files issues: each
# subcommand edits files or checks state, then prints the commands to run next.
#
#   release.sh plan     Print the runbook for RELEASE_TAG, marking the current step.
#   release.sh prepare  Edit CHANGELOG.md and version.go for RELEASE_TAG.
#   release.sh pin      Point the nested modules at the merged prepare commit.
#   release.sh verify   Check the merged pin commit and print the tag request form.
#
# Environment:
#   RELEASE_TAG      tag to release, e.g. v5.2.0
#   SUBMODULES       nested module directories (set by the Makefile)
#   UPSTREAM_REMOTE  remote for opensearch-project/opensearch-go (default: upstream)
#   RELEASE_REMOTE   remote the printed push commands use (default: public-fork)
#   ALLOW_EMPTY      non-empty: do not block on an empty CHANGELOG section

set -o errexit -o nounset -o pipefail

UPSTREAM_REMOTE=${UPSTREAM_REMOTE:-upstream}
RELEASE_REMOTE=${RELEASE_REMOTE:-public-fork}
RELEASE_TAG=${RELEASE_TAG:-}
SUBMODULES=${SUBMODULES:-}
ALLOW_EMPTY=${ALLOW_EMPTY:-}

TOP=$(cd "$(dirname "$0")/.." >/dev/null && pwd -P)
cd "$TOP"

ROOT=$(awk '$1 == "module" { print $2 }' go.mod)
MAJOR=${ROOT##*/}
SLUG=${ROOT#github.com/}
SLUG=${SLUG%/"$MAJOR"}
UPSTREAM=$UPSTREAM_REMOTE/main

die() { printf '\033[31m[!] %s\033[0m\n' "$*" >&2; exit 1; }
step() { printf '\033[2m-> %s\033[0m\n' "$*"; }
warn() { printf '\033[33m%s\033[0m\n' "$*"; }

# trace prints a command, then runs it.
trace() {
    { printf '\033[2m   +'; printf ' %q' "$@"; printf '\033[0m\n'; } >&2
    "$@"
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
    printf '  patch release: make release release-tag=v%s.%s.%s\n' "$major" "$minor" "$((patch + 1))"
    printf '  minor release: make release release-tag=v%s.%s.0\n' "$major" "$((minor + 1))"
}

fetch_upstream() {
    git remote get-url "$UPSTREAM_REMOTE" >/dev/null 2>&1 || die "no remote named $UPSTREAM_REMOTE (set UPSTREAM_REMOTE)"
    step "git fetch --quiet --tags $UPSTREAM_REMOTE main"
    git fetch --quiet --tags "$UPSTREAM_REMOTE" main
}

require_tag() {
    if [ -z "$RELEASE_TAG" ]; then
        printf 'Usage:   make %s release-tag=%s.Y.Z\n' "$1" "$MAJOR"
        next_tags
        exit 2
    fi
    [[ "$RELEASE_TAG" =~ ^${MAJOR}\.[0-9]+\.[0-9]+$ ]] || die "release-tag=$RELEASE_TAG is not a $MAJOR.Y.Z tag"
}

require_clean() {
    git diff --quiet HEAD || die "the working tree has uncommitted changes to tracked files; commit or stash them first"
    git merge-base --is-ancestor "$UPSTREAM" HEAD ||
        die "HEAD does not contain $UPSTREAM; start from it: git switch -c $1 $UPSTREAM"
}

# root_at prints the root version that nested module $2 requires at revision $1.
root_at() {
    git show "$1:$2/go.mod" 2>/dev/null | awk -v m="$ROOT" '$1 == m { print $2 } $1 == "require" && $2 == m { print $3 }'
}

# nested_requiring prints each nested module that requires the root at revision $1.
nested_requiring() {
    local mod
    for mod in $SUBMODULES; do
        [ -z "$(root_at "$1" "$mod")" ] || printf '%s\n' "$mod"
    done
}

# ROOT_SPEC is a pathspec for the root module's files: everything but the
# nested modules, which the root module's zip leaves out.
ROOT_SPEC=(.)
for mod in $SUBMODULES; do ROOT_SPEC+=(":(exclude)$mod"); done

# root_same reports whether the root module has the same content at $1 and $2.
root_same() {
    git diff --quiet "$1" "$2" -- "${ROOT_SPEC[@]}"
}

# notes_empty reports whether CHANGELOG text on stdin has no entries under "## [$1]".
notes_empty() {
    awk -v h="## [$1]" '$0 == h { in_section = 1; next } in_section && /^## \[/ { exit } in_section && NF && !/^### / { found = 1; exit } END { exit found }'
}

# prepare_commit prints the first-parent commit on upstream/main that set
# version.go to $1.
prepare_commit() {
    local want="const Client = \"$1\"" c
    for c in $(git log --first-parent --format=%H "$UPSTREAM" -- internal/version/version.go); do
        git show "$c:internal/version/version.go" | grep -qxF "$want" || continue
        git show "$c^:internal/version/version.go" 2>/dev/null | grep -qxF "$want" && continue
        printf '%s\n' "$c"
        return
    done
}

# pin_commit prints the first first-parent commit after $1 on upstream/main at
# which a nested module requires the root at a pseudo-version of $1.
pin_commit() {
    local short=${1:0:12} c mod
    for c in $(git rev-list --first-parent --reverse "$1..$UPSTREAM"); do
        for mod in $SUBMODULES; do
            [[ "$(root_at "$c" "$mod")" == *-"$short" ]] && { printf '%s\n' "$c"; return; }
        done
    done
}

# diff_anchor prints the fragment GitHub gives file $1 on a diff page: the
# SHA-256 of its path.
diff_anchor() {
    if command -v sha256sum >/dev/null; then
        printf '%s' "$1" | sha256sum
    else
        printf '%s' "$1" | shasum -a 256
    fi | cut -d' ' -f1
}

owner() {
    local url
    url=$(git remote get-url "$RELEASE_REMOTE" 2>/dev/null) || { printf '<owner>\n'; return; }
    url=${url%.git}
    url=${url%/*}
    printf '%s\n' "${url##*[:/]}"
}

apache_text() {
    printf 'By submitting this pull request, I confirm that my contribution is made under the terms of the Apache 2.0 license.\n'
    printf 'For more information on following Developer Certificate of Origin and signing off your commits, please check [here](https://github.com/opensearch-project/OpenSearch/blob/main/CONTRIBUTING.md#developer-certificate-of-origin).\n'
}

pr_command() {
    local branch=$1 title=$2 body=$3
    printf 'gh pr create --repo %s --base main --head %s:%s \\\n' "$SLUG" "$(owner)" "$branch"
    printf "  --title '%s' --body-file - <<'EOF'\n" "$title"
    printf '%s\n' "$body"
    printf 'EOF\n'
}

# notes prints the body of CHANGELOG section "## [$1]" at revision $2.
notes() {
    git show "$2:CHANGELOG.md" | awk -v h="## [$1]" '$0 == h { in_section = 1; next } in_section && /^## \[/ { in_section = 0; done = 1 } in_section && !done' |
        awk 'NF { started = 1 } started' | awk '{ lines[NR] = $0 } NF { last = NR } END { for (i = 1; i <= last; i++) print lines[i] }'
}

release_command() {
    printf "gh release create %s --repo %s --verify-tag --title '%s' --notes-file - <<'EOF'\n" "$1" "$SLUG" "$1"
    notes "${1#v}" "$1"
    printf 'EOF\n'
}

# check_main_pins warns when a nested module on upstream/main requires a root
# version the go command cannot resolve, as when a pinned commit was squashed away.
check_main_pins() {
    local v err
    for v in $(for m in $(nested_requiring "$UPSTREAM"); do root_at "$UPSTREAM" "$m"; done | sort -u); do
        err=$(cd / && GOWORK=off go list -m "$ROOT@$v" 2>&1 >/dev/null) && continue
        warn "Warning: nested modules on $UPSTREAM require $ROOT@$v, which does not resolve:"
        warn "  ${err##*: }"
        warn "  They cannot build until a pin PR points them at a commit on main. The next release's pin step does that."
        printf '\n'
    done
}

plan() {
    fetch_upstream
    require_tag release
    local tag=$RELEASE_TAG version=${RELEASE_TAG#v} prev current=1 prep='' pin='' pv=''
    prev=$(latest_tag)
    local prep_branch=release-$tag-prepare pin_branch=release-$tag-pin
    local prep_title="chore(release): prepare $tag"
    local pin_title="chore(release): pin nested modules to the $tag prepare commit"

    if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
        current=6
        prev=$(git tag --list "$MAJOR.*" --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | grep -A1 -xF "$tag" | sed -n 2p)
    fi
    check_main_pins
    prep=$(prepare_commit "$version")
    if [ "$current" = 6 ]; then
        pin=$(git rev-parse "$tag^{commit}")
        pv=$(root_at "$pin" "$(nested_requiring "$pin" | head -n 1)")
    elif [ -n "$prep" ]; then
        current=3
        pin=$(pin_commit "$prep")
        if [ -n "$pin" ]; then
            current=5
            pv=$(root_at "$pin" "$(nested_requiring "$pin" | head -n 1)")
        elif [[ "$(awk -v m="$ROOT" '$1 == m { print $2 } $1 == "require" && $2 == m { print $3 }' "$(nested_requiring "$UPSTREAM" | head -n 1)/go.mod")" == *-"${prep:0:12}" ]]; then
            current=4
        fi
    elif grep -qxF "const Client = \"$version\"" internal/version/version.go; then
        current=2
    fi

    mark() { if [ "$1" = "$current" ]; then printf '\033[1m=> '; else printf '   '; fi; }
    done_note() { if [ "$1" -lt "$current" ]; then printf ' \033[32m(done)'; fi; printf '\033[0m\n'; }

    printf '\nRelease %s of %s (previous tag %s). Each step lists the commands to run.\n\n' "$tag" "$ROOT" "${prev:-none}"

    mark 1; printf 'Step 1: stage the release notes and version on a branch off %s' "$UPSTREAM"; done_note 1
    printf '\033[2m  release-prepare edits only CHANGELOG.md and internal/version/version.go; then write the [%s] notes.\033[0m\n' "$version"
    printf '  git fetch %s\n  git switch -c %s %s\n' "$UPSTREAM_REMOTE" "$prep_branch" "$UPSTREAM"
    printf '  make release-prepare release-tag=%s\n' "$tag"
    printf '  $EDITOR CHANGELOG.md\n\n'

    mark 2; printf 'Step 2: commit, push and open the prepare PR, then squash-merge it'; done_note 2
    if [ "$current" = 2 ] && notes_empty "$version" <CHANGELOG.md && [ -z "$ALLOW_EMPTY" ]; then
        warn "  Blocked: CHANGELOG.md has no entries under [$version]. Add them, or re-run with allow-empty=1 to release without notes."
    fi
    printf '  git add CHANGELOG.md internal/version/version.go\n'
    printf "  git commit -s -m '%s'\n" "$prep_title"
    printf '  git push -u %s %s\n' "$RELEASE_REMOTE" "$prep_branch"
    pr_command "$prep_branch" "$prep_title" "$(
        printf 'Prepare %s: move the `Unreleased` CHANGELOG entries to `[%s]` and set `internal/version/version.go` to `%s`.\n\n' "$tag" "$version" "$version"
        printf 'Squash-merge. The pin PR that follows makes the nested modules require the root at this PR'"'"'s squash commit, so no other PR that changes the root module should merge until it does.\n\n'
        apache_text
    )"
    printf '  Merge with "Squash and merge".\n\n'

    mark 3; printf 'Step 3: point the nested modules at the prepare PR'"'"'s squash commit%s' "${prep:+ ($(git rev-parse --short "$prep"))}"; done_note 3
    printf '\033[2m  release-pin asks go list -m for that commit'"'"'s version, then runs go mod edit -require, go mod tidy,\n  go build and go vet in each nested module, echoing each command.\033[0m\n'
    printf '  git fetch %s\n  git switch -c %s %s\n' "$UPSTREAM_REMOTE" "$pin_branch" "$UPSTREAM"
    printf '  make release-pin release-tag=%s\n\n' "$tag"

    mark 4; printf 'Step 4: commit, push and open the pin PR, then squash-merge it'; done_note 4
    printf '  git add'; for m in $(nested_requiring "$UPSTREAM"); do printf ' %s/go.mod %s/go.sum' "$m" "$m"; done; printf '\n'
    printf "  git commit -s -m '%s'\n" "$pin_title"
    printf '  git push -u %s %s\n' "$RELEASE_REMOTE" "$pin_branch"
    pr_command "$pin_branch" "$pin_title" "$(
        printf 'Make every nested module require the root at the squash commit of the %s prepare PR, so the nested modules build against the root module content `%s` will tag.\n\n' "$tag" "$tag"
        printf 'Squash-merge, then run `make release-verify release-tag=%s`.\n\n' "$tag"
        apache_text
    )"
    printf '  Merge with "Squash and merge".\n\n'

    mark 5; printf 'Step 5: verify the pin commit%s and request the signed tag' "${pin:+ ($(git rev-parse --short "$pin"), $pv)}"; done_note 5
    printf '\033[2m  release-verify only reads; when every check passes it prints the GitHub Request form link and the text for each field.\033[0m\n'
    printf '  make release-verify release-tag=%s\n\n' "$tag"

    mark 6; printf 'Step 6: once %s exists, publish the GitHub release from its CHANGELOG section' "$tag"; done_note 6
    if [ "$current" = 6 ]; then
        printf '\033[2m  %s is tagged at %s. The notes below are its CHANGELOG section; publishing triggers the pkg.go.dev refresh.\033[0m\n' "$tag" "$(git rev-parse --short "$pin")"
        release_command "$tag"
    else
        printf '\033[2m  Once the tag exists, make release prints the gh release create command with the CHANGELOG notes.\033[0m\n'
    fi
    printf '  git branch -D %s %s\n  git push %s --delete %s %s\n\n' "$prep_branch" "$pin_branch" "$RELEASE_REMOTE" "$prep_branch" "$pin_branch"
}

prepare() {
    fetch_upstream
    require_tag release-prepare
    local tag=$RELEASE_TAG version=${RELEASE_TAG#v} prev prev_prev
    prev=$(latest_tag)
    prev_prev=$(git tag --list "$MAJOR.*" --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sed -n 2p)
    [ -n "$prev" ] || die "no $MAJOR.*.* tag found in $UPSTREAM_REMOTE"
    git rev-parse -q --verify "refs/tags/$tag" >/dev/null && die "tag $tag already exists"
    { [ "$tag" != "$prev" ] && [ "$(printf '%s\n%s\n' "$tag" "$prev" | sort -t. -k1.2,1n -k2,2n -k3,3n | tail -n 1)" = "$tag" ]; } ||
        die "$tag is not newer than the latest tag $prev"
    [ -z "$(prepare_commit "$version")" ] || die "$UPSTREAM already sets version.go to $version; the prepare PR has merged, so run make release release-tag=$tag for the next step"
    require_clean "release-$tag-prepare"
    if grep -qxF "const Client = \"$version\"" internal/version/version.go; then
        die "internal/version/version.go is already $version"
    fi

    step "Moving the Unreleased CHANGELOG entries to [$version], previous tag $prev"
    trace env GOWORK=off go run .github/changelog.go -file CHANGELOG.md -tag "$tag" -prev "$prev" ${prev_prev:+-prev-prev "$prev_prev"}
    step "Setting internal/version/version.go to $version"
    sed -e "s/^const Client = \".*\"\$/const Client = \"$version\"/" internal/version/version.go >internal/version/version.go.new
    grep -q "^const Client = \"$version\"\$" internal/version/version.go.new || { rm -f internal/version/version.go.new; die "could not set the version in internal/version/version.go"; }
    mv internal/version/version.go.new internal/version/version.go

    printf '\n'
    git --no-pager diff --stat
    printf '\n'
    if notes_empty "$version" <CHANGELOG.md; then
        warn "Unreleased had no entries, so [$version] in CHANGELOG.md is empty. Write the release notes there before committing."
    fi
    printf 'Review the diff (git diff), edit the [%s] notes in CHANGELOG.md, then run make release release-tag=%s for the commit, push and PR commands.\n' "$version" "$tag"
}

pin() {
    fetch_upstream
    require_tag release-pin
    local tag=$RELEASE_TAG version=${RELEASE_TAG#v} prep pv mod
    git rev-parse -q --verify "refs/tags/$tag" >/dev/null && die "tag $tag already exists"
    prep=$(prepare_commit "$version")
    [ -n "$prep" ] || die "no commit on $UPSTREAM sets version.go to $version; merge the prepare PR first (make release release-tag=$tag)"
    [ -z "$(pin_commit "$prep")" ] || die "$UPSTREAM already pins the nested modules to $(git rev-parse --short "$prep"); run make release-verify release-tag=$tag"
    step "Prepare commit: $(git show -s --format='%h %s' "$prep")"
    if ! root_same "$prep" "$UPSTREAM"; then
        git --no-pager diff --stat "$prep" "$UPSTREAM" -- "${ROOT_SPEC[@]}" >&2
        die "the root module on $UPSTREAM has changed since the prepare commit, so $tag would not match what the nested modules pin. Release the next version instead."
    fi
    step "Root module on $UPSTREAM matches the prepare commit"
    require_clean "release-$tag-pin"

    # Name the prepare commit outside any module, so the version the nested
    # modules require now (which may not resolve) is never loaded.
    step "Asking the go command for the prepare commit's version"
    pv=$(cd / && trace env GOWORK=off go list -m -f '{{.Version}}' "$ROOT@$prep")
    [[ "$pv" == *-"${prep:0:12}" ]] || die "go list -m $ROOT@$prep gave $pv, not a pseudo-version of $prep"
    printf '   = %s\n' "$pv"

    for mod in $(nested_requiring HEAD); do
        step "$mod ($(root_at HEAD "$mod") -> $pv)"
        (
            cd "$mod"
            export GOWORK=off
            trace go mod edit -require="$ROOT@$pv"
            trace go mod tidy
            trace go build ./...
            trace go vet ./...
        )
    done

    printf '\n'
    git --no-pager diff --stat
    printf '\nThe nested modules now require %s@%s.\n' "$ROOT" "$pv"
    printf 'Review the diff (git diff), then run make release release-tag=%s for the commit, push and PR commands.\n' "$tag"
}

verify() {
    fetch_upstream
    require_tag release-verify
    local tag=$RELEASE_TAG version=${RELEASE_TAG#v} prep pin mod v pv='' canon tmp tagged=''
    git rev-parse -q --verify "refs/tags/$tag" >/dev/null && tagged=1
    prep=$(prepare_commit "$version")
    [ -n "$prep" ] || die "no commit on $UPSTREAM sets version.go to $version"
    pin=$(pin_commit "$prep")
    [ -n "$pin" ] || die "no commit on $UPSTREAM pins the nested modules to $(git rev-parse --short "$prep"); merge the pin PR first"
    step "Prepare commit: $(git show -s --format='%h %s' "$prep")"
    step "Pin commit:     $(git show -s --format='%h %s' "$pin")"
    if [ -n "$tagged" ]; then
        step "Tag:            $tag -> $(git rev-parse --short "$tag^{commit}") ($(git for-each-ref --format='%(taggername)' "refs/tags/$tag"))"
    fi

    for mod in $(nested_requiring "$pin"); do
        v=$(root_at "$pin" "$mod")
        [ -z "$pv" ] || [ "$v" = "$pv" ] || die "at $pin, $mod requires $ROOT@$v but others require $pv"
        pv=$v
    done
    [[ "$pv" == *-"${prep:0:12}" ]] || die "at $pin the nested modules require $ROOT@$pv, not a pseudo-version of $prep"
    printf '   ok  every nested module requires %s@%s\n' "$ROOT" "$pv"

    root_same "$prep" "$pin" || die "the root module differs between $prep and $pin, so $tag on $pin would not match $pv"
    printf '   ok  root module content at %s equals %s\n' "$(git rev-parse --short "$pin")" "$(git rev-parse --short "$prep")"

    git show "$pin:internal/version/version.go" | grep -qxF "const Client = \"$version\"" || die "version.go at $pin is not $version"
    printf '   ok  version.go at %s is %s\n' "$(git rev-parse --short "$pin")" "$version"

    canon=$(cd / && GOWORK=off go list -m "$ROOT@$prep")
    [ "$canon" = "$ROOT $pv" ] || die "go list -m $ROOT@$prep gives ${canon#* }, not $pv"
    printf '   ok  go list -m %s@%s gives %s\n' "$ROOT" "${prep:0:12}" "$pv"

    TMP=$(mktemp -d)
    trap 'git worktree remove --force "$TMP/pin" 2>/dev/null; rm -rf "$TMP"' EXIT
    tmp=$TMP
    git worktree add --quiet --detach "$tmp/pin" "$pin"
    for mod in $(nested_requiring "$pin"); do
        (cd "$tmp/pin/$mod" && GOWORK=off go mod tidy -diff && GOWORK=off go build ./... && GOWORK=off go vet ./...) >"$tmp/$mod.log" 2>&1 ||
            { cat "$tmp/$mod.log" >&2; die "$mod does not tidy, build and vet at $pin"; }
        printf '   ok  %s: go mod tidy -diff, go build, go vet\n' "$mod"
    done

    if [ -n "$tagged" ]; then
        [ "$(git rev-parse "$tag^{commit}")" = "$pin" ] ||
            die "$tag points at $(git rev-parse --short "$tag^{commit}"), not the pin commit $(git rev-parse --short "$pin")"
        printf '   ok  %s points at the pin commit\n' "$tag"
        [ "$(git cat-file -t "$tag")" = tag ] && git cat-file -p "$tag" | grep -q 'BEGIN PGP SIGNATURE' ||
            die "$tag is not a signed annotated tag"
        printf '   ok  %s is a signed annotated tag\n' "$tag"
        local origin
        origin=$(cd / && GOWORK=off go list -m -f '{{with .Origin}}{{.Hash}}{{end}}' "$ROOT@$tag")
        [ "$origin" = "$pin" ] || die "the module proxy serves $ROOT@$tag from ${origin:-an unknown commit}, not $pin"
        printf '   ok  the module proxy serves %s@%s from %s\n' "$ROOT" "$tag" "$(git rev-parse --short "$pin")"
        printf '\n\033[1mAll checks passed. %s is released correctly.\033[0m\n\n' "$tag"
        printf 'Publish the GitHub release, if it is not already:\n\n'
        release_command "$tag"
        printf '\n'
        return
    fi

    # changes is the prepare PR's diff page, or the prepare commit's when its
    # subject names no PR.
    local changes pr
    pr=$(git show -s --format=%s "$prep" | sed -nE 's/.*\(#([0-9]+)\)$/\1/p')
    if [ -n "$pr" ]; then
        changes=https://github.com/$SLUG/pull/$pr/changes
    else
        changes=https://github.com/$SLUG/commit/$prep
    fi

    printf '\n\033[1mAll checks passed. Tag %s on %s.\033[0m\n\n' "$tag" "$pin"
    # The GitHub Request form applies the github-request label the admin
    # dashboards filter on. gh issue create --label cannot: GitHub silently
    # drops labels on a new issue from anyone without push access to the repo.
    # The form's fields have no id, so only the title can be prefilled.
    local title title_uri
    title="[GitHub Request] Tag new opensearch-go $tag release"
    title_uri=$(printf '%s' "$title" | sed 's/ /%20/g; s/\[/%5B/g; s/\]/%5D/g') # the title holds no other URL-reserved characters
    printf 'File the signed-tag request with the GitHub Request form, then paste each field below into it:\n\n'
    printf '  https://github.com/opensearch-project/.github/issues/new?template=GITHUB_REQUEST_TEMPLATE.yaml&title=%s\n\n' "$title_uri"
    printf '\033[2m--- What is the type of request?\033[0m\n'
    printf 'Repository Management\n\n'
    printf '\033[2m--- Details of the request\033[0m\n'
    printf 'Please create a signed `%s` tag for `opensearch-go` from `%s` on the `main` branch.\n\n' "$tag" "$pin"
    printf -- '- repository: https://github.com/%s\n- target branch: `main`\n- module path: `%s`\n\n' "$SLUG" "$ROOT"
    printf '```\ngit tag -s %s %s -m "Tag %s"\n```\n\n' "$tag" "$pin" "$tag"
    printf '\033[2m--- Additional information to support your request\033[0m\n'
    printf -- '- version file: %s#diff-%s\n' "$changes" "$(diff_anchor internal/version/version.go)"
    printf -- '- CHANGELOG: %s#diff-%s\n' "$changes" "$(diff_anchor CHANGELOG.md)"
    printf -- '- Pin commit: https://github.com/%s/commit/%s\n\n' "$SLUG" "$pin"
    printf '\033[2m--- When does this request need to be completed?\033[0m\n'
    printf 'ASAP\n\n'
}

case "${1:-}" in
plan) plan ;;
prepare) prepare ;;
pin) pin ;;
verify) verify ;;
*) die "usage: $0 plan|prepare|pin|verify" ;;
esac
