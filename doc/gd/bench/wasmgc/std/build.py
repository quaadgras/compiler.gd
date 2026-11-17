#!/usr/bin/env python3
"""build.py [-k] [-o prog.wasm prog.go] [pkg...]: compile with GOWASM=gc.

Compiles the packages named, and those prog.go imports, with their
dependencies; the runtime is src/runtime/wasmgc, and packages that only
the real runtime imports are left out. Archives and importcfg go to
out/. With -o, links prog.go into prog.wasm. With -k, keeps going
after a package fails, reporting every failure.

As a module: build(pkgs, keep) returns the importcfg and the set of
packages that built.
"""
import json, os, subprocess, sys

HERE = os.path.dirname(os.path.abspath(__file__))
GOROOT = os.path.abspath(os.path.join(HERE, '../../../../..'))
# GOWASM=gc,split in the environment builds for that model (stage 15 of
# wasm-gc.md), in out-split; anything else is GOWASM=gc.
SPLIT = 'split' in os.environ.get('GOWASM', '').split(',')
# GOWASM=...,ptr64 builds with words of 8 bytes, as before stage 16 of
# wasm-gc.md; without it words are of 4 (wasm32), and the tool tag
# wasm.ptr32 holds.
PTR64 = 'ptr64' in os.environ.get('GOWASM', '').split(',')
GOWASM = ('gc,split' if SPLIT else 'gc') + (',ptr64' if PTR64 else '')
OUT = os.path.join(HERE, ('out-split' if SPLIT else 'out') + ('-ptr64' if PTR64 else ''))
ENV = dict(os.environ, GOROOT=GOROOT, GOOS='wasip1', GOARCH='wasm', GOWASM=GOWASM, NODE_OPTIONS='', GOWASMGC_NOTES='' if os.environ.get('GOWASMGC_QUIET') else '1')
HOSTENV = dict(os.environ, GOROOT=GOROOT)
for k in ('GOOS', 'GOARCH', 'GOWASM'):
    HOSTENV.pop(k, None)
GO = os.path.join(GOROOT, 'bin/go')
TOOL = os.path.join(GOROOT, 'pkg/tool',
                    subprocess.check_output([GO, 'env', 'GOHOSTOS'], env=HOSTENV, text=True).strip() + '_' +
                    subprocess.check_output([GO, 'env', 'GOHOSTARCH'], env=HOSTENV, text=True).strip())


def golist(*args):
    return subprocess.check_output([GO, 'list', *args], env=ENV, text=True, cwd=GOROOT + '/src')


_lock = None

# The build tags of the runtime's files (see build_ok).
TAGS = {'wasip1', 'wasm', 'gc', 'wasm.gc'} | ({'wasm.split'} if SPLIT else set()) | (set() if PTR64 else {'wasm.ptr32'})


def build_ok(path):
    """Reports whether the //go:build line of the file, if any, is satisfied."""
    import re
    for line in open(path):
        if line.startswith('//go:build '):
            py = re.sub(r'[\w.]+', lambda m: str(m.group(0) in TAGS), line[len('//go:build '):])
            return eval(py.replace('&&', ' and ').replace('||', ' or ').replace('!', ' not '))
        if line.startswith('package '):
            break
    return True


def build(want, keep=True, log=print):
    os.makedirs(OUT, exist_ok=True)
    # One process at a time uses OUT: the lock is held until it exits.
    global _lock
    if _lock is None:
        import fcntl
        _lock = open(os.path.join(OUT, '.lock'), 'w')
        fcntl.flock(_lock, fcntl.LOCK_EX)
    rtImports = [q for q in golist('-f', '{{join .Imports " "}}', './runtime/wasmgc').split() if q != 'unsafe']
    out = golist('-e', '-deps', '-json=ImportPath,Dir,GoFiles,Imports,ImportMap,Error', 'runtime', *rtImports, *want)
    dec = json.JSONDecoder()
    pkgs, i = [], 0
    while i < len(out):
        p, i = dec.raw_decode(out, i)
        pkgs.append(p)
        while i < len(out) and out[i].isspace():
            i += 1
    # The packages needed: the closure of want, where the runtime
    # imports what runtime/wasmgc imports.
    byPath = {p['ImportPath']: p for p in pkgs}
    need, stack = set(), ['runtime', *want]
    while stack:
        q = stack.pop()
        if q in need or q not in byPath:
            continue
        need.add(q)
        stack += rtImports if q == 'runtime' else byPath[q].get('Imports', [])
    cfg = os.path.join(OUT, 'importcfg')
    lines, built, failed = [], set(), set()
    # Vendored packages are imported by another path.
    imaps = sorted({'importmap %s=%s\n' % kv for p in pkgs for kv in (p.get('ImportMap') or {}).items()})
    lines += imaps
    for p in pkgs:
        path = p['ImportPath']
        if path == 'unsafe' or path not in need:
            continue
        if p.get('Error'):
            failed.add(path)
            log('FAIL', path, p['Error'].get('Err', '')[:200])
            continue
        imports = set(p.get('Imports', []))
        files = [os.path.join(p['Dir'], f) for f in p.get('GoFiles', [])]
        flags = []
        if path == 'runtime':
            imports = set(rtImports)
            d = GOROOT + '/src/runtime/wasmgc'
            files = sorted(os.path.join(d, f) for f in os.listdir(d) if f.endswith('.go') and build_ok(os.path.join(d, f)))
            flags = ['-+']
        if imports & failed:
            failed.add(path)
            log('SKIP', path, 'needs', ' '.join(sorted(imports & failed)))
            continue
        a = os.path.join(OUT, path.replace('/', '_') + '.a')
        open(cfg, 'w').write(''.join(lines))
        r = subprocess.run([TOOL + '/compile', '-dwarf=false', '-p', path, '-std', *flags, '-importcfg', cfg, '-o', a, *files],
                           env=ENV, capture_output=True, text=True)
        if r.returncode:
            failed.add(path)
            log('FAIL', path)
            log(r.stderr[:2000])
            if not keep:
                sys.exit(1)
            continue
        lines.append('packagefile %s=%s\n' % (path, a))
        built.add(path)
        notes = r.stderr.count('not supported yet')
        log('ok  ', path, '(%d traps)' % notes if notes else '')
    open(cfg, 'w').write(''.join(lines))
    # (The linker takes no importmap.)
    open(cfg + '.link', 'w').write(''.join(l for l in lines if l.startswith('packagefile ')))
    return cfg, built


def main():
    args = sys.argv[1:]
    keep = prog = None
    if args[:1] == ['-k']:
        keep, args = True, args[1:]
    if args[:1] == ['-o']:
        wasm, prog, args = os.path.abspath(args[1]), os.path.abspath(args[2]), args[3:]
        args += golist('-f', '{{join .Imports " "}}', prog).split()
    cfg, _ = build(args, keep, log=print if not prog else lambda *a: a[0] != 'ok  ' and print(*a))
    if prog:
        a = wasm[:-5] + '.a'
        subprocess.run([TOOL + '/compile', '-dwarf=false', '-p', 'main', '-importcfg', cfg, '-o', a, prog], env=ENV, check=True)
        subprocess.run([TOOL + '/link', '-checklinkname=0', '-importcfg', cfg + '.link', '-o', wasm, a], env=ENV, check=True)


if __name__ == '__main__':
    main()
