// Package mesh — peer table: virtual-IP -> public UDP endpoint mapping.
package mesh

import (
	"net"
	"sync"
	"time"
)

// Peer is one remote player in the 10.242.0.0/16 mesh.
// A peer may have several endpoint CANDIDATES (ICE-lite): a LAN address
// from broadcast discovery plus a public address from signaling/STUN.
// Direct LAN candidates always win as primary; ping/punch try all of them.
type Peer struct {
	NodeID   string
	VIP      net.IP
	Primary  *net.UDPAddr            // best-known endpoint
	Cands    map[string]*net.UDPAddr // every known endpoint: "ip:port" -> addr
	CandSrc  map[string]string       // "lan" | "signal" | "mesh" per candidate
	LastSeen time.Time
	RTTms    float64
}

type Table struct {
	mu    sync.RWMutex
	peers map[string]*Peer // key: vip.String()
}

func NewTable() *Table { return &Table{peers: map[string]*Peer{}} }

// Learn inserts or refreshes a peer candidate. Returns false if this VIP is ours.
func (t *Table) Learn(selfVIP net.IP, p *Peer) bool {
	if p.VIP.Equal(selfVIP) {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	k := p.VIP.String()
	old, ok := t.peers[k]
	if !ok {
		if p.Cands == nil {
			p.Cands = map[string]*net.UDPAddr{}
			p.CandSrc = map[string]string{}
		}
		if p.Primary != nil {
			p.Cands[p.Primary.String()] = p.Primary
			if src, ok := p.CandSrc[p.Primary.String()]; !ok || src == "" {
				p.CandSrc[p.Primary.String()] = "mesh"
			}
		}
		p.LastSeen = time.Now()
		t.peers[k] = p
		return true
	}
	if p.NodeID != "" {
		old.NodeID = p.NodeID
	}
	old.LastSeen = time.Now()
	if p.Primary != nil {
		ck := p.Primary.String()
		src := ""
		for s := range p.CandSrc {
			src = p.CandSrc[s]
			break
		}
		if old.Cands == nil {
			old.Cands = map[string]*net.UDPAddr{}
			old.CandSrc = map[string]string{}
		}
		old.Cands[ck] = p.Primary
		if src != "" {
			old.CandSrc[ck] = src
		}
		// LAN/direct candidates always win; newest of the same class wins.
		if isLAN(p.Primary.IP.String()) && !isLANAddr(old.Primary) {
			old.Primary = p.Primary
		} else if isLAN(p.Primary.IP.String()) == isLANAddr(old.Primary) {
			old.Primary = p.Primary
		}
	}
	if p.RTTms != 0 {
		old.RTTms = p.RTTms
	}
	return true
}

// Candidates returns primary first, then the rest (happy-eyeballs order).
func (p *Peer) Candidates() []*net.UDPAddr {
	out := []*net.UDPAddr{}
	if p.Primary != nil {
		out = append(out, p.Primary)
	}
	for k, a := range p.Cands {
		if p.Primary != nil && k == p.Primary.String() {
			continue
		}
		out = append(out, a)
	}
	return out
}

func isLANAddr(a *net.UDPAddr) bool {
	if a == nil {
		return false
	}
	return isLAN(a.IP.String())
}

func isLAN(ip string) bool {
	for _, p := range []string{"10.", "192.168.", "172.16.", "127."} {
		if len(ip) >= len(p) && ip[:len(p)] == p {
			return true
		}
	}
	return false
}

func (t *Table) Get(vip net.IP) (*Peer, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	p, ok := t.peers[vip.String()]
	return p, ok
}

func (t *Table) All() []*Peer {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]*Peer, 0, len(t.peers))
	for _, p := range t.peers {
		out = append(out, p)
	}
	return out
}

// Prune drops peers silent for longer than maxAge.
func (t *Table) Prune(maxAge time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k, p := range t.peers {
		if time.Since(p.LastSeen) > maxAge {
			delete(t.peers, k)
		}
	}
}
