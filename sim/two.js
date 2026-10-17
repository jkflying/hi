// usage (see nat.sh): node two.js URL_HOME URL_PHONE SECS -- home and phone join the same room; prints each
// page's view of the other every second and a RESULT line per call: direct after Ns, split (one side direct,
// the other not, which was a real bug), or none. RELOAD=N reloads the phone after each direct call, N times;
// with DROP=1 the phone instead quietly closes its direct connection, as if it died on that side only.
const pw = require('playwright-core')
const [a, b, secs] = process.argv.slice(2)
const reloads = +(process.env.RELOAD || 0), sleep = ms => new Promise(r => setTimeout(r, ms))
const origins = [...new Set([a, b].map(u => new URL(u).origin))].join(',')
;(async () => {
  const open = async (url, name) => {
    const kind = process.env['BR_' + name]
    const br = await pw[kind].launchPersistentContext(require('fs').mkdtempSync('/tmp/ud'), {ignoreHTTPSErrors: true, executablePath: process.env['EXE_' + name],
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
    const pg = br.pages()[0] || await br.newPage()
    pg.on('console', m => (process.env.DBG || /warn|error/.test(m.type()) && !/ICE failed/.test(m.text())) && console.log(name, 'console', m.text().slice(0, 200)))
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
