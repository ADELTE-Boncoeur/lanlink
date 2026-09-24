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
	Primary  *net.UDPAddr            // best-known endpoint (subnet-aware, see pick)
	Last     *net.UDPAddr            // most recently learned candidate
	Order    []string                // candidate keys in learn order (deterministic)
	Cands    map[string]*net.UDPAddr // every known endpoint: "ip:port" -> addr
	CandSrc  map[string]string       // "lan" | "signal" | "mesh" per candidate
	Room     string                  // last advertised room code ("" = unknown)
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
			p.Order = append(p.Order, p.Primary.String())
			p.Last = p.Primary
			p.Primary = pickPrimary(p)
		}
		p.LastSeen = time.Now()
		t.peers[k] = p
		return true
	}
	if p.NodeID != "" {
		old.NodeID = p.NodeID
	}
	if p.Room != "" {
		old.Room = p.Room
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
		if _, dup := old.Cands[ck]; !dup {
			old.Order = append(old.Order, ck)
		}
		old.Cands[ck] = p.Primary
		if src != "" {
			old.CandSrc[ck] = src
		}
		old.Last = p.Primary
		old.Primary = pickPrimary(old)
	}
	if p.RTTms != 0 {
		old.RTTms = p.RTTms
	}
	return true
}

// pickPrimary prefers a candidate on OUR subnet (fixes multi-homed PCs that
// announce an unreachable hotspot/VM address), then any LAN address, then
// anything. Newest-learned wins inside the best class.
func pickPrimary(p *Peer) *net.UDPAddr {
	ordered := []*net.UDPAddr{}
	for _, k := range p.Order {
		if a, ok := p.Cands[k]; ok {
			ordered = append(ordered, a)
		}
	}
	for _, a := range p.Cands {
		found := false
		for _, b := range ordered {
			if a.String() == b.String() {
				found = true
				break
			}
		}
		if !found {
			ordered = append(ordered, a)
		}
	}
	nets := ourSubnets()
	var same, lan []*net.UDPAddr
	for _, a := range ordered {
		if nets[net24(a.IP.String())] {
			same = append(same, a)
		} else if isLAN(a.IP.String()) {
			lan = append(lan, a)
		}
	}
	for _, pool := range [][]*net.UDPAddr{same, lan, ordered} {
		if len(pool) == 0 {
			continue
		}
		if p.Last != nil {
			for _, a := range pool {
				if a.String() == p.Last.String() {
					return a
				}
			}
		}
		return pool[len(pool)-1]
	}
	return p.Primary
}

var (
	subnetCache     map[string]bool
	subnetCacheTime time.Time
	subnetCacheMu   sync.Mutex
)

// ourSubnets returns our /24s (cached 60s). Offline-safe.
func ourSubnets() map[string]bool {
	subnetCacheMu.Lock()
	defer subnetCacheMu.Unlock()
	if subnetCache != nil && time.Since(subnetCacheTime) < time.Minute {
		return subnetCache
	}
	out := map[string]bool{}
	if ifs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range ifs {
			if ipnet, ok := a.(*net.IPNet); ok {
				if v4 := ipnet.IP.To4(); v4 != nil && !v4.IsLoopback() {
					out[net24(v4.String())] = true
				}
			}
		}
	}
	subnetCache = out
	subnetCacheTime = time.Now()
	return out
}

func net24(ip string) string {
	parts := splitIP(ip)
	if len(parts) != 4 {
		return ""
	}
	return parts[0] + "." + parts[1] + "." + parts[2]
}

func splitIP(ip string) []string {
	var out []string
	cur := ""
	for i := 0; i < len(ip); i++ {
		if ip[i] == '.' {
			out = append(out, cur)
			cur = ""
		} else {
			cur += string(ip[i])
		}
	}
	return append(out, cur)
}

// SameLAN reports whether ip shares one of our /24 subnets.
func SameLAN(ip string) bool {
	n := net24(ip)
	return n != "" && ourSubnets()[n]
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
	for _, p := range []string{"10.", "192.168.", "172.16.", "127.", "169.254."} {
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
