package main

import (
	"encoding/binary"
	"net"
	"sync"
	"time"
)

const stunCookie = 0x2112A442

// stunServe answers STUN Binding requests (RFC 5389) with the sender's
// public address. That is all a browser needs for srflx candidates.
func stunServe(addr string) error {
	c, err := net.ListenPacket("udp", addr) // "udp" = dual-stack, so IPv6 peers get srflx too
	if err != nil {
		return err
	}
	dst := c.LocalAddr().(*net.UDPAddr).Port
	buf := make([]byte, 1500)
	for {
		n, from, err := c.ReadFrom(buf)
		if err != nil {
			return err
		}
		if resp := stunReply(buf[:n], from.(*net.UDPAddr)); resp != nil {
			c.WriteTo(resp, from)
			stunRecord(from.(*net.UDPAddr), dst)
		}
	}
}

// Recent Binding requests, so /lab can show the order in which a NAT handed out ports.
type stunHit struct {
	T    int64  `json:"t"` // unix ms
	IP   string `json:"ip"`
	Port int    `json:"port"`
	Dst  int    `json:"dst"`
}

var (
	stunMu  sync.Mutex
	stunLog []stunHit // ponytail: in-memory, trimmed by half at 50k; 3 MB worst case
)

func stunRecord(from *net.UDPAddr, dst int) {
	stunMu.Lock()
	defer stunMu.Unlock()
	if len(stunLog) >= 50000 {
		stunLog = append(stunLog[:0], stunLog[25000:]...)
	}
	stunLog = append(stunLog, stunHit{time.Now().UnixMilli(), from.IP.String(), from.Port, dst})
}

// stunHits returns the requests from ips in the last window.
func stunHits(ips map[string]bool, window time.Duration) []stunHit {
	since := time.Now().Add(-window).UnixMilli()
	stunMu.Lock()
	defer stunMu.Unlock()
	out := []stunHit{}
	for _, h := range stunLog {
		if h.T >= since && ips[h.IP] {
			out = append(out, h)
		}
	}
	return out
}

// stunReply returns a Binding success response carrying XOR-MAPPED-ADDRESS,
// or nil if req is not a Binding request.
func stunReply(req []byte, from *net.UDPAddr) []byte {
	if len(req) < 20 || binary.BigEndian.Uint16(req) != 0x0001 || binary.BigEndian.Uint32(req[4:]) != stunCookie {
		return nil
	}
	ip, family := from.IP.To4(), byte(1)
	if ip == nil {
		ip, family = from.IP.To16(), 2
	}
	attr := make([]byte, 8+len(ip))
	binary.BigEndian.PutUint16(attr, 0x0020) // XOR-MAPPED-ADDRESS
	binary.BigEndian.PutUint16(attr[2:], uint16(4+len(ip)))
	attr[5] = family
	binary.BigEndian.PutUint16(attr[6:], uint16(from.Port)^(stunCookie>>16))
	for i := range ip { // XOR key is cookie followed by transaction ID
		attr[8+i] = ip[i] ^ req[4+i]
	}
	resp := make([]byte, 20, 20+len(attr))
	binary.BigEndian.PutUint16(resp, 0x0101)
	binary.BigEndian.PutUint16(resp[2:], uint16(len(attr)))
	copy(resp[4:20], req[4:20])
	return append(resp, attr...)
}
