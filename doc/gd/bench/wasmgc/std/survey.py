#!/usr/bin/env python3
"""Run `go test -short` with GOWASM=gc over the standard library.

    survey.py [-j N] [-t SECONDS] [pkg...]

With no packages it runs all of `go list std` but cmd/...; with packages
it runs those and updates their lines in status.txt (next to this
script), keeping the others. Each line is `pkg ok`, `pkg FAIL reason`,
`pkg BUILD reason` or `pkg SKIP reason`. Logs go to $SURVEY_LOGS
(default /tmp/wasmgc-survey). GOROOT, PATH (with $GOROOT/lib/wasm) and
GOCACHE are taken from the environment.

With GOWASIRUNTIME=chromium (or firefox) the tests run in that browser
(lib/wasm/wasm_gc_browser_exec.mjs) and the file is status-chromium.txt.

With GOWASM=gc,split in the environment the tests are built for that model
(stage 15 of wasm-gc.md) and the file is status-split.txt
(status-split-chromium.txt in a browser); the logs go to $SURVEY_LOGS-split.
"""

import argparse, os, re, signal, subprocess, sys, time
from concurrent.futures import ThreadPoolExecutor

HERE = os.path.dirname(os.path.abspath(__file__))
BROWSER = os.environ.get("GOWASIRUNTIME", "")
if BROWSER not in ("chromium", "firefox"):
    BROWSER = ""
SPLIT = "split" in os.environ.get("GOWASM", "").split(",")
GOWASM = "gc,split" if SPLIT else "gc"
STATUS = os.path.join(HERE, "status" + ("-split" if SPLIT else "") + ("-" + BROWSER if BROWSER else "") + ".txt")
LOGS = os.environ.get("SURVEY_LOGS", "/tmp/wasmgc-survey") + ("-split" if SPLIT else "")

# Lines in status.txt that are not package results start with one of these.
def is_result(line):
    return bool(re.match(r"^[^#\s]\S* (ok|FAIL|BUILD|SKIP)\b", line))

def classify(pkg, out, rc, timedout):
    if timedout:
        return "FAIL hard timeout (killed)"
    if re.search(r"^ok\s+" + re.escape(pkg) + r"\b", out, re.M) and rc == 0:
        return "ok"
    if re.search(r"no test files", out) and rc == 0:
        return "SKIP no test files"
    if rc == 0 and re.search(r"\[no tests to run\]", out):
        return "SKIP no tests to run"
    if "[build failed]" in out or "[setup failed]" in out or re.search(r"^# ", out, re.M):
        for l in out.splitlines():
            if l.startswith("#") or "build failed" in l or not l.strip():
                continue
            return "BUILD " + l.strip()[:200]
        return "BUILD ?"
    lines = out.splitlines()
    for pat in (r"^panic: ", r"^fatal error: ", r"test timed out", r"^RuntimeError", r"^wasm trap", r"Check failed", r"^\s*--- FAIL: "):
        for i, l in enumerate(lines):
            if re.search(pat, l):
                s = l.strip()
                if pat == r"^\s*--- FAIL: ":
                    names = [re.sub(r"\s*\(.*", "", x.strip()[len("--- FAIL: "):]) for x in lines if x.startswith("--- FAIL: ")]
                    s = "--- FAIL: " + " ".join(names[:6]) + (" ..." if len(names) > 6 else "")
                    # Add the first message under the first failing test.
                    for m in lines[i + 1:i + 4]:
                        m = m.strip()
                        if m and not m.startswith("---") and not m.startswith("==="):
                            s += " | " + m
                            break
                return "FAIL " + s[:300]
    for l in reversed(lines):
        if l.strip() and not l.startswith("FAIL") and not l.startswith("exit status"):
            return "FAIL " + l.strip()[:200]
    return "FAIL rc=%d" % rc

def run(pkg, timeout, gotimeout):
    env = dict(os.environ, GOOS="wasip1", GOARCH="wasm", GOWASM=GOWASM, NODE_OPTIONS="")
    cmd = ["go", "test", "-short", "-timeout", gotimeout, pkg]
    t0 = time.time()
    p = subprocess.Popen(cmd, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                         start_new_session=True, cwd=os.path.join(os.environ["GOROOT"], "src"))
    timedout = False
    try:
        out, _ = p.communicate(timeout=timeout)
    except subprocess.TimeoutExpired:
        timedout = True
        os.killpg(p.pid, signal.SIGKILL)
        out, _ = p.communicate()
    try:
        os.killpg(p.pid, signal.SIGKILL)  # stray node processes
    except ProcessLookupError:
        pass
    out = out.decode("utf-8", "replace")
    os.makedirs(LOGS, exist_ok=True)
    with open(os.path.join(LOGS, pkg.replace("/", "_") + ".log"), "w") as f:
        f.write(out)
    res = classify(pkg, out, p.returncode, timedout)
    print("%-40s %5.0fs %s" % (pkg, time.time() - t0, res), flush=True)
    return pkg, res

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("-j", type=int, default=8)
    ap.add_argument("-t", type=int, default=240, help="hard timeout per package")
    ap.add_argument("-gotimeout", default="120s")
    ap.add_argument("-n", action="store_true", help="do not write status.txt")
    ap.add_argument("pkgs", nargs="*")
    a = ap.parse_args()
    pkgs = a.pkgs
    if not pkgs:
        out = subprocess.check_output(["go", "list", "std"], env=dict(os.environ, GOOS="wasip1", GOARCH="wasm", GOWASM=GOWASM)).decode()
        pkgs = [p for p in out.split() if not p.startswith("cmd/")]
    old = {}
    header, footer = [], []
    if os.path.exists(STATUS):
        section = header
        for l in open(STATUS).read().splitlines():
            if is_result(l):
                p, r = l.split(" ", 1)
                old[p] = r
                section = footer
            else:
                section.append(l)
    with ThreadPoolExecutor(a.j) as ex:
        res = dict(ex.map(lambda p: run(p, a.t, a.gotimeout), pkgs))
    # A SKIP recorded by hand (a reason other than no test files) is kept.
    for p, r in res.items():
        if p in old and old[p].startswith("SKIP") and not r.startswith("SKIP") and "no test files" not in old[p]:
            res[p] = old[p]
    old.update(res)
    counts = {}
    for r in old.values():
        k = r.split(" ", 1)[0]
        counts[k] = counts.get(k, 0) + 1
    print(" ".join("%s %d" % kv for kv in sorted(counts.items())))
    if a.n:
        return
    with open(STATUS, "w") as f:
        for l in header:
            f.write(l + "\n")
        for p in sorted(old):
            f.write("%s %s\n" % (p, old[p]))
        for l in footer:
            f.write(l + "\n")

main()
