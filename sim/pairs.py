import sys
span=int(sys.argv[2]) if len(sys.argv)>2 else 64512  # ports the phone's NAT picks from
first={}  # (phone port, home port) -> first time the phone sent on it
probes=[]
for l in open(sys.argv[1]):
    f=l.split()
    if len(f)<5 or f[1]!='IP': continue
    t=float(f[0]); src=f[2]; dst=f[4].rstrip(':')
    sip,sp=src.rsplit('.',1); dip,dp=dst.rsplit('.',1)
    if sip=='10.0.0.2' and dip=='10.0.0.3': first.setdefault((sp,dp),t)
    if sip=='10.0.0.3' and dip=='10.0.0.2': probes.append((t,dp,sp))
# a hit: home probes a tuple after the phone has opened it; repeat probes of one tuple count once
hits={}
for t,pp,hp in probes:
    if first.get((pp,hp),t+1)<=t: hits.setdefault((pp,hp),(t,pp,hp,first[(pp,hp)]))
hits=sorted(hits.values())
print("exact tuple hits:",len(hits), "probes:",len(probes), "phone tuples:",len(first))
for h in hits[:10]: print("  probe t=%.2f to phone:%s from home:%s, phone first sent at %.2f"%h)
homeports=sorted(set(hp for _,_,hp in probes)); print("home probe source ports:",homeports[:10])
print("phone dest home ports:",sorted(set(d for _,d in first))[:12])
from collections import defaultdict
pm=defaultdict(set); pr=defaultdict(set)
for (pp,hp) in first: pm[hp].add(pp)
for _,pp,hp in probes: pr[hp].add(pp)
exp=0
for hp in pr:
    e=len(pm[hp])*len(pr[hp])/span; exp+=e
    print("home socket",hp,"phone mappings to it",len(pm[hp]),"distinct probed",len(pr[hp]),"hits",len(pm[hp]&pr[hp]),"expected %.2f"%e)
print("expected total hits %.2f"%exp)
ts=defaultdict(list)
for t,pp,hp in probes: ts[hp].append(t)
t0=min(first.values()) if first else 0
for hp,l in ts.items(): print("socket",hp,"probes %d from +%.1fs to +%.1fs (%.0f/s)"%(len(l),min(l)-t0,max(l)-t0,len(l)/max(.1,max(l)-min(l))))
