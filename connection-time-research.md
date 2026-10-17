# Bringing down time-to-direct: research notes

Scope: a hard NAT (Salt CGNAT, a new random port per destination) on one side, an easy home NAT
on the other, both Firefox. Limits kept: it has to work for anyone, so no asking people to change
browser or router settings; no way around Firefox's per-page STUN rate cap; and the NAT load stays
at levels that won't get anyone banned or strain carrier equipment. The status column and the sections at the end
say what has been implemented since.

## What limits us today: a model checked against data

The birthday search is a race between two capped streams:

- **Home's probes.** Home sprays random phone ports. The page cap is about 78 STUN packets/s, but
  Firefox retransmits each unanswered check. Measured in the sim capture: **2.9 packets per probe**,
  so about 27 distinct ports a second.
- **The phone's mappings.** Each pool connection checks home once and so opens one NAT mapping toward
  home's socket. Measured: **4.9 packets per mapping**, so about 16 new mappings a second. A
  connection is only useful while it is alive and still checking, so live mappings ≈ 16/s × L, with
  L = 12 s (the pool lifetime) ≈ 190. That matches the real phone's diag (`pcs` 150–200).

Hits per second: λ = probes/s × live mappings / 64512 ≈ 27 × 190 / 64512 ≈ **0.08/s**, so about
12 s to the first hit. Add the ~6 s before the first round and you get **~19 s**.
- The sim measured 9–43 s (median ~16).
- `pairs.py` counts land near "expected": 2 actual against 1.57 expected in the last run.
- So the model holds.

**Real calls are slower: 16, 17, 30, 97 and 103 s.**
- The real diag explains the gap: a hit that doesn't finish wastes the rest of its round.
- In the 09:00 call, home's ICE succeeded at 35 s with a phone connection that never completed.
- Firefox had already cancelled all ~2,000 of home's other checks (`pair_cancelled`). Nothing more
  could happen until round 2 at 65 s, and it went direct at 97 s.
- In the sim, 1 of 5 hits landed on a phone connection that was already closed: 13 s old against a
  12 s lifetime.

So time-to-direct ≈ start delay + 1/(λ·q) + (bad hits × rest of the round), where q is the
fraction of hits that finish. The levers are the start delay, λ (probe rate, live mappings, ports to
search), q, and the wasted-round penalty.

## Directions

| # | Direction | Expected gain | Pros | Cons / risks | Evidence / feasibility |
|---|---|---|---|---|---|
| 1 | **Start the next round as soon as home's attempt goes connected → failed/disconnected without being adopted** (now it waits for the ~54 s round timer) | Removes the ~30–60 s penalty after a bad hit; the slow real calls (97 s, 103 s) look like this | A few lines; no extra traffic or carrier load; works with the hold that just shipped | A run of bad hits causes several quick restarts, so it needs a floor (e.g. ≥ 5 s apart) | Real diag 09:00:26–09:01:27 shows the dead round; the sim can show it with a hit on an old connection |
| 2 | **Start the search sooner**: begin round 1 once both sides' public ports are known (~1–2 s), not at 6 s | −4 s on every hard-NAT call | Trivial; no extra cost | Needs the remote candidates first; on a slow link the ports arrive late (keep a fallback timer) | `stages()`: guess at 4 s, round at 6 s; candidates arrive in < 1 s in the sim |
| 3 | **Bring up the TURN bridge right away** (now at 4 s) | Time to a *working* call drops to ~1–2 s; direct still comes later in the background | Users only notice "it connected"; the code already exists | More server bandwidth (capped at 300 kbit/s video, 20 Mbit/s total); costs every call, even ones that would go direct in 2 s | `offerBridge` at 4000 ms in `stages()` |
| 4 | **Keep pool connections longer on Android** (12 s → ~25–30 s) | Live mappings ×2–2.5, so λ ×2–2.5: ~12 s → ~5–6 s to the first hit | Connections go quiet after ~5 checks, so keeping them costs **no STUN budget and no new carrier mappings**. It *reduces* new-mapping churn on Salt per unit of search | Only helps if an older connection still does the check back when hit. Desktop Firefox gives up (`failed`) at ~10 s, so desktop gains nothing. Android stays `connecting` (real diag), which looks promising but is unproven. More phone memory/CPU | Needs one real-phone test: pool lifetime 25 s with ?diag, then compare time-to-direct and hits that don't finish. Desktop sim can't answer it |
| 5 | **Revive given-up connections** by trickling one fresh, distinct remote candidate to old pool connections, putting them back in checking so a late hit can finish | Same ×2–2.5 as #4, on desktop Firefox too | Pairs with #4 | Each revival costs a check (~5 packets) against the same cap, which may eat the gain. Untested whether Firefox does the check back once revived. Fiddly | The code's comment says re-adding the *same* candidate does nothing; a *new* one is untested. Sim experiment: ~1 h |
| 6 | **Split home's probing over 2 attempts** (H=2 for Firefox, each at half rate) | A bad hit kills only half the probing instead of the whole round | Small change; same total rate | #1 does the same job more directly; two attempts double some overhead | Chrome already uses H=4 |
| 7 | ~~Home router settings~~ | — | — | **Out: needs the user to change a setting.** No automatic or click-through route: pages can't speak UPnP, PCP or NAT-PMP. Routers that already allow it go direct in ~2 s with today's code (sim, `HOME_NAT=cone`) | — |
| 8 | **IPv6 direct** (no NAT on either side) | < 1 s when both have it | ICE already tries IPv6 host candidates with no code; only firewalls in the way, and simultaneous checks usually open those | Probably not available here: both devices reached the server over IPv4 in all 214 diag posts, though the server answers on IPv6 too | Feasibility: add IPv6 host candidates to the diag report to know for sure |
| 9 | **Bigger pool when the phone already runs Chrome**, with an automatic back-off | Chrome pairs are already ~7 s in the sim; a larger pool shortens that further | Chrome has no page-wide STUN cap, and many Android users already run it, so no setting change is needed | More concurrent carrier mappings. Keep well under what Salt already handled (~1,000) and ramp up gradually. Needs a guard: if the phone's own STUN checks to our server start failing, shrink the pool. Doesn't help Firefox phones | Code already adapts K by browser. The guard reuses our STUN server, so it adds no extra outside traffic |
| 10 | **Cut the retransmit tax** (2.9× and 4.9×) | Up to ×3 on probes and ×5 on mappings, so λ up to ~×15 | The biggest theoretical lever | Pages can't set the retransmit count (it's a Firefox setting, `stun_client_maximum_transmits`). Closing an attempt stops its retransmits but also its socket, which kills the hit. ICE restarts cancel in-flight checks, but each needs renegotiation for ~190 connections. **I see no clean in-browser way** | Measured in the sim capture. Reading Firefox's ICE (nICEr) source for a case that cancels a check while keeping the socket would settle it, but it's likely a dead end |
| 11 | **Predict the phone's ports better** | — | — | Earlier Salt measurement: per-destination ports are spread across 1024–65535 with no relation to the STUN-seen ones. Learning more would mean probing the carrier NAT harder, which is ruled out | Stage 2 (`guess`) still covers carriers that hand out ports in order |
| 12 | **Lighter phone page** (fewer creates per batch, no getStats) | Small, unless the phone's main thread is saturated | Helps slow phones keep the pool full | Diag shows the real phone already held ~180, so it isn't starved | `PHONE_LAG` in the sim shows a slow main thread mainly hurts the pool size |
| 13 | Ruled out | — | — | Getting around Firefox's STUN cap (iframes/extra pages), high-rate carrier probing, raw UDP (not possible from a page), ICE-TCP (home can't accept inbound TCP behind its NAT) | — |

## The retransmit tax, from Firefox's source (nICEr, current main)

- **The cap** (`nr_socket_prsock.cpp`, `ShouldDrop`): a static token bucket shared by the whole content
  process. It allows a 16,384-byte burst and 7,372 bytes/s sustained, and counts only STUN *requests*
  (responses are free).
  - A request it drops returns `R_WOULDBLOCK`, and the STUN client treats that as a lost packet.
  - So a dropped send still uses up one of the pair's transmissions.
- **Retransmits** (`stun_client_ctx.cpp`): 7 transmissions. The gap starts at the RTO and doubles each
  time (backoff 2.0), with a final wait of 16 × RTO. The count and backoff come from Firefox settings
  (`stun.client.maximum_transmits`), which a page can't change.
- **RTO** (`ice_candidate_pair.cpp`): `max(100 ms, Ta × waiting_pairs)`, with Ta = 20 ms, fixed when the
  pair is *created*. Only pairs in the WAITING state count.
  - Trickled pairs are created FROZEN and unfrozen one at a time (`ice_media_stream.cpp`).
  - So with our trickle, waiting_pairs ≈ 0 and RTO is always the 100 ms floor.
  - Batching candidates doesn't change that, because batched pairs are created FROZEN too.
- **Pacing**: one new check per 20 ms per connection, so a single connection can ask for ~50 checks/s.
  At ~112 bytes each, one connection nearly fills the 7.4 kB/s cap by itself.
- **Cancelling**: a pair's retransmits stop only when it succeeds or fails, when another pair is
  nominated, on an ICE restart, or when the connection closes. There is no page-side way to stop one
  pair's retransmits but keep its socket.

So the page can't change the retransmit count. It can only control what goes into the byte budget:

| Lever | Effect | Status |
|---|---|---|
| Pool only checks the easy side's public address (not its LAN host candidates) and skips STUN gathering | Phone mappings/s 9.7 → ~15 (+50%) in the sim; median call 15 → 12–14 s | **Implemented, simulated** |
| TURN bridge setup competes for the same budget | A bridge started at the same time as a round failed to come up in ~8 of 30 sim calls | **Fixed:** the first round waits for the bridge (at most 4 s) |
| Shorter ufrag (16 → 4 chars, the RFC minimum) | Each check ~112 → ~88 bytes, ~+25% checks in the same byte budget | **Implemented** (checks are 88 bytes in the sim capture). The 32-char password and DTLS fingerprint still authenticate |
| Inflate RTO through waiting_pairs | Would cut retransmits a lot | Dead end: needs extra pairs that are checked themselves, which costs more than it saves |
| Fewer retransmits on the phone (each mapping needs only its first packet) | Up to ×4 on mappings | Not possible from a page (see above) |

## Status (sim: Firefox on both sides, phone behind a random-port NAT, 16 calls each, reconnect after a drop)

| Version | Direct | Median | Slowest | TURN bridge live |
|---|---|---|---|---|
| Live code (fb6d06d) | 16/16 | ~19 s | 71 s | 5 s, every call |
| + #1–#4 | 16/16 | 15 s | 32 s | 2 s, but failed to come up in some calls (1 fell back to chat-only) |
| + leaner pool | 31/32 | 12–14 s | 31 s | same problem; 1 call split (not reproduced in a further 16 with full logging) |
| + round waits for the bridge | 16/16 | 13.5 s | 23 s | 2–4 s, every call |
| + 4-char ufrag, hi/ack (**current working tree**, pool 16/s) | 60/60 | 13 s | 29 s | 2–4 s, every call |
| same, pool 48/s | 63/63 | 9 s | 24 s (p90 13 s) | same |

## The split, explained (sim/until-split.sh, SEED=1, first call)

All of the phone's pool connections share one ufrag, password and certificate, so home's single connection sees
them as one peer with many candidates. In the split:

- 11.67 s: home connects to pool connection A, finishes DTLS, opens its data channel and drops the bridge.
- 11.96 s: home's check to pool connection B, which had already been checking home, succeeds. Firefox switches
  the selected pair to B within ~100 ms.
- B has no DTLS state, so the phone never gets its data channel. Home is "direct", and the phone sits on a dead
  bridge.

Fix (working tree):

- A side adopts a direct connection only after the other side has answered on it: each sends `hi` when the data
  channel opens, replies `ack` to a `hi`, and adopts on the `ack`. If home has switched to the wrong pool
  connection, neither side gets an ack, neither drops the bridge, and home's 8 s check starts a new round.
- The first pool connection to reach ICE connected also closes the rest, so this rarely costs a round.
- The last message can still be lost after a switch (a closed pool connection's socket answers for ~0.3 s), so a
  side that adopted and hears nothing on the direct connection for 5 s drops the call and reconnects (`adopt`).
  Tested with `LOSE_ACK=1`: home gave up 5 s after adopting, the bridge was back 2.5 s later, direct at 35 s.

## Where the time goes (sim/timeline.py, Firefox both sides, 60 calls at pool 16/s, 63 at 48/s)

Traced per call from the TRACE=1 events: the call starts at `stages()`.

| Phase | Code | Pool 16/s: median / max | Pool 48/s: median / max |
|---|---|---|---|
| Signaling, TURN bridge up | `offerBridge` at 0.5 s | 1.9 s / 3.3 s | 1.9 s / 3.6 s |
| First round starts | `first()`: hard NAT seen and bridge up (or 4 s) | right after the bridge | same |
| **Search**: round start → home's first hit | `burst` (home ~54 probes/s, Ta-bound) × phone pool (`onBurst`, `open`) | **10.0 s / 26.5 s** | **5.6 s / 13.5 s** |
| Hit → both sides direct | DTLS, data channel, hi/ack | 0.4 s / 0.4 s | 0.4 s / 1.9 s |
| Lost hits (each costs ~8–15 s: the 8 s check or 5 s backstop, then a new round) | `burst`'s 8 s check, `adopt` | 0 | 1 in 63 calls |
| Round boundary (pool reset) | `p.next` every 54 s | never reached | never reached |

So nearly all of the time, and all of the tail, is the search. It follows the model: hits/s = probes/s × live mappings
/ 64512, with live mappings ramping up at the pool rate. Home's rate is fixed by Firefox (one new check per 20 ms), so
**the phone's pool rate is the lever**. At 16/s the phone used ~17 new mappings/s against a budget of ~84/s (88-byte
checks, 7.4 kB/s, sent once each). Measured on seeds 1–8:

| Pool rate | Maps/s | Median call | p90 | Slowest | Search median / max |
|---|---|---|---|---|---|
| 16/s | 17 | 12 s | 16 s | 29 s | 10.1 / 26.5 s |
| 32/s | 34 | 10 s | 14 s | 20 s | 7.2 / 16.8 s |
| 48/s | 49 | 7–9 s | 12–13 s | 13–24 s | 5.2–5.6 / 10–13.5 s |

### What a faster pool broke, and the fixes

At 48/s, 2–3 hits in 64 calls didn't finish at first, each costing a ~15 s round. They were never seen at 16/s. Found
with `sim/stun.py` (decodes the STUN attributes in the kept pcap):

- **New pool connections after a hit.** The pool kept opening connections until home's `hold` arrived over
  signaling. One opened after the hit checked home, and home switched its selected pair to it, after DTLS (SEED=10 call
  3). Fix: the phone holds the pool itself as soon as one of its connections connects.
- **Sprayed candidates had the lowest priority.** They were `1e6 - k`. nICEr nominates on every check from the
  controlling side and replaces the nominated pair with any higher-priority one, so a peer-reflexive pair from any
  other pool connection's check (priority ~1.85e9) outranked the hit pair. Fix: `2^31 - 1 - k`, the RFC 8839 maximum
  (nICEr's parser rejects anything higher). After a hit, only another hit can outrank it.
- **Tried and reverted:** closing the rest of the pool only once DTLS completes, so that if two connections are hit at
  once, the one home picked survives. Instead, 11 of 64 hits switched away, as more pool connections finished ICE with
  home. The rare double hit is left to the backstop.

### Ways to cut the worst case further, ranked

1. **Raise the pool rate (48/s halves the search tail in the sim).** It costs no extra STUN budget on the phone (it's
   spent anyway) but means more live carrier mappings: POOL × LIFE ≈ 580 on desktop and ~1200 on Android at 48/s,
   against ~1000 Salt is known to take. Options: 32/s on Android (≈ 800 live), or 48/s with Android's LIFE back at 12 s,
   or cap live connections at ~800. Needs a real-phone ?diag test: phone CPU (48 new RTCPeerConnections/s), whether the
   pool really reaches that rate, and the number of hits that don't finish. `POOL` in index.html.
2. **Keep searching past the round boundary.** `p.next` resets the pool every 54 s. The sim never gets there now,
   but slow real calls do: keep the same burst connection spraying and the pool rolling until a hit, and start over only
   after a hit that didn't finish.
3. **Start sooner.** ~1.9 s goes to signaling and bringing up the bridge before the first round. Starting the
   phone's pool (which doesn't depend on home's probes) as soon as the offer arrives would gain at most ~1–2 s, at the
   risk of the bridge failure that waiting fixed.
4. **Home's stage-1/2 probes** (`guess` at 4 s on the main connection) share home's STUN budget with the burst. On a
   random-port NAT they never hit; skipping them once the burst is running gives home ~5–10% more probes. Small.

Still to check on a real phone with ?diag: direction #4 (whether Android pool connections older than 12 s still finish
when hit) and the pool rate.
