#!/bin/bash
# Build the gd fork's toolchain using host Go + host GOROOT.
#
# Full mode runs src/make.bash with -stop=go_bootstrap. Two phases from dist:
#   1. bootstrapBuildTools: host Go compiles fork cmd/{asm,cgo,compile,link}
#      against host GOROOT via a bootstrap workspace with rewritten imports
#      (bootstrap/cmd/..., bootstrap/internal/...). Produces toolchain1.
#   2. go_bootstrap: toolchain1 compiles fork runtime + cmd/go against FORK
#      stdlib. Produces pkg/tool/linux_amd64/go_bootstrap, which dist then
#      copies to bin/go (our -stop=go_bootstrap tweak).
# We stop here to avoid toolchain2/3 and the final `install std cmd` phase,
# which exercise toolchain1 on more of fork stdlib and currently miscompile
# (e.g. crypto/internal/fips140/alias: "doTyp returned nil for info={11 false}").
#
# Partial mode (./rebuild-tools.sh compile ...) is an escape hatch for fast
# iteration: host Go + GOROOT=$GOFORK builds the named tool(s) only. Uses
# fork GOROOT so internal/* changes must stay source-compatible with host Go.
#
# Usage:
#   ./rebuild-tools.sh              # full: make.bash -stop=go_bootstrap
#   ./rebuild-tools.sh compile      # partial: rebuild just cmd/compile
#   ./rebuild-tools.sh compile link # partial: rebuild a subset

set -euo pipefail

GOFORK=/home/quentin/git/go
SYSGO=/usr/lib/go
SYSGOBIN=$SYSGO/bin/go
TOOLDIR=$GOFORK/pkg/tool/linux_amd64
CACHE=/tmp/cache-rebuild-tools
ZBOOTSTRAP=$GOFORK/src/internal/buildcfg/zbootstrap.go

die() {
    echo "rebuild-tools: FATAL: $*" >&2
    exit 1
}

trap 'die "aborted on line $LINENO (command: ${BASH_COMMAND})"' ERR

[ -x "$SYSGOBIN" ] || die "host Go not found at $SYSGOBIN"
[ -d "$GOFORK/src/cmd" ] || die "fork source tree missing at $GOFORK/src/cmd"

sys_ver=go1.26.1
our_ver=$(head -1 $GOFORK/VERSION)

# Previously we force-aligned VERSION/zbootstrap.go to the host Go version
# ("to keep object headers agreeing at link time"). Host Go only compiles
# the fork toolchain binaries (cmd/compile etc.) during bootstrapBuildTools;
# it never produces stdlib .a files, so there is no cross-linker mismatch
# to worry about. Keeping VERSION distinct ("gd1.26.1") lets -V=full carry
# a gd-prefix marker that cmd/internal/objabi/flag.go uses to emit
# buildID=<contentID>, which is how cmd/go/internal/work/buildid.go keys
# the build cache so rebuilds invalidate stale artifacts.

# ---- full mode: make.bash -stop=go_bootstrap ----
if [ $# -eq 0 ]; then
    echo "Full toolchain1 + go_bootstrap build via make.bash (host Go: $sys_ver)"
    echo "  GOROOT_BOOTSTRAP=$SYSGO"
    echo

    # make.bash -a is implicit in dist bootstrap; it cleans pkg/obj and
    # rebuilds the bootstrap workspace from scratch. We pass -stop=go_bootstrap
    # through to cmd/dist so it exits after toolchain1 + cmd/go are built.
    (
        cd $GOFORK/src
        GOROOT_BOOTSTRAP=$SYSGO \
        GOTOOLCHAIN=local \
        ./make.bash -stop=go_bootstrap
    )

    # Verify toolchain1 tools + go_bootstrap (copied to bin/go) are present.
    # bootstrapBuildTools only builds these four tools; preprofile/vet/cover/fix
    # are seeded from host below so tooling drivers still find them.
    for t in compile link asm cgo; do
        [ -x "$TOOLDIR/$t" ] || die "expected $TOOLDIR/$t after make.bash, not found"
    done
    [ -x "$GOFORK/bin/go" ] || die "expected $GOFORK/bin/go after make.bash, not found"
    for seed in preprofile vet cover fix; do
        src=$SYSGO/pkg/tool/linux_amd64/$seed
        [ -x "$src" ] && cp "$src" "$TOOLDIR/$seed"
    done

    # Wipe the user's go-build cache: freshly-installed tools invalidate any
    # previously-cached .a files.
    $GOFORK/bin/go clean -cache 2>/dev/null || true

    echo
    echo "Versions:"
    for t in compile link asm; do
        timeout 3 $TOOLDIR/$t -V=full 2>&1 | sed 's/^/  /' | head -1
    done
    timeout 3 $GOFORK/bin/go version 2>&1 | sed 's/^/  /'

    echo
    echo "Test with:"
    echo "  cd $GOFORK && ./bin/go test -short sort"
    exit 0
fi

# ---- partial mode: in-place host-Go build of requested tools only ----
TOOLS=("$@")

# Re-seed tool dir with stable host tools before each fork tool build, so
# `go build` runs on a known-good toolchain (not a half-built fork tool
# from the previous iteration).
#
# `local` is critical: seed_tools is called from inside a `for t in ...`
# loop that drives the actual builds. Without `local`, the inner loop
# variable leaks out and every build runs against the last-seeded name.
seed_tools() {
    local seed
    for seed in compile link asm cgo preprofile vet cover fix; do
        local src=$SYSGO/pkg/tool/linux_amd64/$seed
        [ -x "$src" ] && cp "$src" "$TOOLDIR/$seed"
    done
}

mkdir -p $TOOLDIR $GOFORK/bin

# Wipe caches: this script's scratch cache fully, and the user's shared cache
# since package .a files are keyed by content hash and can cross-contaminate
# between fork/host builds.
rm -rf $CACHE && mkdir -p $CACHE
if [ -x "$GOFORK/bin/go" ]; then
    $GOFORK/bin/go clean -cache 2>/dev/null || true
fi

# Stale .new files from a prior aborted run mask whether THIS run succeeded.
rm -f $TOOLDIR/*.new

echo "Partial rebuild with host Go ($sys_ver):"
echo "  GOROOT=$GOFORK"
echo "  CACHE=$CACHE"
echo

for t in "${TOOLS[@]}"; do
    [ -d "$GOFORK/src/cmd/$t" ] || die "no such tool: cmd/$t"
    echo "  -> cmd/$t"
    seed_tools
    GOCACHE=$CACHE GOROOT=$GOFORK GOTOOLCHAIN=local $SYSGOBIN build \
        -o $TOOLDIR/$t.new ./src/cmd/$t
    [ -x "$TOOLDIR/$t.new" ] || die "cmd/$t build produced no binary"
done

# Atomically install: re-seed with stable host tools, then drop freshly-built
# on top. Any tool we DIDN'T rebuild ends up as the stable host version.
seed_tools
for t in "${TOOLS[@]}"; do
    mv $TOOLDIR/$t.new $TOOLDIR/$t
    [ -x "$TOOLDIR/$t" ] || die "install mv succeeded but $TOOLDIR/$t not executable"
done

stray=$(ls $TOOLDIR/*.new 2>/dev/null || true)
[ -z "$stray" ] || die "leftover .new files after install: $stray"

[ -x "$GOFORK/bin/go" ] && $GOFORK/bin/go clean -cache 2>/dev/null || true

echo
echo "Versions:"
for t in compile link asm; do
    [ -x "$TOOLDIR/$t" ] && timeout 3 $TOOLDIR/$t -V=full 2>&1 | sed 's/^/  /' | head -1
done

echo
echo "Installed:"
for t in "${TOOLS[@]}"; do
    echo "  $TOOLDIR/$t ($(stat -c '%y' $TOOLDIR/$t | cut -d. -f1))"
done
