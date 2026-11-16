#!/usr/bin/env python3
"""run.py [-j N] [pattern...]: run the "// run" tests of $GOROOT/test with GOWASM=gc.

A test is built with the compiler and the linker of this tree, against the
runtime in src/runtime/wasmgc and the packages listed in STD (which are
compiled once), and run with node. It passes if it exits normally and prints
what its .out file says, if it has one. Tests that import a package that is
not in STD are skipped.

The results go to status.txt: one line per test, and a summary of the reasons
for the failures.

With GOWASIRUNTIME=chromium (or firefox) in the environment the tests run in
that browser (lib/wasm/wasm_gc_browser_exec.mjs), and the results go to
status-chromium.txt.
"""
import collections, concurrent.futures, os, re, subprocess, sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), '../std'))
import build as stdbuild

HERE = os.path.dirname(os.path.abspath(__file__))
GOROOT = os.path.abspath(os.path.join(HERE, '../../../../..'))
OUT = os.path.join(HERE, 'out')
ENV = stdbuild.ENV
HOSTENV = dict(os.environ, GOROOT=GOROOT, GOWASM='')
for k in ('GOOS', 'GOARCH'):
    HOSTENV.pop(k, None)
GO = os.path.join(GOROOT, 'bin/go')
TOOL = os.path.join(GOROOT, 'pkg/tool',
                    subprocess.check_output([GO, 'env', 'GOHOSTOS'], env=HOSTENV, text=True).strip() + '_' +
                    subprocess.check_output([GO, 'env', 'GOHOSTARCH'], env=HOSTENV, text=True).strip())
RUNNER = os.path.join(HERE, '../compiled/run.mjs')
BROWSER = os.environ.get('GOWASIRUNTIME', '')
if BROWSER not in ('chromium', 'firefox'):
    BROWSER = ''
STATUS = os.path.join(HERE, 'status-%s.txt' % BROWSER if BROWSER else 'status.txt')

# The packages of the standard library that tests may import: those
# that build are importable.
STD = ['fmt', 'os', 'strings', 'strconv', 'reflect', 'sort', 'math', 'time', 'sync', 'errors', 'bytes',
       'unicode', 'unicode/utf8', 'unicode/utf16', 'sync/atomic', 'io', 'bufio', 'flag', 'log', 'math/rand',
       'math/rand/v2', 'container/heap', 'container/list', 'container/ring', 'slices', 'maps', 'iter', 'cmp',
       'math/bits', 'math/big', 'math/cmplx', 'encoding/binary', 'hash/crc32', 'regexp', 'runtime/debug',
       'io/ioutil', 'path/filepath', 'os/exec', 'os/signal', 'go/ast', 'go/parser', 'go/token', 'text/template',
       'encoding/json', 'internal/race', 'internal/abi', 'internal/goarch', 'internal/testenv', 'testing',
       'unique', 'weak', 'syscall', 'runtime/pprof', 'context', 'crypto/sha256', 'hash/fnv', 'hash/maphash',
       'strings', 'embed', 'internal/asan', 'internal/msan', 'internal/godebug', 'math/rand/v2', 'structs',
       'crypto/md5', 'encoding/xml']
IMPORTABLE = {'runtime', 'unsafe'}
DIRS = ['.', 'fixedbugs', 'ken', 'chan', 'interface', 'typeparam']


def sh(args, **kw):
    return subprocess.run(args, env=ENV, capture_output=True, text=True, errors='replace', **kw)


def build_std():
    cfg, built = stdbuild.build(STD, keep=True, log=lambda *a: None)
    if 'runtime' not in built:
        stdbuild.build(['runtime'], keep=True)
        sys.exit('the runtime does not build')
    IMPORTABLE.update(built)
    return cfg


# The build tags of GOOS=wasip1 GOARCH=wasm with the gc toolchain.
TAGS = {'wasip1', 'wasm', 'gc', 'wasm.gc'} | {'go1.%d' % i for i in range(1, 30)}


def build_ok(expr):
    """Reports whether a //go:build expression is satisfied."""
    py = re.sub(r'[\w.]+', lambda m: str(m.group(0) in TAGS), expr)
    py = py.replace('&&', ' and ').replace('||', ' or ').replace('!', ' not ')
    return eval(py)


def tests(patterns):
    for d in DIRS:
        full = os.path.join(GOROOT, 'test', d)
        for f in sorted(os.listdir(full)):
            if not f.endswith('.go'):
                continue
            name = os.path.normpath(os.path.join(d, f))
            if patterns and not any(p in name for p in patterns):
                continue
            src = open(os.path.join(full, f), encoding='utf8', errors='replace').read()
            first = src.split('\n', 1)[0].strip()
            if first not in ('// run', '//run'):
                continue
            m = re.search(r'^//go:build (.*)$', src.split('\npackage ', 1)[0], re.M)
            if m and not build_ok(m.group(1)):
                continue
            head = src.split('\nfunc ', 1)[0]
            # (Only import declarations: a struct tag is a string too.)
            imports = set(re.findall(r'^import\s+(?:[\w.]+\s+)?"([\w/.]+)"', head, re.M))
            for block in re.findall(r'^import\s*\((.*?)^\)', head, re.M | re.S):
                imports |= set(re.findall(r'"([\w/.]+)"', block))
            yield name, imports


def reason(text):
    """A short reason for a failure, from what the tools or the program printed."""
    m = re.search(r'RuntimeError: ([^\n]*)\n\s*at (\S+)', text)
    if m:
        return re.sub(r'\[\d+\]|\d+', 'N', m.group(1).strip() + ' in ' + m.group(2))
    for pat in (r'GOWASM=gc: \S+ traps: not supported yet: ([^(\n]*)',
                r'call of (\S+), which is not defined',
                r'relocation target (\S+) not defined',
                r'internal compiler error: ([^\n]{0,80})',
                r'(fatal error: [^\n]*)',
                r'(panic: [^\n]{0,60})',
                r'RuntimeError: ([^\n]*)',
                r'CompileError: .*?failed: ([^@\n]{0,70})',
                r'(undefined: \S+)'):
        m = re.search(pat, text)
        if m:
            return re.sub(r'\[\d+\]|\d+', 'N', m.group(1).strip())
    return (text.strip().split('\n') or ['?'])[0][:80]


# Tests that check finalizers run: runtime.SetFinalizer does nothing on
# GOWASM=gc (a finalizer needs its object, which the host only reports
# once it is gone; runtime.AddCleanup works).
FINALIZERS = {
    'deferfin.go', 'tinyfin.go', 'stackobj.go', 'stackobj3.go',
    'fixedbugs/issue27518b.go', 'fixedbugs/issue32477.go',
    'fixedbugs/issue46725.go', 'fixedbugs/issue5493.go', 'fixedbugs/issue54343.go',
    'fixedbugs/issue24491a.go', 'fixedbugs/issue24491b.go', 'fixedbugs/issue45045.go',
    'fixedbugs/issue57823.go', 'mallocfin.go', 'finprofiled.go',
}


# Tests of what GOWASM=gc does not have: an address for an object. A
# uintptr made from a pointer to one is its identity, which only a
# conversion the compiler sees (unsafe.Pointer(v.Pointer())) undoes, and
# no pointer is past the end of an address space.
NOADDRESS = {
    'fixedbugs/issue15329.go',  # a uintptr from a method value converted back
    'unsafebuiltins.go',        # unsafe.Slice of the last byte of the address space
}


# The heap is the engine's: there is no profile of its allocations.
NOPROFILE = {'heapsampling.go'}


def run(cfg, name, imports):
    if name in NOPROFILE:
        return name, 'SKIP', 'no heap profile'
    if name in FINALIZERS:
        return name, 'SKIP', 'finalizers'
    if name in NOADDRESS:
        return name, 'SKIP', 'objects have no address'
    missing = imports - IMPORTABLE
    if missing:
        return name, 'SKIP', 'imports ' + ' '.join(sorted(missing))
    src = os.path.join(GOROOT, 'test', name)
    base = os.path.join(OUT, name.replace('/', '_')[:-3])
    r = sh([TOOL + '/compile', '-dwarf=false', '-p', 'main', '-importcfg', cfg, '-o', base + '.a', src])
    notes = r.stderr
    if r.returncode:
        return name, 'FAIL', 'compile: ' + reason(r.stderr)
    r = sh([TOOL + '/link', '-checklinkname=0', '-importcfg', cfg + '.link', '-o', base + '.wasm', base + '.a'])
    if r.returncode:
        return name, 'FAIL', 'link: ' + reason(r.stderr + r.stdout)
    try:
        # (Output compared as Go's test/run.go does: stdout and stderr together.)
        cmd = ['node', '--disable-warning=ExperimentalWarning', '--stack-size=4000', RUNNER, base + '.wasm']
        env = ENV
        if BROWSER:
            cmd = [os.path.join(GOROOT, 'lib/wasm/go_wasip1_wasm_exec'), base + '.wasm']
            env = dict(ENV, GOWASM='gc', GOWASIRUNTIME=BROWSER)
        r = subprocess.run(cmd, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, errors='replace', timeout=60)
    except subprocess.TimeoutExpired:
        return name, 'FAIL', 'run: timeout'
    finally:
        for ext in ('.a', '.wasm'):
            try:
                os.remove(base + ext)
            except OSError:
                pass
    if r.returncode:
        why = reason(r.stdout)
        if 'unreachable' in why and 'not supported yet' in notes:
            why = 'unsupported: ' + reason(notes)
        return name, 'FAIL', 'run: ' + why
    want = src[:-3] + '.out'
    if os.path.exists(want) and open(want, errors='replace').read() != r.stdout:
        return name, 'FAIL', 'run: wrong output'
    return name, 'PASS', ''


def main():
    args = sys.argv[1:]
    jobs = os.cpu_count() or 4
    if args[:1] == ['-j']:
        jobs, args = int(args[1]), args[2:]
    cfg = build_std()
    results = []
    with concurrent.futures.ThreadPoolExecutor(jobs) as ex:
        for res in ex.map(lambda t: run(cfg, *t), tests(args)):
            results.append(res)
            if args:
                print(*res)
    count = collections.Counter(r[1] for r in results)
    why = collections.Counter(r[2] for r in results if r[1] == 'FAIL')
    summary = ['%d pass, %d fail, %d skipped (of %d "// run" tests)' % (count['PASS'], count['FAIL'], count['SKIP'], len(results)), '']
    summary += ['%5d  %s' % (n, w) for w, n in why.most_common()]
    print('\n'.join(summary[:40]))
    if not args:
        with open(STATUS, 'w') as f:
            f.write('\n'.join(summary) + '\n\n')
            for name, status, w in sorted(results):
                f.write('%s %s %s\n' % (status, name, w))


if __name__ == '__main__':
    main()
