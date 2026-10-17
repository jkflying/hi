#!/bin/bash
# Two browsers in a call across simulated NATs, in a podman container. The "phone" is behind a NAT that picks
# a new port per destination (hard), "home" behind a port-preserving one (easy); the server has STUN and TURN.
#   sim/run.sh [random|block|preserve] [secs]      phone NAT: random port / random in a 1024 block / preserving
# Environment:
#   HOME_BR, PHONE_BR    chromium (default) or firefox
#   PHONE_MS, HOME_MS    one-way delay each side adds to the internet, ms (default 30, 10), with 20% jitter
#   PHONE_CPU            percent of one core the phone browser may use (default unlimited)
#   PHONE_LAG            ms the phone's page thread is blocked out of every 2x that (default 0)
#   HOME_NAT=cone        home's router lets in anything to a port it has used (default: only exact replies)
#   DBG=1                print all of both pages' console output
#   RELOAD=N             once both are direct, reload the phone and connect again, N times
#   DROP=1               ...instead of reloading, the phone quietly closes its direct connection
#   SEED=n               repeatable page randomness and no delay jitter (see two.js); timing still varies
#   TRACE=1              log every connection's events (sim/timeline.py); TRACE=2 adds Firefox's own logs, out/moz-*
# Results in sim/out: run.txt (per-second view from both pages), hi.log (server, with ?diag reports), all.txt (packets).
# With TRACE set, also nat.pcap (for stun.py).
set -euo pipefail
cd "$(dirname "$0")"
(cd .. && CGO_ENABLED=0 GOTOOLCHAIN=local go build -o hi .) # index.html is embedded, so rebuild for every run
podman build -q -t hisim . >/dev/null # cached unless the Containerfile changed
mkdir -p out
podman run --rm --privileged -e HOME_BR -e PHONE_BR -e PHONE_MS -e HOME_MS -e PHONE_CPU -e PHONE_LAG -e RELOAD -e DROP -e HOME_NAT -e DBG -e SEED -e TRACE -e LOSE_ACK \
  -v "$PWD:/sim:ro" -v "$PWD/out:/out" -v "$PWD/../hi:/hi:ro" hisim bash /sim/nat.sh "${1:-random}" "${2:-90}" | tee out/run.txt
