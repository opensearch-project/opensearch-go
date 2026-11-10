#!/usr/bin/env bash

# See the LICENSE file in the project root for more information.

# Check that source code files in this repo have the appropriate license
# header.

if [ "$TRACE" != "" ]; then
    export PS4='${BASH_SOURCE}:${LINENO}: ${FUNCNAME[0]:+${FUNCNAME[0]}(): }'
    set -o xtrace
fi
set -o errexit
set -o pipefail

TOP=$(cd "$(dirname "$0")/.." >/dev/null && pwd)
NLINES=$(wc -l .github/license-header.txt | awk '{print $1}')

function check_license_header {
    local f
    f=$1
    if ! diff .github/license-header.txt <(head -$NLINES "$f") >/dev/null; then
        echo "check-license-headers: error: '$f' does not have required license header, see 'diff -u .github/license-header.txt <(head -$NLINES $f)'"
        return 1
    else
        return 0
    fi
}


cd "$TOP"
nErrors=0
# Code under a third_party directory is a copy of another project's, under that
# project's license, which its own LICENSE file states.
for f in $(git ls-files | grep '\.go$' | grep -v '/third_party/'); do
    if ! check_license_header $f; then
        nErrors=$((nErrors+1))
    fi
done

# A module zip holds only the files under its go.mod, so each nested module
# carries its own copy of the root LICENSE.txt. Without it pkg.go.dev detects no
# license and does not display the module's documentation.
for mod in $(git ls-files '*/go.mod'); do
    dir=$(dirname "$mod")
    if ! cmp -s LICENSE.txt "$dir/LICENSE.txt"; then
        echo "check-license-headers: error: '$dir/LICENSE.txt' is missing or differs from LICENSE.txt, see 'cp LICENSE.txt $dir/LICENSE.txt'"
        nErrors=$((nErrors+1))
    fi
done

if [[ $nErrors -eq 0 ]]; then
    exit 0
else
    exit 1
fi