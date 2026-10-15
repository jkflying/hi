package main

import (
	"encoding/binary"
	"net"
	"testing"
)

func TestStunReply(t *testing.T) {
	req := make([]byte, 20)
	binary.BigEndian.PutUint16(req, 0x0001)
	binary.BigEndian.PutUint32(req[4:], stunCookie)
	copy(req[8:], "txn-id-12345")

	for _, from := range []*net.UDPAddr{
		{IP: net.ParseIP("203.0.113.7"), Port: 54321},
		{IP: net.ParseIP("2001:db8::42"), Port: 1234},
	} {
		resp := stunReply(req, from)
		if binary.BigEndian.Uint16(resp) != 0x0101 || string(resp[8:20]) != "txn-id-12345" {
			t.Fatalf("bad header %x", resp[:20])
		}
		a := resp[20:]
		port := int(binary.BigEndian.Uint16(a[6:]) ^ (stunCookie >> 16))
		ip := make(net.IP, len(a)-8)
		for i := range ip {
			ip[i] = a[8+i] ^ resp[4+i]
		}
		if port != from.Port || !ip.Equal(from.IP) {
			t.Fatalf("got %v:%d want %v", ip, port, from)
		}
	}

	if stunReply([]byte("not stun at all, nope"), &net.UDPAddr{}) != nil {
		t.Fatal("replied to garbage")
	}
}
