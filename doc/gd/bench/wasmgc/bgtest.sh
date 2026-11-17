#!/bin/bash
# bgtest.sh [suite...]: run the slow suites of GOWASM=gc on a copy of
# this tree, so that this one can be worked on meanwhile. Run it in the
# background:
#
#	doc/gd/bench/wasmgc/bgtest.sh conformance std &
#
# The suites: conformance (the tests of the language, under node), std
# (go test over the standard library), conformance-chromium and
# std-chromium (the same in a browser; -firefox for the other). The default is the first two.
#
# The copy is a git worktree ($GOTESTTREE, by default ../go-pkgtests
# next to this tree: make it once with git worktree add), set to this
# tree as it is now: its commit and what is changed in it (a new file
# only if it is staged: git add), or to the commit $GOTESTREV. It
# builds its own compiler and
# linker, with its own caches. The model is $GOTESTWASM (by default gc;
# gc,split for the other). The results go to $GOTESTOUT (by default
# /tmp/go-bgtest): a file for each suite (the status of every test) and
# summary.txt, one line for each suite; nothing of this tree is written.
set -e
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../../../.." && pwd)
tree=${GOTESTTREE:-$(dirname "$root")/go-pkgtests}
out=${GOTESTOUT:-/tmp/go-bgtest}
suites=${*:-conformance std}
wasm=${GOTESTWASM:-gc}
split=
case $wasm in *split*) split=-split ;; esac
mkdir -p "$out"

# One run at a time: they share the tree.
exec 9>"$out/lock"
if ! flock -n 9; then
	echo "bgtest: another run is using $tree" >&2
	exit 1
fi

cd "$root"
if [ -n "$GOTESTREV" ]; then
	snap=$(git rev-parse "$GOTESTREV") # a commit, not this tree as it is
else
	snap=$(git stash create)
	[ -n "$snap" ] || snap=$(git rev-parse HEAD)
fi
cd "$tree"
git checkout -q -f --detach "$snap"
: >"$out/summary.txt"
if [ -n "$GOTESTREV" ]; then
	echo "commit $(git rev-parse --short "$snap"), GOWASM=$wasm, $(date +%H:%M)" >>"$out/summary.txt"
else
	echo "tree $(git -C "$root" rev-parse --short HEAD) + $(git -C "$root" status --short | grep -vc '^??') files changed, $(date +%H:%M)" >>"$out/summary.txt"
fi

unset GOOS GOARCH GOWASM GOFLAGS GOWASIRUNTIME
export GOROOT=$tree NODE_OPTIONS= GOCACHE=$out/gocache-host
if ! "$tree/bin/go" install cmd/compile cmd/asm cmd/link cmd/go >"$out/build.log" 2>&1; then
	echo "build FAILED: see $out/build.log" >>"$out/summary.txt"
	exit 1
fi
export GOCACHE=$out/gocache GOOS=wasip1 GOARCH=wasm GOWASM=$wasm PATH=$tree/bin:$tree/lib/wasm:$PATH
bench=$tree/doc/gd/bench/wasmgc
mkdir -p "$bench/conformance/out$split"
jobs=${GOTESTJOBS:-8}

for suite in $suites; do
	browser=
	case $suite in *-chromium) browser=chromium ;; *-firefox) browser=firefox ;; esac
	case $suite in
	conformance*)
		GOWASIRUNTIME=$browser nice python3 "$bench/conformance/run.py" -j "$jobs" >"$out/$suite.log" 2>&1 || true
		cp "$bench/conformance/status$split${browser:+-$browser}.txt" "$out/$suite.txt"
		echo "$suite: $(head -1 "$out/$suite.txt")" >>"$out/summary.txt"
		grep '^FAIL' "$out/$suite.txt" | head -20 | cut -c1-200 | sed 's/^/  /' >>"$out/summary.txt"
		;;
	std*)
		GOWASIRUNTIME=$browser SURVEY_LOGS=$out/$suite-logs nice python3 "$bench/std/survey.py" -n -j "$jobs" >"$out/$suite.txt" 2>&1 || true
		echo "$suite: $(tail -1 "$out/$suite.txt")" >>"$out/summary.txt"
		grep 'FAIL\|BUILD' "$out/$suite.txt" | grep -v '^FAIL [0-9]' | head -30 | cut -c1-200 | sed 's/^/  /' >>"$out/summary.txt"
		;;
	*)
		echo "$suite: no such suite" >>"$out/summary.txt"
		;;
	esac
done
echo "done $(date +%H:%M)" >>"$out/summary.txt"
cat "$out/summary.txt"
