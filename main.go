package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed index.html
var page []byte

//go:embed lab.html
var labPage []byte

type peer struct {
	id, token, room string
	joined          time.Time // first stream open, kept across reconnects: the relay is only for a call's start
	ch              chan []byte
	done            chan struct{} // closed when a reconnect replaces this stream
}

var (
	mu     sync.Mutex // ponytail: one global lock, fine for thousands of rooms; shard per room if it shows up in profiles
	rooms  = map[string]map[string]*peer{}
	tokens = map[string]*peer{}
	joins  = map[string]int{} // per-IP stream opens this minute; stops room-code scanning
	creds  = map[string]int{} // per-IP relay credentials this minute; one machine can't take all relay ports

	maxJoinsPerMin = 30
	maxCredsPerMin = 10 // a bridge per peer, plus rebuilds; carrier NAT users share an IP, so not too tight

	codeRe  = regexp.MustCompile(`^\d{6,12}$`)
	idRe    = regexp.MustCompile(`^[0-9a-f]{16}$`)
	tokenRe = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// send never blocks: a slow client loses messages rather than stalling the room.
func send(p *peer, v any) {
	b, _ := json.Marshal(v)
	select {
	case p.ch <- b:
	default:
	}
}

// events is the per-client SSE stream. The client picks its own id and secret
// token so an EventSource auto-reconnect keeps the same identity and existing
// P2P links survive a server blip.
func events(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := &peer{id: q.Get("id"), token: q.Get("token"), room: r.PathValue("code"), ch: make(chan []byte, 256), done: make(chan struct{})}
	if !codeRe.MatchString(p.room) || !idRe.MatchString(p.id) || !tokenRe.MatchString(p.token) {
		http.Error(w, "bad request", 400)
		return
	}

	ip := clientIP(r)
	mu.Lock()
	if joins[ip]++; joins[ip] > maxJoinsPerMin {
		mu.Unlock()
		http.Error(w, "slow down", 429)
		return
	}
	room := rooms[p.room]
	if room == nil {
		room = map[string]*peer{}
		rooms[p.room] = room
	}
	old := room[p.id]
	if t := tokens[p.token]; (old != nil && old.token != p.token) || (t != nil && t != old) {
		mu.Unlock()
		http.Error(w, "id or token taken", 409)
		return
	}
	p.joined = time.Now()
	if old != nil { // reconnect: take over the queue so nothing pending is lost, retire the stale stream
		p.ch, p.joined = old.ch, old.joined
		close(old.done)
	}
	ids := []string{}
	for id := range room {
		if id != p.id {
			ids = append(ids, id)
		}
	}
	room[p.id], tokens[p.token] = p, p
	mu.Unlock()

	defer func() {
		mu.Lock()
		if room[p.id] == p {
			delete(room, p.id)
			delete(tokens, p.token)
			if len(room) == 0 {
				delete(rooms, p.room)
			}
		}
		mu.Unlock()
		// Announce the leave after a grace period, so a quick SSE reconnect isn't one.
		// Clients only act on it for peers they reach via the relay, not P2P.
		time.AfterFunc(10*time.Second, func() {
			mu.Lock()
			defer mu.Unlock()
			if rooms[p.room][p.id] != nil {
				return
			}
			for _, o := range rooms[p.room] {
				send(o, map[string]any{"t": "leave", "id": p.id})
			}
		})
	}()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	hello, _ := json.Marshal(map[string]any{"t": "hello", "peers": ids})
	fmt.Fprintf(w, "data: %s\n\n", hello)
	http.NewResponseController(w).Flush()

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-p.done:
			return
		case b := <-p.ch:
			fmt.Fprintf(w, "data: %s\n\n", b)
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		}
		if http.NewResponseController(w).Flush() != nil {
			return
		}
	}
}

// clientIP trusts X-Forwarded-For only from a local reverse proxy, and takes the
// last entry, which is the one the proxy appended.
func clientIP(r *http.Request) string {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if v := r.Header.Values("X-Forwarded-For"); len(v) > 0 {
			last := v[len(v)-1]
			return strings.TrimSpace(last[strings.LastIndex(last, ",")+1:])
		}
	}
	return host
}

// lab takes a NAT measurement from /lab, adds what our STUN servers saw from the same public IPs,
// logs it as one JSON line (journalctl -u hi | grep lab) and returns the server's view to the page.
func lab(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	mu.Lock()
	joins[ip]++
	slow := joins[ip] > maxJoinsPerMin
	mu.Unlock()
	if slow {
		http.Error(w, "slow down", 429)
		return
	}
	var m struct {
		IPs []string `json:"ips"`
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil || json.Unmarshal(raw, &m) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	ips := map[string]bool{ip: true}
	for _, a := range m.IPs {
		if net.ParseIP(a) != nil {
			ips[a] = true
		}
	}
	hits := stunHits(ips, 3*time.Minute)
	b, _ := json.Marshal(map[string]any{"from": ip, "client": json.RawMessage(raw), "server": hits})
	log.Printf("lab %s", b)
	json.NewEncoder(w).Encode(hits)
}

// diag logs a call report from a client opened with ?diag (journalctl -u hi | grep diag).
// ponytail: any room member can write to the log, 32KB a time; rate limit per token if anyone abuses it
func diag(w http.ResponseWriter, r *http.Request) {
	var m struct {
		Token string
		D     json.RawMessage
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&m) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	mu.Lock()
	p := tokens[m.Token]
	mu.Unlock()
	if p == nil {
		http.Error(w, "unknown token", 403)
		return
	}
	log.Printf("diag %s %s %s %s", p.room, p.id, clientIP(r), m.D)
	w.WriteHeader(204)
}

// leave is the page's beacon on unload: a reload after switching networks leaves the old stream on a dead link,
// which the server would only notice minutes later, so the others would keep trying to reach a ghost.
func leave(w http.ResponseWriter, r *http.Request) {
	tok, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 64))
	mu.Lock()
	if p := tokens[string(tok)]; p != nil {
		delete(rooms[p.room], p.id) // so a reconnect can't close done again; events announces the leave
		delete(tokens, p.token)
		if len(rooms[p.room]) == 0 {
			delete(rooms, p.room)
		}
		close(p.done)
	}
	mu.Unlock()
	w.WriteHeader(204)
}

// signal relays an opaque blob (SDP, ICE, or chat for peers that couldn't hole punch)
// to another peer in the sender's room. Nothing is stored.
func signal(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var m struct {
		Token, To string
		Data      json.RawMessage
	}
	if json.NewDecoder(r.Body).Decode(&m) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	mu.Lock()
	defer mu.Unlock()
	from := tokens[m.Token]
	if from == nil {
		http.Error(w, "unknown token", 403)
		return
	}
	to := rooms[from.room][m.To]
	if to == nil {
		http.Error(w, "no such peer", 404)
		return
	}
	send(to, map[string]any{"t": "signal", "from": from.id, "data": m.Data})
	w.WriteHeader(204)
}

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	stun := flag.String("stun", ":3478", "STUN UDP listen address; the next 3 ports too, so clients can measure NATs that pick a new port per destination")
	cert := flag.String("cert", "", "TLS cert file (browsers need HTTPS for camera/mic except on localhost)")
	key := flag.String("key", "", "TLS key file")
	turnAddr := flag.String("turn", ":3482", "TURN UDP listen address (empty disables the relay); relay ports 50000-50199 must be reachable too")
	turnIP := flag.String("turn-ip", "", "public IP of the relay ports (default: the address we reach the internet from)")
	flag.Parse()

	host, port, err := net.SplitHostPort(*stun)
	p, _ := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65532 {
		log.Fatal("bad -stun address: ", *stun)
	}
	for k := range 4 {
		go func() {
			log.Print("STUN stopped, calls across NATs won't connect: ", stunServe(net.JoinHostPort(host, strconv.Itoa(p+k))))
		}()
	}
	if *turnAddr != "" {
		if err := turnServe(*turnAddr, *turnIP); err != nil {
			log.Print("TURN disabled, calls that can't hole punch get chat only: ", err)
		}
	}
	go func() {
		for range time.Tick(time.Minute) {
			mu.Lock()
			clear(joins)
			clear(creds)
			mu.Unlock()
		}
	}()

	serve := func(page []byte) http.HandlerFunc {
		html := bytes.ReplaceAll(page, []byte("{{STUN_PORT}}"), []byte(port))
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(html)
		}
	}

	http.HandleFunc("GET /{$}", serve(page))
	http.HandleFunc("GET /{code}", serve(page))
	http.HandleFunc("GET /lab", serve(labPage))
	http.HandleFunc("POST /api/lab", lab)
	http.HandleFunc("GET /api/{code}/events", events)
	http.HandleFunc("POST /api/signal", signal)
	http.HandleFunc("POST /api/leave", leave)
	http.HandleFunc("POST /api/turn", turnCreds)
	http.HandleFunc("POST /api/diag", diag)

	log.Printf("http %s, stun udp %s to port %d", *addr, *stun, p+3)
	srv := &http.Server{Addr: *addr, ReadHeaderTimeout: 10 * time.Second} // no WriteTimeout: SSE streams are long-lived
	if *cert != "" {
		log.Fatal(srv.ListenAndServeTLS(*cert, *key))
	}
	log.Fatal(srv.ListenAndServe())
}
