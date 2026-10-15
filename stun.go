package main

import (
	"encoding/binary"
	"net"
)

const stunCookie = 0x2112A442

// stunServe answers STUN Binding requests (RFC 5389) with the sender's
// public address. That is all a browser needs for srflx candidates.
func stunServe(addr string) error {
	c, err := net.ListenPacket("udp", addr) // "udp" = dual-stack, so IPv6 peers get srflx too
	if err != nil {
		return err
	}
	buf := make([]byte, 1500)
	for {
		n, from, err := c.ReadFrom(buf)
		if err != nil {
			return err
		}
		if resp := stunReply(buf[:n], from.(*net.UDPAddr)); resp != nil {
			c.WriteTo(resp, from)
		}
	}
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
