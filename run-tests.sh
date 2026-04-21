#!/bin/bash
# Run stdlib/cmd tests against the gd fork.
#
# Workflow change: rebuild-tools.sh no longer produces bin/go (it would be
# fork-compiled-by-fork, double-stacking compiler bug onto runtime bug).
# Instead we use SYSTEM go (host runtime, stable) with GOROOT pointing at
# the fork; system go invokes the fork toolchain in $GOROOT/pkg/tool/... to
# compile the test binary, which then runs on FORK runtime. Crashes in test
# binaries reflect real fork bugs.
#
# `-run='^Test'` filters out fuzz harness and example targets — fuzz workers
# spawn subprocesses that are easy to leave hanging while we still have
# rough edges.
#
# Each package gets a timeout so a hung test can't stall the whole run.
#
# Usage:
#   ./run-tests.sh                   # default "hot" set (fast-ish iteration)
#   ./run-tests.sh fast              # very small set: reflect + sync + strings + sort + fmt
#   ./run-tests.sh std               # every stdlib package
#   ./run-tests.sh cmd               # every cmd package
#   ./run-tests.sh pkg1 pkg2 ...     # specific packages
#
# Exit code: 0 if every package passed, 1 if any failed.

set -u

GOFORK=/home/quentin/git/go
SYSGO=/usr/lib/go/bin/go
GOCACHE=${GOCACHE:-/tmp/gd-test-cache}
LOG=/tmp/gd-tests.log
PKG_TIMEOUT=60s

die() { echo "run-tests: $*" >&2; exit 1; }

[ -x "$SYSGO" ] || die "host Go not found at $SYSGO"
[ -x "$GOFORK/pkg/tool/linux_amd64/compile" ] || die "fork toolchain missing; run ./rebuild-tools.sh"

# Build environment for system go to drive the fork toolchain.
export GOROOT="$GOFORK"
export GOTOOLCHAIN=local
export GOCACHE

# Hot set: packages most likely to exercise the gd-fork ABI changes
# (interface conversions, reflection, atomic broadcasts, encoders/decoders,
# string layout).
HOT_PKGS=(
    reflect
    runtime
    sync
    sync/atomic
    strings
    strconv
    bytes
    sort
    slices
    maps
    fmt
    errors
    encoding/json
    encoding/gob
    encoding/binary
    encoding/base64
    encoding/hex
    io
    bufio
    hash/crc32
    hash/fnv
    container/heap
    container/list
    container/ring
    math
    math/big
    math/rand
    math/rand/v2
    time
    unicode
    unicode/utf8
    unicode/utf16
    context
    flag
    log
    regexp
    text/template
    html/template
)

FAST_PKGS=(reflect sync strings sort fmt)

case "${1:-}" in
    "")         PKGS=("${HOT_PKGS[@]}") ;;
    fast)       PKGS=("${FAST_PKGS[@]}") ;;
    std)        mapfile -t PKGS < <($SYSGO list std) ;;
    cmd)        mapfile -t PKGS < <($SYSGO list cmd/...) ;;
    *)          PKGS=("$@") ;;
esac

echo "Running ${#PKGS[@]} package(s) via $SYSGO + GOROOT=$GOFORK (timeout $PKG_TIMEOUT per pkg)"
echo "Full log: $LOG"
echo

: > "$LOG"

passed=0
failed=0
timedout=0
failed_pkgs=()

for pkg in "${PKGS[@]}"; do
    # `go test -count=1`     defeat the test cache so we re-run.
    # `-short`               skip long-running tests; we care about correctness.
    # `-run='^Test'`         exclude fuzz harness (Fuzz*) and examples; fuzz
    #                        workers spawn subprocesses that hang easily while
    #                        the toolchain still has rough edges.
    # `-vet=off`             rebuild-tools.sh now ships a gd-aware vet, but
    #                        running vet inline still adds noise. The "go vet
    #                        -asmdecl ./..." sweep belongs in a separate run.
    # Per-package skip list for tests with separate, non-Phase-A bugs.
    # Keep this small and document each entry — every skip is a known issue
    # we should resolve later.
    skip=""
    case "$pkg" in
        runtime)
            # TestAtomicAlignment — go/types Importer.Default loops on
            # our fork's export data; not string-layout related.
            skip='TestAtomicAlignment'
            ;;
    esac
    {
        echo
        echo "==== $pkg ===="
        if [ -n "$skip" ]; then
            $SYSGO test -timeout "$PKG_TIMEOUT" -short -count=1 -run='^Test' -skip="$skip" -vet=off "$pkg"
        else
            $SYSGO test -timeout "$PKG_TIMEOUT" -short -count=1 -run='^Test' -vet=off "$pkg"
        fi
    } >>"$LOG" 2>&1
    rc=$?
    case $rc in
        0)   printf "  %-40s PASS\n" "$pkg"; passed=$((passed + 1)) ;;
        124) printf "  %-40s TIMEOUT\n" "$pkg"; timedout=$((timedout + 1)); failed_pkgs+=("$pkg (timeout)") ;;
        *)   printf "  %-40s FAIL (rc=$rc)\n" "$pkg"; failed=$((failed + 1)); failed_pkgs+=("$pkg") ;;
    esac
done

echo
echo "Passed:   $passed"
echo "Failed:   $failed"
echo "Timeouts: $timedout"

if [ $failed -gt 0 ] || [ $timedout -gt 0 ]; then
    echo
    echo "Problem packages:"
    for p in "${failed_pkgs[@]}"; do
        echo "  $p"
    done
    echo
    echo "Inspect $LOG (search for 'FAIL', '--- FAIL', or 'TIMEOUT')."
    exit 1
fi
