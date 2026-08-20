#!/bin/bash
# Build the gd fork's toolchain using host Go via bootstrap workspace.
#
# What this produces
# ------------------
# Just the fork toolchain binaries: cmd/{compile,link,asm,cgo}, installed to
# $TOOLDIR (pkg/tool/linux_amd64). These binaries are HOST-RUNTIME-LINKED:
# they were compiled by host Go against a bootstrap-rewritten copy of the
# fork source that uses host stdlib. They run on host runtime — not fork
# runtime — so any ABI bugs in fork stdlib cannot crash the tools.
#
# The tools emit code for FORK stdlib and FORK runtime. To actually test
# fork changes, use:
#
#   cd /home/quentin/git/go && GOROOT=$PWD GOTOOLCHAIN=local /usr/lib/go/bin/go test -count=1 ./some/package
#
# System go (host-runtime-linked) invokes the fork tools to compile test
# binaries against fork stdlib; the test binaries run on fork runtime.
# Crashes in test binaries reflect real fork bugs. We never run the
# rebuilt fork tools *against their own fork runtime* — that would
# double-stack "compiler bug" on "runtime bug" and make debugging awful.
#
# What this does NOT produce
# --------------------------
# No go_bootstrap / bin/go. make.bash's phase 2 (toolchain1 building
# go_bootstrap against fork stdlib) intentionally skipped via
# -stop=toolchain1. Debugging a fork-compiled cmd/go binary is exactly
# the trap this script exists to avoid.
#
# Host Go tools (preprofile/vet/cover/fix) are seeded into $TOOLDIR from
# the host toolchain so cmd/go tooling drivers find them.
#
# Usage
# -----
#   ./rebuild-tools.sh          # build toolchain1 (compile/link/asm/cgo)

set -euo pipefail

GOFORK=/home/quentin/git/go
SYSGO=/usr/lib/go
SYSGOBIN=$SYSGO/bin/go
TOOLDIR=$GOFORK/pkg/tool/linux_amd64

die() {
    echo "rebuild-tools: FATAL: $*" >&2
    exit 1
}

trap 'die "aborted on line $LINENO (command: ${BASH_COMMAND})"' ERR

[ -x "$SYSGOBIN" ] || die "host Go not found at $SYSGOBIN"
[ -d "$GOFORK/src/cmd" ] || die "fork source tree missing at $GOFORK/src/cmd"

sys_ver=go1.26.1

echo "Building fork toolchain1 via bootstrap workspace (host Go: $sys_ver)"
echo "  GOROOT_BOOTSTRAP=$SYSGO"
echo

# make.bash -stop=toolchain1:
#   bootstrapBuildTools only — host Go compiles fork cmd/{compile,link,asm,
#   cgo} against a bootstrap workspace with rewritten imports. The bootstrap
#   workspace's stdlib-style imports resolve to HOST Go's stdlib, so the
#   produced tools don't link fork runtime. Exits before the go_bootstrap
#   (phase 2) + toolchain2/3 + "install std cmd" stages that exercise fork
#   stdlib at build time.
(
    cd $GOFORK/src
    GOROOT_BOOTSTRAP=$SYSGO \
    GOTOOLCHAIN=local \
    ./make.bash -stop=toolchain1
)

for t in compile link asm cgo; do
    [ -x "$TOOLDIR/$t" ] || die "expected $TOOLDIR/$t after make.bash, not found"
done

# Seed driver tools that we don't customize (preprofile/cover/fix) from
# the host toolchain so cmd/go can find them. vet is rebuilt from FORK
# source below because gd's 24-byte string and 32-byte fat-iface change
# go/types size data that asmdecl uses to validate runtime assembly.
for seed in preprofile cover fix; do
    src=$SYSGO/pkg/tool/linux_amd64/$seed
    [ -x "$src" ] && cp "$src" "$TOOLDIR/$seed"
done

# Rebuild vet against fork source so its asmdecl pass sees 24 B strings
# and 32 B fat-iface. Built by host Go (so vet itself runs on host
# runtime), but the SOURCE it uses for type sizes is fork's go/types.
# This makes `go vet` flag any runtime assembly that still hardcodes
# stock 16 B / 16 B layouts.
echo
echo "Rebuilding vet from host source with gd-aware overlay..."
# Build vet from HOST Go's source tree (so vet binary uses host stdlib —
# host runtime, host 16 B strings) but overlay just the three files where
# we changed type-size data: asmdecl (knows about 24 B string + 32 B
# fat-iface components) and go/types size implementations (returns those
# sizes when asmdecl asks). Host's vet binary therefore runs cleanly on
# host runtime AND its asmdecl pass flags any runtime assembly that still
# hardcodes stock layouts.
OVERLAY="${GOTMPDIR:-${TMPDIR:-/tmp}}/gd-vet-overlay.json"
cat > "$OVERLAY" <<EOF
{
  "Replace": {
    "$SYSGO/src/cmd/vendor/golang.org/x/tools/go/analysis/passes/asmdecl/asmdecl.go": "$GOFORK/src/cmd/vendor/golang.org/x/tools/go/analysis/passes/asmdecl/asmdecl.go",
    "$SYSGO/src/go/types/sizes.go":   "$GOFORK/src/go/types/sizes.go",
    "$SYSGO/src/go/types/gcsizes.go": "$GOFORK/src/go/types/gcsizes.go"
  }
}
EOF
( cd "$SYSGO/src/cmd" && unset GOROOT && GOTOOLCHAIN=local $SYSGOBIN build -overlay="$OVERLAY" -o "$TOOLDIR/vet" ./vet )
[ -x "$TOOLDIR/vet" ] || die "vet rebuild failed"

# Wipe any cached fork-compiled .a files: freshly-installed compile
# invalidates whatever the previous iteration produced.
$SYSGOBIN clean -cache 2>/dev/null || true

echo
echo "Installed:"
for t in compile link asm cgo vet; do
    echo "  $TOOLDIR/$t"
done
echo "  $GOFORK/bin/go (shim → $SYSGOBIN with GOROOT=fork)"

echo
echo "Test via host go:"
echo "  cd $GOFORK && GOROOT=\$PWD GOTOOLCHAIN=local $SYSGOBIN test -count=1 -short ./sort"
