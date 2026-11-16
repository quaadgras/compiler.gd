#!/usr/bin/env python3
"""test.py pkg... : run the tests of standard packages with GOWASM=gc.

Builds each package with its _test.go files (and its external tests),
a test main as the go command makes one, links, and runs it with node.
Extra arguments after -- go to the test binary (e.g. -test.run=X).
"""
import os, re, subprocess, sys, json
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build as B

RUNNER = os.path.join(B.HERE, '../compiled/run.mjs')


def funcs(files, pat):
    names = []
    for f in files:
        for m in re.finditer(pat, open(f, encoding='utf8', errors='replace').read(), re.M):
            names.append(m.group(1))
    return names


def compile(pkg, cfg, files, out, std=False):
    r = subprocess.run([B.TOOL + '/compile', '-dwarf=false', '-p', pkg, *(['-std'] if std else []), '-importcfg', cfg, '-o', out, *files],
                       env=B.ENV, capture_output=True, text=True)
    if r.returncode:
        sys.exit('compiling %s:\n%s' % (pkg, r.stderr[-3000:]))
    return r.stderr


def run(pkg, args):
    info = json.loads(B.golist('-json', pkg))
    d = info['Dir']
    gofiles = [os.path.join(d, f) for f in info.get('GoFiles', [])]
    tfiles = [os.path.join(d, f) for f in info.get('TestGoFiles', [])]
    xfiles = [os.path.join(d, f) for f in info.get('XTestGoFiles', [])]
    imports = set(info.get('Imports', [])) | set(info.get('TestImports', [])) | set(info.get('XTestImports', []))
    imports |= {'testing', 'testing/internal/testdeps', 'os'}
    imports.discard(pkg)
    cfg, built = B.build(sorted(imports), keep=True, log=lambda *a: None)
    missing = [p for p in imports if p not in built and p != 'unsafe' and p != 'C']
    if missing:
        return 'SKIP (%s does not build)' % ' '.join(sorted(missing))
    out = os.path.join(B.OUT, 'test_' + pkg.replace('/', '_'))
    os.makedirs(out, exist_ok=True)
    files, other = {}, []
    for l in open(cfg).read().splitlines():
        if not l.startswith('packagefile '):
            other.append(l + '\n')
            continue
        k, _, v = l[len('packagefile '):].partition('=')
        files[k] = v
    pa = os.path.join(out, 'pkg.a')
    tcfg = os.path.join(out, 'importcfg')

    def writecfg():
        open(tcfg, 'w').write(''.join(other) + ''.join('packagefile %s=%s\n' % kv for kv in files.items()))

    writecfg()
    compile(pkg, tcfg, gofiles + tfiles, pa, std=True)
    files[pkg] = pa
    # The packages that import pkg are compiled again, against the
    # variant with the tests, as the go command does.
    out2 = B.golist('-e', '-deps', '-json=ImportPath,Dir,GoFiles,Imports', *sorted(imports))
    dec, i, changed = json.JSONDecoder(), 0, {pkg}
    while i < len(out2):
        p, i = dec.raw_decode(out2, i)
        while i < len(out2) and out2[i].isspace():
            i += 1
        q = p['ImportPath']
        if q in ('runtime', pkg, 'unsafe') or q not in files or not set(p.get('Imports', [])) & changed:
            continue
        writecfg()
        qa = os.path.join(out, 'dep_' + q.replace('/', '_') + '.a')
        compile(q, tcfg, [os.path.join(p['Dir'], f) for f in p.get('GoFiles', [])], qa, std=True)
        files[q] = qa
        changed.add(q)
    writecfg()
    tests = [('_p', n) for n in funcs(tfiles, r'^func (Test\w*)\(\w+ \*testing\.T\)')]
    imp = 'import (\n\t"os"\n\t"testing"\n\t"testing/internal/testdeps"\n'
    if tfiles:
        imp += '\t%s %s\n' % ('_p' if tests else '_', json.dumps(pkg))
    if xfiles:
        xa = os.path.join(out, 'xpkg.a')
        compile(pkg + '_test', tcfg, xfiles, xa)
        open(tcfg, 'a').write('packagefile %s_test=%s\n' % (pkg, xa))
        xtests = [('_x', n) for n in funcs(xfiles, r'^func (Test\w*)\(\w+ \*testing\.T\)')]
        imp += '\t%s %s\n' % ('_x' if xtests else '_', json.dumps(pkg + '_test'))
        tests += xtests
    tests = [(q, n) for q, n in tests if n != 'TestMain']
    main = 'package main\n\n' + imp + ')\n\nvar tests = []testing.InternalTest{\n'
    main += ''.join('\t{%s, %s.%s},\n' % (json.dumps(n), q, n) for q, n in tests)
    main += '}\n\nfunc main() {\n\tm := testing.MainStart(testdeps.TestDeps{}, tests, nil, nil, nil)\n\tos.Exit(m.Run())\n}\n'
    mf = os.path.join(out, 'main.go')
    open(mf, 'w').write(main)
    ma = os.path.join(out, 'main.a')
    compile('main', tcfg, [mf], ma)
    wasm = os.path.join(out, 'test.wasm')
    lcfg = os.path.join(out, 'importcfg.link')  # (the linker takes no importmap)
    open(lcfg, 'w').write(''.join(l for l in open(tcfg) if l.startswith('packagefile ')))
    r = subprocess.run([B.TOOL + '/link', '-checklinkname=0', '-importcfg', lcfg, '-o', wasm, ma], env=B.ENV, capture_output=True, text=True)
    if r.returncode:
        return 'LINK FAIL\n' + r.stderr[-2000:]
    try:
        r = subprocess.run(['node', '--disable-warning=ExperimentalWarning', '--stack-size=4000', RUNNER, wasm, *args],
                           env=dict(B.ENV, NODE_OPTIONS=''), capture_output=True, text=True, timeout=600, cwd=info['Dir'])
    except subprocess.TimeoutExpired:
        return 'TIMEOUT'
    return ('ok' if r.returncode == 0 else 'FAIL') + '\n' + (r.stdout + r.stderr)[-4000:]


def main():
    args = sys.argv[1:]
    extra = []
    if '--' in args:
        i = args.index('--')
        args, extra = args[:i], args[i + 1:]
    for pkg in args:
        res = run(pkg, extra)
        print('==', pkg, res if res.startswith('SKIP') or res.startswith('ok') and not extra else res)


if __name__ == '__main__':
    main()
