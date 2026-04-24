#!/usr/bin/env bash
# Run all Phase G.2.1 boundary tests.
#
# Each test prints "ok" on success. Anything else counts as failure.
# Runs with the fork toolchain (GOROOT=$PWD uses the compile tool
# built by rebuild-tools.sh).
set -eu
cd "$(dirname "$0")/../../.."

fails=0
total=0
for t in test/gd/phaseg/*.go; do
    name=$(basename "$t" .go)
    total=$((total + 1))
    out=$(GOROOT="$PWD" GOTOOLCHAIN=local /usr/lib/go/bin/go run "$t" 2>&1)
    if [ "$out" = "ok" ]; then
        echo "PASS $name"
    else
        fails=$((fails + 1))
        echo "FAIL $name:"
        echo "$out" | sed 's/^/    /'
    fi
done

if [ "$fails" -eq 0 ]; then
    echo
    echo "All $total Phase G tests passed."
else
    echo
    echo "$fails of $total Phase G tests failed."
    exit 1
fi
