# usage: python3 timeline.py out/run.txt... -- per call, from a TRACE=1 run: when the bridge came up, each
# birthday round, the phone's pool, every hit (home's attempt finding a path) and whether it finished, and when
# both sides went direct. Times are seconds from the call's start. With the run's all.txt next to run.txt it also
# counts, from round start to the first hit: home's distinct probed ports and the phone's new mappings toward home.
import re, sys, os

def calls(path):
    ev, wrap, last = [], 0, 0
    for l in open(path):
        m = re.match(r'MARK (\S+) call (\d+)', l) or re.match(r'([AB]) console TRACE (\S+) (\d+) (\w+) (.*)', l)
        r = re.match(r'RESULT call (\d+): (.*)', l)
        if r: ev.append((None, 'result', r[2])); continue
        if not m: continue
        t = float(m[1] if l.startswith('MARK') else m[2])
        if t + wrap < last - 500: wrap += 1000
        t += wrap; last = t
        ev.append((t, 'mark', int(m[2])) if l.startswith('MARK') else (t, m[1], int(m[3]), m[4], m[5]))
    out, cur = [], None
    for e in ev:
        if e[1] == 'mark': cur = {'t0': e[0], 'n': e[2], 'ev': []}; out.append(cur)
        elif e[1] == 'result': cur and cur.setdefault('result', e[2])
        elif cur: cur['ev'].append(e)
    return out

def packets(path):
    out = []
    for l in open(path):
        m = re.match(r'(\S+) IP 10\.0\.0\.(\d)\.(\d+) > 10\.0\.0\.(\d)\.(\d+): UDP, length (\d+)', l)
        if m and int(m[6]) < 120: out.append((float(m[1]) % 1000, m[2], m[3], m[4], m[5]))
    return out

def rates(pk, a, b):
    w = lambda t: (t - a) % 1000 < b - a # trace times are seconds mod 1000
    probes = {d for t, s, sp, d_, d in pk if w(t) and s == '3' and d_ == '2'}
    maps = {sp for t, s, sp, d_, d in pk if w(t) and s == '2' and d_ == '3'}
    return round(len(probes) / (b - a), 1), round(len(maps) / (b - a), 1)

def summary(c, pk=None):
    t0, s = c['t0'], {'n': c['n'], 'result': c.get('result', '?')}
    rel = lambda t: round(t - t0, 2)
    ev = c['ev']
    s['bridge'] = next((rel(t) for t, w, i, k, a in ev if w == 'A' and k == 'turn' and a == 'dc open'), None)
    s['rounds'] = [rel(t) for t, w, i, k, a in ev if w == 'A' and k == 'birthday' and a == 'new']
    pool = [t for t, w, i, k, a in ev if w == 'B' and k == 'birthday' and a == 'new']
    s['pool_first'] = rel(pool[0]) if pool else None
    s['pool_made'] = len(pool)
    hits = []
    for t, w, i, k, a in ev:
        if w == 'A' and k == 'birthday' and a == 'ice connected':
            done = next((rel(u) for u, w2, j, k2, b in ev if w2 == 'A' and j == i and b == 'dc ack'), None)
            hits.append((rel(t), done))
    s['hits'] = hits
    s['direct'] = max((rel(t) for t, w, i, k, a in ev if a == 'dc ack'), default=None)
    m = re.search(r'direct after (\d+)s', s['result'])
    s['secs'] = int(m[1]) if m else None
    if pk and s['rounds'] and hits:
        s['probes/s'], s['maps/s'] = rates(pk, t0 + s['rounds'][0], t0 + hits[0][0])
    return s

if __name__ == '__main__':
    for path in sys.argv[1:]:
        cap = os.path.join(os.path.dirname(path), 'all.txt')
        pk = packets(cap) if os.path.exists(cap) else None
        for c in calls(path):
            s = summary(c, pk)
            print(path, s)
