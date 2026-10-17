#!/bin/bash
# Build hi, installing Go first if needed (Debian/Ubuntu).
#   ./build.sh           test and build ./hi
#   ./build.sh install   also install it as a systemd service and open the STUN port (run as root)
# The service listens on 127.0.0.1:8090 (see hi.service); a TLS proxy must forward the site to it.
set -euo pipefail
cd "$(dirname "$0")"
GO_MIN=1.22
PATH=$PATH:/usr/lib/go/bin

go_ok() { command -v go >/dev/null && [ "$(printf 'go%s\n%s\n' $GO_MIN "$(go env GOVERSION)" | sort -V | head -1)" = go$GO_MIN ]; }
if ! go_ok; then
  [ "$(id -u)" = 0 ] || { echo "Need Go >= $GO_MIN: run as root, or apt-get install golang-go" >&2; exit 1; }
  apt-get update -q && DEBIAN_FRONTEND=noninteractive apt-get install -yq golang-go
  go_ok || { echo "apt's Go is older than $GO_MIN" >&2; exit 1; }
fi

go test ./...
CGO_ENABLED=0 go build -trimpath -ldflags=-s -o hi .
[ "${1:-}" = install ] || exit 0

# only restart when something changed: a restart drops signaling (calls in progress survive, P2P)
if ! cmp -s hi /usr/local/bin/hi || ! cmp -s hi.service /etc/systemd/system/hi.service; then
  install -m755 hi /usr/local/bin/hi
  install -m644 hi.service /etc/systemd/system/hi.service
  systemctl daemon-reload
  systemctl enable -q hi
  systemctl restart hi
fi
command -v ufw >/dev/null && ufw allow 3478:3481/udp comment "hi STUN" >/dev/null
command -v ufw >/dev/null && ufw allow 3482/udp comment "hi TURN" >/dev/null && ufw allow 50000:50199/udp comment "hi TURN relays" >/dev/null
systemctl is-active -q hi && echo "hi is running"
