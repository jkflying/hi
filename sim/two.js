// usage (see nat.sh): node two.js URL_HOME URL_PHONE SECS -- home and phone join the same room; prints each
// page's view of the other every second and a RESULT line per call: direct after Ns, split (one side direct,
// the other not, which was a real bug), or none. RELOAD=N reloads the phone after each direct call, N times;
// with DROP=1 the phone instead quietly closes its direct connection, as if it died on that side only.
// SEED=n makes each page's Math.random and crypto.getRandomValues repeatable (ids, so who offers; credentials;
// the ports home probes). LOSE_ACK=1 makes the phone miss its first ack and all after it on that connection (a one-sided adoption). TRACE=1 prints when each connection is made, its state changes and data channel events
// (sim/timeline.py turns that into a per-call breakdown); TRACE=2 also has Firefox write its ICE, SDP and data
// channel logs to out/moz-A* and out/moz-B* (~200MB a run on the phone).
const pw = require('playwright-core')
const [a, b, secs] = process.argv.slice(2)
const reloads = +(process.env.RELOAD || 0), sleep = ms => new Promise(r => setTimeout(r, ms))
const origins = [...new Set([a, b].map(u => new URL(u).origin))].join(',')
;(async () => {
  const open = async (url, name) => {
    const kind = process.env['BR_' + name]
    const moz = process.env.TRACE == 2 && kind == 'firefox' ? {MOZ_LOG: 'timestamp,jsep:4,mtransport:4,DataChannel:4,sctp:4', MOZ_LOG_FILE: '/out/moz-' + name} : {}
    const br = await pw[kind].launchPersistentContext(require('fs').mkdtempSync('/tmp/ud'), {ignoreHTTPSErrors: true, executablePath: process.env['EXE_' + name],
      env: {...process.env, ...moz},
      args: ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream', '--unsafely-treat-insecure-origin-as-secure=' + origins, '--no-sandbox'].filter(() => kind == 'chromium'),
      // with a real mic granted Firefox shows which local socket each public port is from (raddr); the fake one doesn't
      firefoxUserPrefs: {'media.navigator.streams.fake': true, 'media.navigator.permission.disabled': true, 'media.peerconnection.ice.obfuscate_host_addresses': false}})
    // the race behind a real split: closing an attempt whose ICE found a path while its handshake is still running
    await br.addInitScript(() => {
      const close = RTCPeerConnection.prototype.close
      RTCPeerConnection.prototype.close = function () {
        if (/connected|completed/.test(this.iceConnectionState) && this.connectionState == 'connecting') (window.CUT ||= []).push(this.connectionState)
        return close.call(this)
      }
    })
    // PHONE_LAG=ms: the phone's page thread is busy that long out of every 2x, like a slow phone running a big pool,
    // so events (a connection finishing) wait while timers (the pool's cull) run
    if (name == 'B' && +process.env.PHONE_LAG) await br.addInitScript(ms => setInterval(() => { for (const t = Date.now() + ms; Date.now() < t;); }, 2 * ms), +process.env.PHONE_LAG)
    // LOSE_ACK=1: the phone misses the first ack, as when home has switched to another pool connection (see mkpc)
    if (name == 'B' && process.env.LOSE_ACK) await br.addInitScript(() => {
      const dc = RTCPeerConnection.prototype.createDataChannel; let lost
      RTCPeerConnection.prototype.createDataChannel = function (...a) {
        const d = dc.apply(this, a)
        // from the first ack on, nothing more arrives on that channel, as home is sending to another connection
        d.addEventListener('message', e => (lost == d || !lost && /"t":"ack"/.test(e.data) && (lost = d, console.debug('TRACE lost from here'), 1)) && e.stopImmediatePropagation())
        return d
      }
    })
    // seeded per page and per load, so a reloaded phone doesn't reuse its old id
    if (process.env.SEED) await br.addInitScript(s => {
      let n = 0; try { n = +sessionStorage.loads || 0; sessionStorage.loads = n + 1 } catch {}
      let a = s + n * 7919 >>> 0
      const r = () => { a = a + 0x6D2B79F5 | 0; let t = Math.imul(a ^ a >>> 15, 1 | a); t = t + Math.imul(t ^ t >>> 7, 61 | t) ^ t; return ((t ^ t >>> 14) >>> 0) / 4294967296 }
      Math.random = r
      crypto.getRandomValues = v => { const b = new Uint8Array(v.buffer, v.byteOffset, v.byteLength); for (let i = 0; i < b.length; i++) b[i] = r() * 256; return v }
    }, +process.env.SEED * 2 + (name == 'B'))
    // TRACE: number each connection (main, turn bridge, birthday = burst or pool) and log what happens to it
    if (process.env.TRACE) await br.addInitScript(() => {
      const P = RTCPeerConnection; let n = 0
      window.RTCPeerConnection = function (c) {
        const pc = new P(c), id = ++n, kind = c?.iceTransportPolicy ? 'turn' : c?.certificates ? 'birthday' : 'main'
        const log = (...a) => console.debug('TRACE', (Date.now() % 1e6 / 1000).toFixed(3), id, kind, ...a)
        log('new')
        pc.addEventListener('iceconnectionstatechange', () => log('ice', pc.iceConnectionState))
        pc.addEventListener('connectionstatechange', () => log('conn', pc.connectionState))
        const dc = pc.createDataChannel.bind(pc)
        pc.createDataChannel = (...a) => { const d = dc(...a); ['open', 'close', 'error'].forEach(e => d.addEventListener(e, () => log('dc', e)))
          d.addEventListener('message', e => /"t":"(hi|ack)"/.test(e.data) && log('dc', JSON.parse(e.data).t)); return d }
        const close = pc.close.bind(pc)
        pc.close = () => (log('close', pc.iceConnectionState, pc.connectionState), close())
        return pc
      }
      RTCPeerConnection.prototype = P.prototype; RTCPeerConnection.generateCertificate = P.generateCertificate
    })
    const pg = br.pages()[0] || await br.newPage()
    pg.on('console', m => (process.env.DBG || process.env.TRACE && /^TRACE/.test(m.text()) || /warn|error/.test(m.type()) && !/ICE failed/.test(m.text())) && console.log(name, 'console', m.text().slice(0, 200)))
    pg.on('pageerror', e => console.log(name, 'pageerror', e.message))
    pg.join = async () => { await pg.goto(url); await pg.fill('#name', name); await pg.click('#lobby .primary') }
    await pg.join()
    return pg
  }
  const view = pg => pg.evaluate(() => Object.values(peers).map(p => ({ok: !!p.ok && p.pc.connectionState == 'connected', s: `ok=${!!p.ok} live=${!!p.live} relay=${!!p.relay} tr=${p.tr?.connectionState} round=${p.round} bs=${p.bs.length} "${p.i.textContent}"`})))
  // one call: until both sides are direct, or one is and the other hasn't caught up within 10s, or time's up
  const cuts = async () => (await Promise.all([A, B].map(pg => pg.evaluate(() => (window.CUT || []).length)))).join('/')
  const call = async n => {
    const t0 = Date.now()
    console.log('MARK', (t0 % 1e6 / 1000).toFixed(3), 'call', n)
    let oneSince = 0
    await A.evaluate(() => window.CUT = []) // the phone's resets with its reload
    for (let s = 1; s <= secs; s++) {
      await sleep(1000)
      const [h, p] = await Promise.all([A, B].map(view))
      console.log(`${n}:${s}s home sees: ${h.map(x => x.s).join(' | ') || '-'}  || phone sees: ${p.map(x => x.s).join(' | ') || '-'}`)
      const hok = h.length == 1 && h[0].ok, pok = p.length == 1 && p[0].ok
      if (hok && pok) return console.log(`RESULT call ${n}: direct after ${Math.round((Date.now() - t0) / 1000)}s, mid-handshake closes home/phone ${await cuts()}`), true
      if (hok != pok) { if ((oneSince ||= s) && s - oneSince >= 10) return console.log(`RESULT call ${n}: mid-handshake closes home/phone ${await cuts()}, split, home ${hok ? 'direct' : h.length ? 'not direct' : 'dropped the phone'}, phone ${pok ? 'direct' : p.length ? 'not direct' : 'dropped home'}`), false }
      else oneSince = 0
    }
    console.log(`RESULT call ${n}: no direct connection in ${secs}s, mid-handshake closes home/phone ${await cuts()}`)
  }
  const A = await open(a, 'A'); await sleep(500); const B = await open(b, 'B')
  for (let n = 0; n <= reloads; n++) {
    if (n && process.env.DROP) { await B.evaluate(() => Object.values(peers).forEach(p => p.pc.close())); console.log('--- phone dropped its connection') }
    else if (n) { await B.join(); console.log('--- phone reloaded') }
    if (!await call(n + 1)) break
  }
  process.exit(0)
})().catch(e => { console.log('RESULT harness error:', e.message); process.exit(1) })
