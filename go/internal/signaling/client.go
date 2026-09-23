// Package signaling — room-code client + STUN + UDP hole punching (stdlib only).
//
// Flow for two players behind NATs sharing room code "HALO-42":
//  1. Each node learns its public endpoint via STUN (or falls back to LAN IP).
//  2. Each node HTTP-POSTs {room, node_id, vip, mesh_port, public} to the
//     signaling server every 10s (heartbeat) — server remembers RemoteAddr.
//  3. Each node HTTP-GETs /room/peers?room=HALO-42, learns the other's
//     public endpoint, and both start sending PUNCH frames to each other.
//     The first outbound packet opens the NAT mapping; once both sides
//     have punched, direct P2P flows with no relay.
//  4. DATA/PING/PONG frames then travel directly over UDP.
package signaling

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"time"
)

// PeerInfo mirrors the server's record for one room member.
type PeerInfo struct {
	NodeID   string `json:"node_id"`
	VIP      string `json:"vip"`
	Endpoint string `json:"endpoint"` // "ip:port" as seen by the server
	Room     string `json:"room"`
	Ts       int64  `json:"ts"`
}

// Client polls the coordination server.
type Client struct {
	Server   string // e.g. http://signal.example.com:32440
	Room     string
	NodeID   string
	VIP      net.IP
	MeshPort int
	http     *http.Client
}

func NewClient(server, room, nodeID string, vip net.IP, meshPort int) *Client {
	return &Client{
		Server: server, Room: room, NodeID: nodeID, VIP: vip, MeshPort: meshPort,
		http: &http.Client{Timeout: 8 * time.Second},
	}
}

// Heartbeat announces us; server replies with the current member list.
func (c *Client) Heartbeat(public string) ([]PeerInfo, error) {
	body, _ := json.Marshal(map[string]any{
		"room": c.Room, "node_id": c.NodeID,
		"vip": c.VIP.String(), "mesh_port": c.MeshPort, "endpoint": public,
	})
	resp, err := c.http.Post(c.Server+"/room/join", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		Peers []PeerInfo `json:"peers"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out.Peers, nil
}

// FetchPeers is a read-only poll (used between heartbeats).
func (c *Client) FetchPeers() ([]PeerInfo, error) {
	resp, err := c.http.Get(c.Server + "/room/peers?room=" + c.Room)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		Peers []PeerInfo `json:"peers"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out.Peers, nil
}

// ---------------------------------------------------------------------------
// Minimal STUN binding request (RFC 5389) — no external deps.
// ---------------------------------------------------------------------------

// BuildStunRequest creates a binding request; returns wire bytes + txn id.
// Send it FROM the mesh socket so the reply reveals the mesh port's true
// public mapping; demux replies in the mesh receive loop via IsStunResponse.
func BuildStunRequest() (wire []byte, txn [12]byte) {
	var req [20]byte
	binary.BigEndian.PutUint16(req[0:2], 0x0001) // Binding Request
	binary.BigEndian.PutUint16(req[2:4], 0)      // length
	binary.BigEndian.PutUint32(req[4:8], 0x2112A442)
	for i := range txn {
		txn[i] = byte(rand.Intn(256))
	}
	copy(req[8:20], txn[:])
	return req[:], txn
}

// IsStunResponse reports whether dg looks like a STUN binding response
// for our outstanding transaction (mesh frames start with "LLNK", never 0x0101).
func IsStunResponse(dg []byte, txn [12]byte) bool {
	return len(dg) >= 20 && dg[0] == 0x01 && dg[1] == 0x01 &&
		string(dg[8:20]) == string(txn[:])
}

// ParseStunResponse extracts "ip:port" from a binding response.
func ParseStunResponse(dg []byte) (string, error) {
	if len(dg) < 20 || dg[0] != 0x01 || dg[1] != 0x01 {
		return "", fmt.Errorf("signaling: not a STUN response")
	}
	cookie := binary.BigEndian.Uint32(dg[4:8])
	off := 20
	for off+4 <= len(dg) {
		typ := binary.BigEndian.Uint16(dg[off : off+2])
		ln := int(binary.BigEndian.Uint16(dg[off+2 : off+4]))
		val := dg[off+4:]
		if typ == 0x0020 && len(val) >= ln && ln >= 8 {
			family := val[1]
			if family == 0x01 {
				xport := binary.BigEndian.Uint16(val[2:4]) ^ uint16(cookie>>16)
				xip := binary.BigEndian.Uint32(val[4:8]) ^ cookie
				ip := net.IPv4(byte(xip>>24), byte(xip>>16), byte(xip>>8), byte(xip))
				return fmt.Sprintf("%s:%d", ip.String(), xport), nil
			}
		}
		off += 4 + ((ln + 3) &^ 3)
	}
	return "", fmt.Errorf("signaling: no XOR-MAPPED-ADDRESS in STUN reply")
}

// PublicEndpoint asks a STUN server for our public ip:port.
func PublicEndpoint(stunServer string) (string, error) {
	raddr, err := net.ResolveUDPAddr("udp4", stunServer)
	if err != nil {
		return "", err
	}
	conn, err := net.DialUDP("udp4", nil, raddr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))

	var req [20]byte
	binary.BigEndian.PutUint16(req[0:2], 0x0001) // Binding Request
	binary.BigEndian.PutUint16(req[2:4], 0)      // length
	binary.BigEndian.PutUint32(req[4:8], 0x2112A442)
	for i := 8; i < 20; i++ {
		req[i] = byte(rand.Intn(256))
	}
	if _, err := conn.Write(req[:]); err != nil {
		return "", err
	}
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		return "", err
	}
	// Parse XOR-MAPPED-ADDRESS (type 0x0020).
	cookie := binary.BigEndian.Uint32(buf[4:8])
	off := 20
	for off+4 <= n {
		typ := binary.BigEndian.Uint16(buf[off : off+2])
		ln := int(binary.BigEndian.Uint16(buf[off+2 : off+4]))
		val := buf[off+4:]
		if typ == 0x0020 && len(val) >= ln && ln >= 8 {
			family := val[1]
			if family == 0x01 {
				xport := binary.BigEndian.Uint16(val[2:4]) ^ uint16(cookie>>16)
				xip := binary.BigEndian.Uint32(val[4:8]) ^ cookie
				ip := net.IPv4(byte(xip>>24), byte(xip>>16), byte(xip>>8), byte(xip))
				return fmt.Sprintf("%s:%d", ip.String(), xport), nil
			}
		}
		off += 4 + ((ln + 3) &^ 3)
	}
	return "", fmt.Errorf("signaling: no XOR-MAPPED-ADDRESS in STUN reply")
}
