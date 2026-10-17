package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pion/turn/v4"
)

// TURN relay: a bridge while hole punching keeps searching, and the fallback when it never succeeds.
// Abuse limits: credentials are only handed to members of a room with someone else in it, and stop
// working when the member leaves; relays may only talk to each other (both sides relay, so nobody can
// bounce traffic to third parties); every relay port and the whole server are rate capped.
const (
	relayMin, relayMax = 50000, 50199   // 2 ports per call
	allocRate          = 1_000_000 / 8  // bytes/s each relay port may send: video is capped at 300 kbit/s
	totalRate          = 20_000_000 / 8 // bytes/s for all relays together
	credTTL            = 12 * time.Hour
)

var (
	turnSecret = make([]byte, 32)
	turnPort   string
	everyone   = &bucket{rate: totalRate, burst: totalRate / 4}
)

func turnServe(addr, ip string) error {
	relayIP := net.ParseIP(ip)
	if relayIP == nil { // our public address is the one we route to the internet from
		c, err := net.Dial("udp4", "8.8.8.8:53")
		if err != nil {
			return err
		}
		relayIP = c.LocalAddr().(*net.UDPAddr).IP
		c.Close()
	}
	rand.Read(turnSecret)
	conn, err := net.ListenPacket("udp4", addr)
	if err != nil {
		return err
	}
	_, turnPort, _ = net.SplitHostPort(addr)
	_, err = turn.NewServer(turn.ServerConfig{
		Realm:       "hi",
		AuthHandler: func(user, realm string, _ net.Addr) ([]byte, bool) { return turnKey(user, realm) },
		PacketConnConfigs: []turn.PacketConnConfig{{
			PacketConn:            conn,
			RelayAddressGenerator: &limitedRelay{turn.RelayAddressGeneratorPortRange{RelayAddress: relayIP, Address: "0.0.0.0", MinPort: relayMin, MaxPort: relayMax}},
			PermissionHandler:     func(_ net.Addr, peer net.IP) bool { return peer.Equal(relayIP) },
		}},
	})
	if err == nil {
		log.Printf("turn udp %s, relays %s:%d-%d", addr, relayIP, relayMin, relayMax)
	}
	return err
}

// turnKey accepts a username we issued (expiry:room:id) while it's unexpired and its owner is still in a call.
func turnKey(user, realm string) ([]byte, bool) {
	f := strings.Split(user, ":")
	exp, _ := strconv.ParseInt(f[0], 10, 64)
	if len(f) != 3 || time.Now().Unix() > exp || !inCall(f[1], f[2]) {
		return nil, false
	}
	return turn.GenerateAuthKey(user, realm, turnPass(user)), true
}

func turnPass(user string) string {
	m := hmac.New(sha1.New, turnSecret)
	m.Write([]byte(user))
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

func inCall(room, id string) bool {
	mu.Lock()
	defer mu.Unlock()
	return rooms[room][id] != nil && len(rooms[room]) > 1
}

// turnCreds hands a room member credentials for the relay.
func turnCreds(w http.ResponseWriter, r *http.Request) {
	var m struct{ Token string }
	if turnPort == "" || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&m) != nil {
		http.Error(w, "no relay", 404)
		return
	}
	mu.Lock()
	p := tokens[m.Token]
	mu.Unlock()
	if p == nil || !inCall(p.room, p.id) {
		http.Error(w, "not in a call", 403)
		return
	}
	user := fmt.Sprintf("%d:%s:%s", time.Now().Add(credTTL).Unix(), p.room, p.id)
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	json.NewEncoder(w).Encode(map[string]any{"urls": "turn:" + host + ":" + turnPort + "?transport=udp", "username": user, "credential": turnPass(user)})
}

type limitedRelay struct {
	turn.RelayAddressGeneratorPortRange
}

func (g *limitedRelay) AllocatePacketConn(network string, port int) (net.PacketConn, net.Addr, error) {
	c, a, err := g.RelayAddressGeneratorPortRange.AllocatePacketConn(network, port)
	if err != nil {
		return nil, nil, err
	}
	return limited{c, &bucket{rate: allocRate, burst: allocRate / 2}}, a, nil
}

// limited drops what a relay port sends beyond its own and the global rate. Each packet passes
// exactly one relay's WriteTo, so this caps both directions.
type limited struct {
	net.PacketConn
	b *bucket
}

func (c limited) WriteTo(p []byte, a net.Addr) (int, error) {
	if !c.b.take(len(p)) || !everyone.take(len(p)) {
		return len(p), nil
	}
	return c.PacketConn.WriteTo(p, a)
}

type bucket struct {
	mu          sync.Mutex
	rate, burst float64
	have        float64
	at          time.Time
}

func (b *bucket) take(n int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.have = min(b.burst, b.have+b.rate*now.Sub(b.at).Seconds())
	b.at = now
	if b.have < float64(n) {
		return false
	}
	b.have -= float64(n)
	return true
}
