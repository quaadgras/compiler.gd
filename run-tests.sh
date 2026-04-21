#!/bin/bash
# Run tests from the Go repo with the gd fork's toolchain.
#
# Uses ./bin/go (built by rebuild-tools.sh) to exercise fat-iface codegen
# against test suites. Packages run one at a time so a single fail doesn't
# mask the rest; each package gets a timeout so a hung test can't stall
# the whole run.
#
# Usage:
#   ./run-tests.sh                   # default "hot" set (fast-ish iteration)
#   ./run-tests.sh fast              # very small set: reflect + sync + strings
#   ./run-tests.sh std               # every stdlib package
#   ./run-tests.sh cmd               # every cmd package (from cmd module)
#   ./run-tests.sh testdir           # compiler $GOROOT/test suite
#   ./run-tests.sh all               # std + cmd + testdir
#   ./run-tests.sh pkg1 pkg2 ...     # specific packages
#
# Exit code: 0 if every package passed, 1 if any failed.

set -u

GOFORK=/home/quentin/git/go
GO=$GOFORK/bin/go
LOG=/tmp/gd-tests.log
PKG_TIMEOUT=120s

die() { echo "run-tests: $*" >&2; exit 1; }

[ -x "$GO" ] || die "./bin/go missing; run ./rebuild-tools.sh first"

# Hot set: packages most likely to exercise fat-iface (interface conversions,
# reflection, atomic broadcasts, encoders/decoders).
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

# The cmd module is separate from the std module, so `go list cmd/...` from
# GOROOT resolves against the cmd module (which contains cmd/go, cmd/compile,
# etc.). testdir lives in cmd/internal/testdir and runs every .go file under
# $GOROOT/test as a compiler test.

case "${1:-}" in
    "")         PKGS=("${HOT_PKGS[@]}") ;;
    fast)       PKGS=("${FAST_PKGS[@]}") ;;
    std)        mapfile -t PKGS < <($GO list std) ;;
    cmd)        mapfile -t PKGS < <($GO list cmd/...) ;;
    testdir)    PKGS=(cmd/internal/testdir) ;;
    all)
        mapfile -t STD  < <($GO list std)
        mapfile -t CMD  < <($GO list cmd/...)
        PKGS=("${STD[@]}" "${CMD[@]}" cmd/internal/testdir)
        ;;
    *)          PKGS=("$@") ;;
esac

echo "Running ${#PKGS[@]} package(s) with $GO (timeout $PKG_TIMEOUT per pkg)"
echo "Full log: $LOG"
echo

: > "$LOG"

passed=0
failed=0
failed_pkgs=()

for pkg in "${PKGS[@]}"; do
    # `go test -count=1` defeats the test cache so we actually re-run.
    # `-short` skips long-running tests (our focus is correctness, not throughput).
    # `-vet=off` because asmvet still uses 16-byte interface layout and rejects
    # runtime's fat-iface-sized debugCallPanicked ($...-32). Tracked separately.
    if $GO test -compiler=gd -vet=off -timeout "$PKG_TIMEOUT" -short -count=1 "$pkg" >>"$LOG" 2>&1; then
        printf "  %-40s PASS\n" "$pkg"
        passed=$((passed + 1))
    else
        printf "  %-40s FAIL\n" "$pkg"
        failed=$((failed + 1))
        failed_pkgs+=("$pkg")
    fi
done

echo
echo "Passed: $passed"
echo "Failed: $failed"

if [ $failed -gt 0 ]; then
    echo
    echo "Failed packages:"
    for p in "${failed_pkgs[@]}"; do
        echo "  $p"
    done
    echo
    echo "Inspect failures in $LOG (search for 'FAIL' or '--- FAIL')."
    exit 1
fi
