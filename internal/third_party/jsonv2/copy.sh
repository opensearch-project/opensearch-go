#!/usr/bin/env bash

# Copies encoding/json/v2 and the encoding/json code that runs encoding/json's
# API on it from a Go toolchain's standard library into this directory, so the
# client runs the encoding/json/v2 engine on toolchains that do not build it.
# Run from anywhere, with the toolchain to copy from on PATH:
#
#   GOTOOLCHAIN=go1.27.3 internal/third_party/jsonv2/copy.sh
#
# It replaces what is here, and changes the copied files in two ways only: it
# removes each file's goexperiment.jsonv2 build constraint, and it rewrites the
# encoding/json/v2, encoding/json/jsontext and encoding/json/internal import
# paths to this directory's. Tests are not copied: several import
# internal/testenv, which only the standard library can import.

set -o errexit
set -o nounset
set -o pipefail

DST=$(cd "$(dirname "$0")" >/dev/null && pwd)
SRC="$(go env GOROOT)/src/encoding/json"
MOD=github.com/opensearch-project/opensearch-go/v5/internal/third_party/jsonv2
VERSION=$(go env GOVERSION)

copy() {
    local from=$1 to=$2 f
    rm -rf "${DST:?}/$to"
    mkdir -p "$DST/$to"
    for f in "$SRC/$from"/*.go; do
        case $f in
        *_test.go) continue ;;
        esac
        cp "$f" "$DST/$to/"
    done
}

copy v2 json
copy jsontext jsontext
copy internal internal
copy internal/jsonflags internal/jsonflags
copy internal/jsonopts internal/jsonopts
copy internal/jsonwire internal/jsonwire

# encoding/json's API on the v2 engine is the package's v2_*.go files.
rm -rf "${DST:?}/v1"
mkdir -p "$DST/v1"
for f in "$SRC"/v2_*.go; do
    case $f in
    *_test.go) continue ;;
    esac
    cp "$f" "$DST/v1/${f##*/v2_}"
done

LICENSE="$(go env GOROOT)/LICENSE"
if [ ! -f "$LICENSE" ]; then
    # Homebrew keeps the LICENSE beside GOROOT rather than in it.
    LICENSE="$(go env GOROOT)/../LICENSE"
fi
cp "$LICENSE" "$DST/LICENSE"

for f in $(cd "$DST" && ls json/*.go jsontext/*.go internal/*.go internal/*/*.go v1/*.go); do
    # Only import specs are rewritten: a line holding nothing but an optional
    # name and the quoted path. Doc comments that name a package keep its name.
    perl -0pi \
        -e 's{^//go:build goexperiment\.jsonv2\n\n}{}m;' \
        -e 's{^(\s*(?:\w+\s+)?)"encoding/json/v2"$}{$1"'"$MOD"'/json"}mg;' \
        -e 's{^(\s*(?:\w+\s+)?)"encoding/json/jsontext"$}{$1"'"$MOD"'/jsontext"}mg;' \
        -e 's{^(\s*(?:\w+\s+)?)"encoding/json/internal(/\w+)?"$}{$1"'"$MOD"'/internal$2"}mg;' \
        "$DST/$f"
done

echo "$VERSION" >"$DST/VERSION"
echo "copied encoding/json/v2 from $VERSION into $DST"
