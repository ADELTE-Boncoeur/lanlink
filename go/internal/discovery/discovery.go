// Package discovery implements Part C: local peer discovery.
//
// Two mechanisms, both stdlib-only:
//
//  1. UDP broadcast (primary, works everywhere incl. Windows Wi-Fi):
//     every node broadcasts a JSON hello on 255.255.255.255:32442
//     every 3s and listens on 0.0.0.0:32442. Any hello with a
//     different node_id is a LAN peer — no config needed.
//
//  2. mDNS hook: the hello already carries everything an mDNS TXT
//     record would (node id, VIP, mesh port, room). If you later add
//     an mDNS responder (e.g. _lanlink._udp.local), publish the same
//     fields as TXT and keep this broadcast path as fallback for
//     networks that filter multicast.
//
// Hello schema:
//	{"magic":"LLNK","node_id":"...","vip":"10.242.x.y",
//	 "mesh_port":32443,"room":"HALO-42","ts":1699999999}
package discovery

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

const (
	BroadcastPort = 32442
	Magic         = "LLNK"
	Interval      = 3 * time.Second
)

// Hello is the presence announcement.
type Hello struct {
	Magic    string `json:"magic"`
	NodeID   string `json:"node_id"`
	VIP      string `json:"vip"`
	MeshPort int    `json:"mesh_port"`
	Room     string `json:"room"`
	Ts       int64  `json:"ts"`
}

// NodeID generates a short random id like "pc-a3f9".
func NodeID() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("pc-%02x%02x%02x", b[0], b[1], b[2])
}

// AllocVIP deterministically maps nodeID -> 10.242.h1.h2 (avoids .0/.1/.255).
// salt bumps on conflict (another node claims the same VIP).
func AllocVIP(nodeID string, salt int) net.IP {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s#%d", nodeID, salt)))
	h1 := h[0]
	if h1 == 0 || h1 == 255 {
		h1 = 7
	}
	h2 := h[1]
	if h2 == 0 || h2 == 1 || h2 == 255 {
		h2 = binary.BigEndian.Uint16(h[2:4])%254 + 2 // 2..255, never .0/.1
		if h2 > 254 {
			h2 = 100
		}
	}
	return net.IPv4(10, 242, h1, byte(h2))
}

// Broadcaster sends hellos and invokes onPeer for every foreign hello.
type Broadcaster struct {
	Hello  Hello
	onPeer func(h Hello, from *net.UDPAddr)
	conn   *net.UDPConn
	quit   chan struct{}
}

// Start begins broadcast + listen loops. Non-blocking.
func Start(hello Hello, onPeer func(h Hello, from *net.UDPAddr)) (*Broadcaster, error) {
	laddr := &net.UDPAddr{IP: net.IPv4zero, Port: BroadcastPort}
	conn, err := net.ListenUDP("udp4", laddr)
	if err != nil {
		return nil, err
	}
	b := &Broadcaster{Hello: hello, onPeer: onPeer, conn: conn, quit: make(chan struct{})}
	go b.sendLoop()
	go b.recvLoop()
	return b, nil
}

func (b *Broadcaster) sendLoop() {
	bcast := &net.UDPAddr{IP: net.IPv4bcast, Port: BroadcastPort}
	t := time.NewTicker(Interval)
	defer t.Stop()
	_ = b.sendOnce(bcast) // immediate announce (game hosts appear instantly)
	for {
		select {
		case <-b.quit:
			return
		case <-t.C:
			_ = b.sendOnce(bcast)
		}
	}
}

func (b *Broadcaster) sendOnce(dst *net.UDPAddr) error {
	b.Hello.Ts = time.Now().Unix()
	b.Hello.Magic = Magic
	raw, _ := json.Marshal(b.Hello)
	c, err := net.DialUDP("udp4", nil, dst)
	if err != nil {
		return err
	}
	defer c.Close()
	_, err = c.Write(raw)
	return err
}

func (b *Broadcaster) recvLoop() {
	buf := make([]byte, 2048)
	for {
		_ = b.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, from, err := b.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-b.quit:
				return
			default:
				continue
			}
		}
		var h Hello
		if err := json.Unmarshal(buf[:n], &h); err != nil {
			continue
		}
		if h.Magic != Magic || h.NodeID == "" || h.NodeID == b.Hello.NodeID {
			continue
		}
		if b.onPeer != nil {
			b.onPeer(h, from)
		}
	}
}

// Close stops discovery.
func (b *Broadcaster) Close() error {
	select {
	case <-b.quit:
	default:
		close(b.quit)
	}
	return b.conn.Close()
}
