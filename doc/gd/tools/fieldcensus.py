import sys, collections, os
FRESH,RESULT,PARAM,SHARED,STATIC,UNKNOWN,LOAD=1,2,4,8,16,32,64
path=sys.argv[1]
pkgs=None
if len(sys.argv)>2 and sys.argv[2]!='-': pkgs=set(open(sys.argv[2]).read().split())
show=sys.argv[3] if len(sys.argv)>3 else 'go/ast.'
dyn=os.environ.get('DYN')=='1'
verbose=os.environ.get('V')=='1'
def nd(d): return d if d[:2] in('p:','r:','a:') else 'f:'+d
inc=collections.defaultdict(list)     # node -> [(sharedroot, deps, pos)]
stores=[]                              # (key, vdeps, cshared, cdeps, pos)
nstores=collections.Counter(); defs=collections.defaultdict(set); esc=set()
for line in open(path, errors='replace'):
    if not line.startswith('fieldcensus'): continue
    p=line.rstrip('\n').split('\t'); kind=p[0]
    if pkgs is not None and p[-1] not in pkgs: continue
    if kind=='fieldcensus':
        key,cat,incat,deps,ops,pos,cdeps=p[1],int(p[2]),int(p[3]),p[4],p[5],p[6],p[7]
        sh=bool(cat&(SHARED|UNKNOWN)) or (dyn and 'dynamic result' in ops)
        dl=[nd(d) for d in deps.split('|') if d]
        cd=[nd(d) for d in cdeps.split('|') if d]
        inc['f:'+key].append((sh,dl,pos))
        stores.append(('f:'+key,dl,bool(incat&(SHARED|UNKNOWN)) or key=='<shared>',cd,pos))
        nstores['f:'+key]+=1
    else:
        key,i,cat,deps,ops=p[1],p[2],int(p[3]),p[4],p[5]
        dl=[nd(d) for d in deps.split('|') if d]
        v=(bool(cat&(SHARED|UNKNOWN)) or (dyn and 'dynamic result' in ops),dl,'')
        if kind=='fieldcensus-arg': inc[f'p:{key}#{i}'].append(v)
        elif kind=='fieldcensus-ret': inc[f'r:{key}#{i}'].append(v)
        elif kind=='fieldcensus-def': defs[ops].add(key)
        elif kind=='fieldcensus-esc': esc.add(key)
nodes=set(inc)
for l in list(inc.values()):
    for v in l: nodes.update(v[1])
for s in stores: nodes.update(s[3])
byfunc=collections.defaultdict(list)
for n in nodes:
    if n[:2] in('p:','r:'):
        k,i=n[2:].rsplit('#',1); byfunc[k].append((n[0],i))
for m,fs in defs.items():
    for f in fs:
        for kind,i in byfunc.get(f,[]):
            if kind=='p': inc[f'p:{f}#{i}'].append((False,[f'p:{m}#{i}'],''))
        if m in esc: esc.add(f)
    for kind,i in byfunc.get(m,[]):
        if kind=='r':
            for f in fs: inc[f'r:{m}#{i}'].append((False,[f'r:{f}#{i}'],''))
nodes|=set(inc)
users=collections.defaultdict(set)
for n,l in inc.items():
    for v in l:
        for d in v[1]: users[d].add(n)
bycont=collections.defaultdict(list)
for i,s in enumerate(stores):
    for d in s[3]: bycont[d].append(i)
P=set(); Fo=set(); why={}; work=[]
def addP(n,w):
    if n not in P: P.add(n); why.setdefault(n,w); work.append(n)
def addF(n,w):
    if n not in Fo:
        Fo.add(n); why[n]=w; work.append(('F',n)); addP(n,w)
def force(i,w):
    for d in stores[i][1]: addF(d,w)
for n,l in inc.items():
    for sh,dl,pos in l:
        if sh: addP(n,pos or 'a shared value')
if dyn:
    for n in nodes:
        if n[:2]=='p:' and n[2:].rsplit('#',1)[0] in esc: addP(n,'callers nobody can name')
for i,s in enumerate(stores):
    if s[2]: force(i,'stored in what is shared at '+s[4])
while work:
    x=work.pop()
    if isinstance(x,tuple):
        n=x[1]
        for sh,dl,pos in inc.get(n,[]):
            for d in dl: addF(d,'flows to '+n)
        continue
    n=x
    for u in users[n]: addP(u,'holds '+n)
    for i in bycont[n]: force(i,f'stored at {stores[i][4]} in what {n} may make shared')
fields=[n for n in inc if n[:2]=='f:' and n!='f:<shared>']
allocs=[n for n in nodes if n[:2]=='a:']
tot=len(fields); ns=sum(nstores[f] for f in fields)
H=[f for f in fields if f not in P]; M=[f for f in fields if f in P and f not in Fo]; Sx=[f for f in fields if f in Fo]
pc=lambda a,b: f"{a} ({100*a//max(1,b)}%)"
print(f"{tot} fields/elements/maps, {ns} stores, {len(allocs)} allocations")
print(f"fields: holder {pc(len(H),tot)}, mixed {pc(len(M),tot)}, shared {pc(len(Sx),tot)}")
la=[a for a in allocs if a not in Fo]
print(f"allocations that may be local: {pc(len(la),len(allocs))}")
for sh in show.split(','):
    k=[f for f in fields if f.startswith('f:'+sh)]
    ka=[a for a in allocs if ('/src/'+sh.rstrip('.')+'/') in a]
    print(f"  {sh:12} fields holder {sum(1 for f in k if f not in P)}, mixed {sum(1 for f in k if f in P and f not in Fo)}, shared {sum(1 for f in k if f in Fo)}; allocations that may be local {sum(1 for a in ka if a not in Fo)}/{len(ka)}")
def chain(n):
    out=[]; seen=set()
    while n in why and n not in seen and len(out)<8:
        seen.add(n); w=why[n]; out.append(n.replace('/home/quentin/git/go/src/',''))
        if w.startswith('holds '): n=w[6:]
        elif w.startswith('flows to '): out.append('[flows to]'); n=w[9:]
        else: out.append(w.replace('/home/quentin/git/go/src/','')); break
    return ' <- '.join(out)
ends=collections.Counter()
sh0=show.split(',')[0]
for a in allocs:
    if a in Fo and ('/src/'+sh0.rstrip('.')+'/') in a:
        c=chain(a).split(' <- '); ends[c[-1][:170]]+=1
        if verbose: print('   ',chain(a)[:600])
print(f"why allocations in {sh0} are shared:")
for k2,v in ends.most_common(10): print('  ',v,k2)
