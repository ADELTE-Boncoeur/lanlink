// Package mesh implements Part D: minimal-overhead UDP encapsulation.
//
// Wire format (shared with py/lanlink.py, interoperable):
//
//	UDP payload = HEADER(20) || CIPHERTEXT(n) || TAG(8)   // 28 bytes overhead
//	HEADER = MAGIC u32be 0x4C4C4E4B | VER u8=1 | TYPE u8 | SRC u32be | DST u32be | SEQ u32be | LEN u16be
//	TYPE: 0x01 DATA (raw IP packet from TUN)  0x02 PING  0x03 PONG  0x04 PUNCH
//	      0x05 LOBBY (JSON game-server announcement {title,node,vip})
//
// Crypto (stdlib-only, low latency, portable):
//	key      = SHA256(strings.ToLower(roomCode))
//	nonce    = SEQ(4) || SRC_VIP(4)
//	ks block = SHA256(key || nonce || counter_be32)  // CTR-style XOR stream
//	ct       = pt XOR keystream
//	tag      = HMAC-SHA256(key, HEADER || ct)[:8]
//
// Production hardening path: swap Seal/Open for ChaCha20-Poly1305
// (golang.org/x/crypto) keeping the same header — the header already
// carries everything a real AEAD needs (nonce = seq||src).
package mesh

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"net"
)

const (
	Magic   uint32 = 0x4C4C4E4B // "LLNK"
	Version uint8  = 1

	TypeData  uint8 = 0x01
	TypePing  uint8 = 0x02
	TypePong  uint8 = 0x03
	TypePunch uint8 = 0x04
	TypeLobby uint8 = 0x05

	HeaderLen = 20
	TagLen    = 8
	MaxMTU    = 1400 // keep under typical 1500 MTU minus header/tag/UDP/IP
)

var ErrIntegrity = errors.New("mesh: integrity tag mismatch")
var ErrMalformed = errors.New("mesh: malformed frame")

type Header struct {
	Type uint8
	Src  net.IP // v4
	Dst  net.IP
	Seq  uint32
}

// RoomKey derives the pre-shared room key.
func RoomKey(room string) []byte {
	lower := ""
	for _, r := range room {
		if r >= 'A' && r <= 'Z' {
			lower += string(r + 32)
		} else {
			lower += string(r)
		}
	}
	if lower == "" {
		lower = "public-lobby"
	}
	sum := sha256.Sum256([]byte("lanlink-v1:" + lower))
	return sum[:]
}

func ip4(ip net.IP) [4]byte {
	v4 := ip.To4()
	var b [4]byte
	copy(b[:], v4)
	return b
}

// Seal wraps a raw IP packet (or PING payload) for one UDP datagram.
func Seal(roomKey, srcVIP, dstVIP net.IP, ptype uint8, seq uint32, plaintext []byte) []byte {
	hdr := make([]byte, HeaderLen)
	binary.BigEndian.PutUint32(hdr[0:4], Magic)
	hdr[4] = Version
	hdr[5] = ptype
	copy(hdr[6:10], ip4(srcVIP)[:])
	copy(hdr[10:14], ip4(dstVIP)[:])
	binary.BigEndian.PutUint32(hdr[14:18], seq)
	binary.BigEndian.PutUint16(hdr[18:20], uint16(len(plaintext)))

	ct := xorStream(roomKey, ip4(srcVIP), seq, plaintext)

	mac := hmac.New(sha256.New, roomKey)
	mac.Write(hdr)
	mac.Write(ct)
	tag := mac.Sum(nil)[:TagLen]

	out := make([]byte, 0, HeaderLen+len(ct)+TagLen)
	out = append(out, hdr...)
	out = append(out, ct...)
	out = append(out, tag...)
	return out
}

// Open verifies + decrypts one UDP datagram. Returns header + plaintext.
func Open(roomKey []byte, datagram []byte) (Header, []byte, error) {
	if len(datagram) < HeaderLen+TagLen {
		return Header{}, nil, ErrMalformed
	}
	hdr := datagram[:HeaderLen]
	if binary.BigEndian.Uint32(hdr[0:4]) != Magic || hdr[4] != Version {
		return Header{}, nil, ErrMalformed
	}
	ctLen := int(binary.BigEndian.Uint16(hdr[18:20]))
	if len(datagram) != HeaderLen+ctLen+TagLen {
		return Header{}, nil, ErrMalformed
	}
	ct := datagram[HeaderLen : HeaderLen+ctLen]
	wantTag := datagram[HeaderLen+ctLen:]

	mac := hmac.New(sha256.New, roomKey)
	mac.Write(hdr)
	mac.Write(ct)
	if !hmac.Equal(mac.Sum(nil)[:TagLen], wantTag) {
		return Header{}, nil, ErrIntegrity
	}
	var h Header
	h.Type = hdr[5]
	h.Src = net.IPv4(hdr[6], hdr[7], hdr[8], hdr[9])
	h.Dst = net.IPv4(hdr[10], hdr[11], hdr[12], hdr[13])
	h.Seq = binary.BigEndian.Uint32(hdr[14:18])

	pt := xorStream(roomKey, ip4(h.Src), h.Seq, ct)
	return h, pt, nil
}

// xorStream builds a CTR-style keystream from SHA256 (stdlib-only).
func xorStream(key []byte, src [4]byte, seq uint32, data []byte) []byte {
	out := make([]byte, len(data))
	var nonce [8]byte
	binary.BigEndian.PutUint32(nonce[0:4], seq)
	copy(nonce[4:8], src[:])
	var ctr uint32
	for off := 0; off < len(data); {
		h := sha256.New()
		h.Write(key)
		h.Write(nonce[:])
		var cb [4]byte
		binary.BigEndian.PutUint32(cb[:], ctr)
		h.Write(cb[:])
		ks := h.Sum(nil) // 32 bytes
		for i := 0; i < len(ks) && off < len(data); i, off = i+1, off+1 {
			out[off] = data[off] ^ ks[i]
		}
		ctr++
	}
	return out
}
