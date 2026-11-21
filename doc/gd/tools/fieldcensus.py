import sys, collections
FRESH,RESULT,PARAM,SHARED,STATIC,UNKNOWN,LOAD=1,2,4,8,16,32,64
stores=collections.defaultdict(list)
for line in open(sys.argv[1], errors='replace'):
    if not line.startswith('fieldcensus\t'): continue
    p=line.rstrip('\n').split('\t')
    if len(p)<7: continue
    key,cat,incat,deps,ops,pos=p[1],int(p[2]),int(p[3]),p[4],p[5],p[6]
    if incat==SHARED: continue   # the container is certainly shared
    stores[key].append((cat,[d for d in deps.split('|') if d],ops,pos))
def solve(bad):
    dem={}
    for k,ss in stores.items():
        for cat,deps,ops,pos in ss:
            if cat&bad:
                dem.setdefault(k,(cat&bad,pos)); break
    ch=True
    while ch:
        ch=False
        for k,ss in stores.items():
            if k in dem: continue
            for cat,deps,ops,pos in ss:
                d=[x for x in deps if x in dem and x!=k]
                # a value loaded from a field nobody stores to here is unknown too
                if d:
                    dem[k]=('load '+d[0],pos); ch=True; break
    return dem
total=len(stores); nst=sum(len(v) for v in stores.values())
print(f"{total} fields, elements and maps with {nst} stores")
modes=[('optimistic (shared, unknown demote)',SHARED|UNKNOWN),('+ parameters demote',SHARED|UNKNOWN|PARAM),('+ call results demote',SHARED|UNKNOWN|PARAM|RESULT)]
res={}
for name,bad in modes:
    dem=solve(bad); res[name]=dem
    ok=[k for k in stores if k not in dem]
    print(f"{name}: {len(ok)} holder ({100*len(ok)//total}%), covering {sum(len(stores[k]) for k in ok)} stores ({100*sum(len(stores[k]) for k in ok)//nst}%)")
def kind(k):
    return 'elem' if k.startswith('elem ') else 'key' if k.startswith('key ') else 'field'
for name,bad in modes[:2]:
    dem=res[name]
    c=collections.Counter(); t=collections.Counter()
    for k in stores:
        t[kind(k)]+=1
        if k not in dem: c[kind(k)]+=1
    print(name, {x:f"{c[x]}/{t[x]}" for x in t})
    why=collections.Counter()
    for k,(w,pos) in dem.items():
        if isinstance(w,int):
            why['shared' if w&SHARED else 'unknown' if w&UNKNOWN else 'param' if w&PARAM else 'result']+=1
        else: why['loaded from a demoted field']+=1
    print('  demoted by:',dict(why))
pk=sys.argv[2] if len(sys.argv)>2 else 'go/ast.'
print(f"--- {pk} fields")
for name,bad in modes[:2]:
    dem=res[name]
    ks=[k for k in stores if kind(k)=='field' and k.startswith(pk)]
    okk=[k for k in ks if k not in dem]
    print(f"{name}: {len(okk)}/{len(ks)} holder")
    if bad==SHARED|UNKNOWN:
        for k in sorted(ks):
            if k in dem:
                w,pos=dem[k]; print('   demoted', k, w, pos.replace('/home/quentin/git/go/src/',''))
ops=collections.Counter()
for k,ss in stores.items():
    for cat,deps,o,pos in ss:
        if cat&UNKNOWN:
            for x in o.split('|'): ops[x]+=1
print('unknown operations:',ops.most_common(12))
def chain(k, dem):
    out=[]
    seen=set()
    while k in dem and k not in seen:
        seen.add(k); w,pos=dem[k]; out.append(f"{k} <- {w} at {pos.replace('/home/quentin/git/go/src/','')}")
        if isinstance(w,str): k=w[5:]
        else: break
    return out
for k in sys.argv[3:]:
    print('\n'.join(chain(k,res[modes[0][0]])))
