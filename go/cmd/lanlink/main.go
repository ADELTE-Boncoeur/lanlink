// Command lanlink — one portable node of the virtual LAN.
//
//	lanlink.exe -name Player1 [-mesh-port 32443] [-ui-port 32441]
//	            [-room HALO-42] [-signal http://host:32440] [-tun]
//
// What it does:
//  1. Picks a virtual IP in 10.242.0.0/16 (Part A/B).
//  2. Opens the TUN adapter (real with -tun+Admin, else userspace stub).
//  3. Broadcasts/listens for LAN peers (Part C).
//  4. Joins the room-code signaling server + hole-punches (NAT bypass).
//  5. Forwards raw IP packets peer->peer over encrypted UDP (Part D).
//  6. Serves the lightweight UI on http://127.0.0.1:32441 (no Electron).
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"lanlink/internal/discovery"
	"lanlink/internal/mesh"
	"lanlink/internal/signaling"
	"lanlink/internal/tun"
)

type node struct {
	nodeID   string
	vip      net.IP
	room     string
	roomKey  []byte
	meshConn *net.UDPConn
	peers    *mesh.Table
	tunif    tun.Interface
	seq      uint32
	seqMu    sync.Mutex
	pending  map[uint32]time.Time
	pendMu   sync.Mutex
	signal   *signaling.Client
	publicEP string
	stunTxn  [12]byte
	stunMu   sync.Mutex
	serve    string // announced game-server title ("" = not hosting)
	games    map[string]gameEntry
	gamesMu  sync.Mutex
}

// gameEntry is one hosted game seen on the mesh.
type gameEntry struct {
	Title  string
	NodeID string
	Seen   time.Time
}

func main() {
	name := flag.String("name", "", "player name (default: random node id)")
	meshPort := flag.Int("mesh-port", 32443, "UDP port for P2P mesh")
	uiPort := flag.Int("ui-port", 32441, "HTTP port for local UI")
	room := flag.String("room", "public-lobby", "room code for internet play")
	signalURL := flag.String("signal", "", "signaling server URL, e.g. http://host:32440 (empty = LAN only)")
	useTun := flag.Bool("tun", false, "attach real TUN adapter (needs Admin + wintun/tap driver)")
	stun := flag.String("stun", "stun.l.google.com:19302", "STUN server for public endpoint (empty = skip)")
	serve := flag.String("serve", "", "announce a hosted game, e.g. -serve \"CoD4 mp_shipment\"")
	flag.Parse()

	nodeID := *name
	if nodeID == "" {
		nodeID = discovery.NodeID()
	}
	vip := discovery.AllocVIP(nodeID, 0)

	tunif, err := tun.New(tun.Config{VIP: vip, MTU: 1400, UseReal: *useTun, IfName: "lanlink"})
	if err != nil {
		log.Fatalf("tun: %v", err)
	}
	defer tunif.Close()

	laddr, _ := net.ResolveUDPAddr("udp4", fmt.Sprintf("0.0.0.0:%d", *meshPort))
	mconn, err := net.ListenUDP("udp4", laddr)
	if err != nil {
		log.Fatalf("mesh listen: %v", err)
	}
	defer mconn.Close()

	n := &node{
		nodeID: nodeID, vip: vip, room: *room,
		roomKey:  mesh.RoomKey(*room),
		meshConn: mconn, peers: mesh.NewTable(),
		tunif: tunif, pending: map[uint32]time.Time{},
		serve: *serve, games: map[string]gameEntry{},
	}
	if *signalURL != "" {
		n.signal = signaling.NewClient(*signalURL, *room, nodeID, vip, *meshPort)
		go n.refreshPublic(*stun) // STUN on the MESH socket -> true mapping
		go n.signalLoop()
	}

	// Part C: LAN discovery — learn peers, punch immediately.
	discHello := discovery.Hello{NodeID: nodeID, VIP: vip.String(), MeshPort: *meshPort, Room: *room}
	disc, err := discovery.Start(discHello, func(h discovery.Hello, from *net.UDPAddr) {
		rvip := net.ParseIP(h.VIP)
		if rvip == nil {
			return
		}
		ep := &net.UDPAddr{IP: from.IP, Port: h.MeshPort}
		n.peers.Learn(n.vip, &mesh.Peer{NodeID: h.NodeID, VIP: rvip, Primary: ep,
			Cands: map[string]*net.UDPAddr{ep.String(): ep},
			CandSrc: map[string]string{ep.String(): "lan"}, Room: h.Room})
		n.punchAddr(ep) // open NAT/stateful-firewall path back immediately
		log.Printf("LAN peer: %s (%s) via %s room=%s", h.NodeID, h.VIP, ep, h.Room)
	})
	if err != nil {
		log.Fatalf("discovery: %v", err)
	}
	defer disc.Close()

	go n.meshRecvLoop()
	go n.tunReadLoop() // real packets -> mesh (idle in stub mode)
	go n.keepaliveLoop()

	log.Printf("LANLink node %s vip=%s room=%s mesh=:%d ui=http://127.0.0.1:%d",
		nodeID, vip, *room, *meshPort, *uiPort)
	log.Fatal(http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", *uiPort), n.uiMux()))
}

// ---------------------------------------------------------------- mesh I/O

func (n *node) nextSeq() uint32 {
	n.seqMu.Lock()
	defer n.seqMu.Unlock()
	n.seq++
	return n.seq
}

// meshRecvLoop: Part D receive path — verify, decrypt, dispatch.
func (n *node) meshRecvLoop() {
	buf := make([]byte, 2048)
	for {
		n.meshConn.SetReadDeadline(time.Now().Add(10 * time.Second))
		m, from, err := n.meshConn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		// STUN replies arrive on the same socket — demux before mesh.Open.
		n.stunMu.Lock()
		txn := n.stunTxn
		n.stunMu.Unlock()
		var zeroTxn [12]byte
		if txn != zeroTxn && signaling.IsStunResponse(buf[:m], txn) {
			if ep, err := signaling.ParseStunResponse(buf[:m]); err == nil {
				n.publicEP = ep
				log.Printf("STUN mesh public endpoint: %s", ep)
			}
			continue
		}
		h, pt, err := mesh.Open(n.roomKey, buf[:m])
		if err != nil {
			continue // wrong room or corrupted — drop silently (cheap integrity)
		}
		n.peers.Learn(n.vip, &mesh.Peer{VIP: h.Src, Primary: from,
			Cands: map[string]*net.UDPAddr{from.String(): from},
			CandSrc: map[string]string{from.String(): "mesh"}})
		switch h.Type {
		case mesh.TypePing:
			reply := mesh.Seal(n.roomKey, n.vip, h.Src, mesh.TypePong, h.Seq, pt)
			_, _ = n.meshConn.WriteToUDP(reply, from)
		case mesh.TypePong:
			n.pendMu.Lock()
			if t0, ok := n.pending[h.Seq]; ok {
				rtt := float64(time.Since(t0).Microseconds()) / 1000.0
				if p, ok := n.peers.Get(h.Src); ok {
					p.RTTms = rtt
				}
				delete(n.pending, h.Seq)
			}
			n.pendMu.Unlock()
		case mesh.TypeData:
			_ = n.tunif.Write(pt) // inject into OS (stub queues locally)
		case mesh.TypePunch:
			// NAT mapping opened — nothing else needed.
		case mesh.TypeLobby:
			var info struct {
				Title string `json:"title"`
				Node  string `json:"node"`
			}
			if err := json.Unmarshal(pt, &info); err == nil && info.Title != "" {
				if len(info.Title) > 64 {
					info.Title = info.Title[:64]
				}
				n.gamesMu.Lock()
				n.games[h.Src.String()] = gameEntry{Title: info.Title, NodeID: info.Node, Seen: time.Now()}
				n.gamesMu.Unlock()
				log.Printf("lobby: game server '%s' @ %s", info.Title, h.Src)
			}
		}
	}
}

// tunReadLoop: Part D send path — raw packet -> route -> encapsulate -> UDP.
func (n *node) tunReadLoop() {
	for {
		pkt, err := n.tunif.Read()
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if len(pkt) < 20 {
			continue
		}
		dst := net.IPv4(pkt[16], pkt[17], pkt[18], pkt[19])
		if dst.Mask(net.CIDRMask(16, 32)).String() != "10.242.0.0" {
			continue // not our virtual subnet — ignore
		}
		p, ok := n.peers.Get(dst)
		if !ok {
			continue // unknown peer — discovery hasn't seen them yet
		}
		frame := mesh.Seal(n.roomKey, n.vip, dst, mesh.TypeData, n.nextSeq(), pkt)
		for _, ep := range p.Candidates() {
			_, _ = n.meshConn.WriteToUDP(frame, ep)
		}
	}
}

func (n *node) punchAddr(ep *net.UDPAddr) {
	frame := mesh.Seal(n.roomKey, n.vip, n.vip, mesh.TypePunch, n.nextSeq(), []byte("punch"))
	_, _ = n.meshConn.WriteToUDP(frame, ep)
}

// punchAll tries every known candidate (happy-eyeballs hole punching).
func (n *node) punchAll(vip net.IP) {
	if p, ok := n.peers.Get(vip); ok {
		for _, ep := range p.Candidates() {
			n.punchAddr(ep)
		}
	}
}

// gamePorts mirrors py/lanlink.py GAME_PORTS: a taken UDP port means a
// local game server is running -> announced automatically, no typing needed.
var gamePorts = map[int]string{28960: "CoD4", 28961: "CoD MW2", 2302: "Halo", 27015: "Source game"}

func detectLocalGame() string {
	for port, game := range gamePorts {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: port})
		if err != nil {
			return game + " server (auto-detected)"
		}
		c.Close()
	}
	return ""
}

func (n *node) keepaliveLoop() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for range t.C {
		for _, p := range n.peers.All() {
			for _, ep := range p.Candidates() {
				n.punchAddr(ep)
			}
		}
		title := n.serve
		if title == "" {
			title = detectLocalGame()
		}
		if title != "" { // announce our hosted game to the whole mesh
			payload, _ := json.Marshal(map[string]string{
				"title": title, "node": n.nodeID, "vip": n.vip.String()})
			for _, p := range n.peers.All() {
				for _, ep := range p.Candidates() {
					frame := mesh.Seal(n.roomKey, n.vip, p.VIP, mesh.TypeLobby, n.nextSeq(), payload)
					_, _ = n.meshConn.WriteToUDP(frame, ep)
				}
			}
		}
		n.gamesMu.Lock()
		for k, g := range n.games {
			if time.Since(g.Seen) > 40*time.Second {
				delete(n.games, k)
			}
		}
		n.gamesMu.Unlock()
		n.peers.Prune(90 * time.Second)
	}
}

// PingPeer sends an app-level echo and waits for the PONG (for UI + gaming RTT).
func (n *node) PingPeer(vip net.IP, timeout time.Duration) (float64, error) {
	p, ok := n.peers.Get(vip)
	if !ok {
		return 0, fmt.Errorf("unknown peer %s", vip)
	}
	cands := p.Candidates()
	if len(cands) == 0 {
		return 0, fmt.Errorf("no endpoint for %s", vip)
	}
	seq := n.nextSeq()
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(time.Now().UnixNano()))
	frame := mesh.Seal(n.roomKey, n.vip, vip, mesh.TypePing, seq, ts[:])
	n.pendMu.Lock()
	n.pending[seq] = time.Now()
	n.pendMu.Unlock()
	for _, ep := range cands { // happy-eyeballs: try every candidate
		if _, err := n.meshConn.WriteToUDP(frame, ep); err != nil {
			return 0, err
		}
	}
	dead := time.Now().Add(timeout)
	for time.Now().Before(dead) {
		time.Sleep(20 * time.Millisecond)
		n.pendMu.Lock()
		_, waiting := n.pending[seq]
		n.pendMu.Unlock()
		if !waiting {
			if pp, ok := n.peers.Get(vip); ok {
				return pp.RTTms, nil
			}
			return 0, fmt.Errorf("pong received")
		}
	}
	n.pendMu.Lock()
	delete(n.pending, seq)
	n.pendMu.Unlock()
	return 0, fmt.Errorf("ping timeout")
}

// ---------------------------------------------------------------- signaling

// refreshPublic sends a STUN binding request FROM the mesh socket so the
// reply reveals the mesh port's true public mapping (demuxed in recv loop).
func (n *node) refreshPublic(stunServer string) {
	if stunServer == "" {
		return
	}
	raddr, err := net.ResolveUDPAddr("udp4", stunServer)
	if err != nil {
		return
	}
	wire, txn := signaling.BuildStunRequest()
	n.stunMu.Lock()
	n.stunTxn = txn
	n.stunMu.Unlock()
	_, _ = n.meshConn.WriteToUDP(wire, raddr)
}

func (n *node) signalLoop() {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	n.signalOnce()
	i := 0
	for range t.C {
		i++
		if i%6 == 0 && n.signal != nil {
			// re-resolve ~every minute; stale arg kept simple: default server
			n.refreshPublic("stun.l.google.com:19302")
		}
		n.signalOnce()
	}
}

func (n *node) signalOnce() {
	if n.signal == nil {
		return
	}
	peers, err := n.signal.Heartbeat(n.publicEP)
	if err != nil {
		return
	}
	for _, sp := range peers {
		if sp.NodeID == n.nodeID {
			continue
		}
		rvip := net.ParseIP(sp.VIP)
		ep, err := net.ResolveUDPAddr("udp4", sp.Endpoint)
		if err != nil || rvip == nil {
			continue
		}
		n.peers.Learn(n.vip, &mesh.Peer{NodeID: sp.NodeID, VIP: rvip, Primary: ep,
			Cands: map[string]*net.UDPAddr{ep.String(): ep},
			CandSrc: map[string]string{ep.String(): "signal"}, Room: sp.Room})
		n.punchAll(rvip) // both sides punch every candidate -> NATs open -> direct P2P
	}
}

// ---------------------------------------------------------------- local UI API

func (n *node) uiMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir("../frontend")))
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"node_id": n.nodeID, "vip": n.vip.String(), "room": n.room,
			"public": n.publicEP, "tun": n.tunif.Name(), "peers": len(n.peers.All()),
		})
	})
	mux.HandleFunc("/api/peers", func(w http.ResponseWriter, r *http.Request) {
		type row struct {
			NodeID   string  `json:"node_id"`
			VIP      string  `json:"vip"`
			Endpoint string  `json:"endpoint"`
			Source   string  `json:"source"`
			Room     string  `json:"room"`
			Cands    int     `json:"cands"`
			RTTms    float64 `json:"rtt_ms"`
		}
		rows := []row{}
		for _, p := range n.peers.All() {
			ep, src := "", ""
			if p.Primary != nil {
				ep = p.Primary.String()
				src = p.CandSrc[ep]
			}
			rows = append(rows, row{p.NodeID, p.VIP.String(), ep, src, p.Room, len(p.Cands), p.RTTms})
		}
		writeJSON(w, map[string]any{"peers": rows})
	})
	mux.HandleFunc("/api/games", func(w http.ResponseWriter, r *http.Request) {
		type row struct {
			VIP      string  `json:"vip"`
			Title    string  `json:"title"`
			NodeID   string  `json:"node_id"`
			SeenSAgo float64 `json:"seen_s_ago"`
		}
		rows := []row{}
		n.gamesMu.Lock()
		for vip, g := range n.games {
			rows = append(rows, row{vip, g.Title, g.NodeID, time.Since(g.Seen).Seconds()})
		}
		n.gamesMu.Unlock()
		writeJSON(w, map[string]any{"games": rows})
	})
	mux.HandleFunc("/api/room", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var in struct {
				Room string `json:"room"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in.Room != "" {
				n.room = in.Room
				n.roomKey = mesh.RoomKey(in.Room)
				if n.signal != nil {
					n.signal.Room = in.Room
				}
			}
		}
		writeJSON(w, map[string]any{"room": n.room})
	})
	mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			VIP string `json:"vip"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.VIP == "" && r.URL.Query().Get("vip") != "" {
			in.VIP = r.URL.Query().Get("vip")
		}
		rtt, err := n.PingPeer(net.ParseIP(in.VIP), 3*time.Second)
		if err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"ok": true, "rtt_ms": rtt})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(v)
	_, _ = w.Write(buf.Bytes())
}
