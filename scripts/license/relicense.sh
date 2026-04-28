#!/bin/bash
# Relicense / add license headers across the gd fork tree.
#
# Three categories of files, three handlings:
#
#   1. fork-only      — files added by the fork (no upstream Go origin).
#                       Get the full fork-license header from fork.tpl.
#   2. modified-upstream — files originally from upstream Go (BSD-3-Clause)
#                       that this fork has since modified. The BSD header
#                       MUST be retained per the BSD-3-Clause notice
#                       requirement; we APPEND a fork-modification notice
#                       (modified.tpl) immediately after it.
#   3. unmodified-upstream — left alone.
#
# Categorisation comes from `git log` against a baseline ref (default:
# the merge-base with origin/master). Every file added by us since the
# baseline is fork-only; every file modified by us is modified-upstream;
# everything else is unmodified-upstream.
#
# Usage:
#
#   scripts/license/relicense.sh              # apply headers (writes)
#   scripts/license/relicense.sh --check      # CI mode: exit non-zero if missing
#   scripts/license/relicense.sh --baseline upstream/master  # custom baseline
#   scripts/license/relicense.sh --holder "Quentin Quaadgras" --year 2026
#
# Requires: addlicense (`go install github.com/google/addlicense@latest`),
# git, sed, grep.

set -uo pipefail

cd "$(dirname "$0")/../.."   # repo root
ROOT=$(pwd)

# --- defaults -----------------------------------------------------------
HOLDER="Quentin Quaadgras"
YEAR=$(date +%Y)
BASELINE=""             # auto-detected below if empty
CHECK_ONLY=0
VERBOSE=0
INCLUDE_GLOB="src/**/*.go src/**/*.s src/**/*.c src/**/*.h"
EXCLUDE_GLOB="**/testdata/** **/_*/** **/zdefaultcc.go **/zversion.go **/zzipdata.go **/stdlib.tar.gz"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --check)    CHECK_ONLY=1; shift ;;
        --baseline) BASELINE="$2"; shift 2 ;;
        --holder)   HOLDER="$2"; shift 2 ;;
        --year)     YEAR="$2"; shift 2 ;;
        -v|--verbose) VERBOSE=1; shift ;;
        -h|--help)
            sed -n '2,/^$/p' "$0" | sed 's/^# //; s/^#//'
            exit 0 ;;
        *)
            echo "unknown flag: $1" >&2
            exit 2 ;;
    esac
done

if [[ -z "$BASELINE" ]]; then
    # Try common upstream remotes/branches in order.
    for cand in "origin/master" "upstream/master" "upstream/main" "origin/main"; do
        if git rev-parse --verify "$cand" >/dev/null 2>&1; then
            BASELINE=$(git merge-base HEAD "$cand")
            break
        fi
    done
    if [[ -z "$BASELINE" ]]; then
        echo "relicense: no upstream remote found; pass --baseline <ref> explicitly" >&2
        exit 2
    fi
fi

[[ $VERBOSE -eq 1 ]] && echo "baseline: $BASELINE"

# --- categorize ---------------------------------------------------------
# Fork-only: files added (status A) since baseline that still exist.
# Modified-upstream: files that exist at baseline AND have been modified
# (status M) or had their mode/content changed (R, etc.) since.
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

git diff --name-status --diff-filter=A "$BASELINE"..HEAD -- 'src/**' \
    | awk '{print $2}' \
    | grep -E '\.(go|s|c|h)$' \
    | grep -v -E '/(testdata|_[a-z]+|tests/_[^/]+)/' \
    > "$TMP/fork-only.txt" || true

git diff --name-status --diff-filter=M "$BASELINE"..HEAD -- 'src/**' \
    | awk '{print $2}' \
    | grep -E '\.(go|s|c|h)$' \
    | grep -v -E '/(testdata|_[a-z]+|tests/_[^/]+)/' \
    > "$TMP/modified-upstream.txt" || true

# Filter to extant files only — git history may show a path that's
# since been moved/deleted.
xargs -a "$TMP/fork-only.txt" -d'\n' -I{} sh -c '[[ -f "{}" ]] && echo "{}"' > "$TMP/fork-only-extant.txt"
xargs -a "$TMP/modified-upstream.txt" -d'\n' -I{} sh -c '[[ -f "{}" ]] && echo "{}"' > "$TMP/modified-upstream-extant.txt"

n_fork=$(wc -l < "$TMP/fork-only-extant.txt")
n_modified=$(wc -l < "$TMP/modified-upstream-extant.txt")
echo "found: fork-only=$n_fork modified-upstream=$n_modified"

# --- helpers ------------------------------------------------------------

apply_fork_header() {
    local file="$1"
    if has_fork_header "$file"; then
        return 0
    fi
    if [[ $CHECK_ONLY -eq 1 ]]; then
        echo "MISSING fork header: $file"
        return 1
    fi
    addlicense -c "$HOLDER" -f scripts/license/fork.tpl -y "$YEAR" "$file"
}

# True if the file already carries our fork-only header.
has_fork_header() {
    local file="$1"
    head -10 "$file" | grep -q "Copyright $YEAR $HOLDER"
}

# True if the file already carries our fork-modification suffix block.
has_fork_modnotice() {
    local file="$1"
    head -30 "$file" | grep -q "Modifications copyright $YEAR $HOLDER"
}

# True if the file has the upstream BSD header at the top (used to
# locate the insertion point for the modification notice).
has_bsd_header() {
    local file="$1"
    head -5 "$file" | grep -q "The Go Authors. All rights reserved"
}

# Append the modification notice immediately after the BSD header.
# The BSD header runs from the first "Copyright" line through the
# "license that can be found in the LICENSE file." line; we insert
# the contents of modified.tpl right after that line.
apply_modified_notice() {
    local file="$1"
    if has_fork_modnotice "$file"; then
        return 0
    fi
    if ! has_bsd_header "$file"; then
        # No BSD header to anchor to. Treat as fork-only — a file we
        # added but git classifies as "modified" (rare; usually a
        # rename across the baseline). Fall through to fork header.
        apply_fork_header "$file"
        return $?
    fi
    if [[ $CHECK_ONLY -eq 1 ]]; then
        echo "MISSING fork mod notice: $file"
        return 1
    fi
    # Render modified.tpl with substituted Year/Holder.
    local tpl
    tpl=$(sed -e "s/{{.Year}}/$YEAR/g" -e "s/{{.Holder}}/$HOLDER/g" \
              scripts/license/modified.tpl)
    # Insert after the BSD anchor line. -i runs in-place.
    awk -v block="$tpl" '
        /license that can be found in the LICENSE file\./ && !inserted {
            print
            print block
            inserted = 1
            next
        }
        { print }
    ' "$file" > "$file.tmp" && mv "$file.tmp" "$file"
}

# --- run ----------------------------------------------------------------
fail=0

while IFS= read -r file; do
    [[ -z "$file" ]] && continue
    apply_fork_header "$file" || fail=1
done < "$TMP/fork-only-extant.txt"

while IFS= read -r file; do
    [[ -z "$file" ]] && continue
    apply_modified_notice "$file" || fail=1
done < "$TMP/modified-upstream-extant.txt"

if [[ $CHECK_ONLY -eq 1 && $fail -ne 0 ]]; then
    echo "relicense --check FAILED: some files missing fork license headers" >&2
    exit 1
fi
if [[ $CHECK_ONLY -eq 1 ]]; then
    echo "relicense --check OK"
fi
