# usage: python3 stun.py out/nat.pcap [from to] -- the STUN packets in a TRACE run's capture (the phone NAT's public
# side), one a line: time, source > destination, request/response, and the ICE attributes that decide which pair
# gets picked: role (controlling/controlled), USE-CANDIDATE, PRIORITY. Times are seconds mod 1000, as in TRACE.
import struct, sys

def packets(path):
    f = open(path, 'rb'); magic, = struct.unpack('<I', f.read(24)[:4])
    e = '<' if magic in (0xa1b2c3d4, 0xa1b23c4d) else '>'; ns = magic in (0xa1b23c4d, 0x4d3cb2a1)
    while h := f.read(16):
        s, us, n, _ = struct.unpack(e + 'IIII', h); d = f.read(n)
        yield (s + us / (1e9 if ns else 1e6)) % 1000, d

def stun(d):
    i = 14 if d[12:14] == b'\x08\x00' else 0 # ethernet or raw IP
    if d[i] >> 4 != 4 or d[i + 9] != 17: return
    ip = lambda o: '.'.join(map(str, d[o:o + 4]))
    i, src, dst = i + (d[i] & 15) * 4, ip(i + 12), ip(i + 16)
    sp, dp = struct.unpack('>HH', d[i:i + 4]); m = d[i + 8:]
    if len(m) < 20 or m[4:8] != b'\x21\x12\xa4\x42': return
    t, n = struct.unpack('>HH', m[:4]); a, o = [], 20
    while o + 4 <= 20 + n:
        k, l = struct.unpack('>HH', m[o:o + 4]); v = m[o + 4:o + 4 + l]
        if k == 0x24: a.append('prio=%d' % struct.unpack('>I', v))
        elif k == 0x25: a.append('USE-CANDIDATE')
        elif k == 0x802a: a.append('controlling')
        elif k == 0x8029: a.append('controlled')
        elif k == 0x9: a.append('error')
        o += 4 + (l + 3) // 4 * 4
    return f'{src}:{sp} > {dst}:{dp}', {0x1: 'req', 0x101: 'ok', 0x111: 'err'}.get(t, hex(t)), a

if __name__ == '__main__':
    lo, hi = (float(x) for x in sys.argv[2:4]) if len(sys.argv) > 3 else (0, 1000)
    for t, d in packets(sys.argv[1]):
        if lo <= t <= hi and (s := stun(d)): print(round(t, 3), s[0], s[1], *s[2])
