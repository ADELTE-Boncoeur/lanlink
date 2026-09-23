// Command signaling — lightweight public coordination server for room codes.
//
// Stores room -> members in memory. The server NEVER relays game traffic;
// it only introduces peers (their public endpoints as seen in RemoteAddr)
// so they can UDP hole-punch directly. Run ONE of these publicly; point
// all players at it with -signal http://host:32440.
//
// Endpoints:
//	POST /room/join  {room, node_id, vip, mesh_port, endpoint?} -> {peers:[...]}
//	GET  /room/peers?room=CODE -> {peers:[...]}
//	GET  /health -> ok
//
// Portable: stdlib only, `go build -o signaling.exe ./cmd/signaling`.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

type Member struct {
	NodeID   string `json:"node_id"`
	VIP      string `json:"vip"`
	Endpoint string `json:"endpoint"`
	Room     string `json:"room"`
	Ts       int64  `json:"ts"`
}

type hub struct {
	mu    sync.RWMutex
	rooms map[string]map[string]*Member // room -> nodeID -> member
}

func newHub() *hub { return &hub{rooms: map[string]map[string]*Member{}} }

func (h *hub) join(room string, m *Member, remote string) []Member {
	h.mu.Lock()
	defer h.mu.Unlock()
	if m.Endpoint == "" {
		m.Endpoint = remote
	}
	m.Room = room
	m.Ts = time.Now().Unix()
	members, ok := h.rooms[room]
	if !ok {
		members = map[string]*Member{}
		h.rooms[room] = members
	}
	members[m.NodeID] = m
	out := make([]Member, 0, len(members))
	for _, v := range members {
		out = append(out, *v)
	}
	return out
}

func (h *hub) peers(room string) []Member {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var out []Member
	for _, v := range h.rooms[room] {
		if time.Since(time.Unix(v.Ts, 0)) < 60*time.Second {
			out = append(out, *v)
		}
	}
	return out
}

func main() {
	addr := flag.String("addr", ":32440", "listen address")
	flag.Parse()

	h := newHub()

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	http.HandleFunc("/room/join", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", 405)
			return
		}
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		var in struct {
			Room     string `json:"room"`
			NodeID   string `json:"node_id"`
			VIP      string `json:"vip"`
			Endpoint string `json:"endpoint"`
			MeshPort int    `json:"mesh_port"`
		}
		if err := json.Unmarshal(raw, &in); err != nil || in.Room == "" || in.NodeID == "" {
			http.Error(w, "bad join", 400)
			return
		}
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		ep := in.Endpoint
		if _, _, err := net.SplitHostPort(ep); err != nil {
			// No usable STUN endpoint advertised: fall back to the
			// server-seen IP + advertised mesh port (works on
			// port-preserving NATs, keeps the node punchable).
			if in.MeshPort == 0 {
				in.MeshPort = 32443
			}
			ep = fmt.Sprintf("%s:%d", host, in.MeshPort)
		}
		m := &Member{NodeID: in.NodeID, VIP: in.VIP, Endpoint: ep}
		peers := h.join(in.Room, m, "")
		writeJSON(w, map[string]any{"peers": peers})
	})
	http.HandleFunc("/room/peers", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"peers": h.peers(r.URL.Query().Get("room"))})
	})

	log.Printf("signaling on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

func portOf(ep string) int {
	_, p, err := net.SplitHostPort(ep)
	if err != nil {
		return 0
	}
	var n int
	_, _ = fmt.Sscanf(p, "%d", &n)
	return n
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
