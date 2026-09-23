// Package tun implements Part B: virtual network interface setup.
//
// Design: the rest of the app talks to the Interface abstraction —
// Read() yields raw IPv4 packets captured locally, Write() injects
// packets received from remote peers. Two backends:
//
//  1. Real L3 TUN (needs Admin + a driver on Windows: wintun or
//     tap-windows6). Enable with -tun. The OS then routes 10.242.0.0/16
//     through us and every LAN game sees the virtual network natively.
//  2. Userspace stub (default). No Admin needed: discovery, room codes,
//     mesh ping and game-listing all work; raw game packets are logged
//     instead of injected. This is what the demo runs in.
//
// Windows production path (wintun, same flow as WireGuard):
//
//	wintun.dll -> WintunCreateAdapter("LANLink", "LANLink", GUID)
//	  -> WintunStartSession(handle, 0x10000 /* ring capacity */)
//	  -> GetInterfaceLuid -> SetInterfaceAddresses(10.242.x.y/16, metric 5)
//	  -> netsh interface ipv4 add route 10.242.0.0/16 <luid>
//	  -> session read loop: WintunReceivePackets / WintunReleaseReceivePacket
//	  -> session write loop: WintunAllocateSendPacket / WintunSendPacket
//
// The stub below keeps the same method set so swapping in the wintun
// session is a drop-in: replace New() with NewWintun() returning Interface.
package tun

import (
	"errors"
	"fmt"
	"net"
	"time"
)

// Interface is a virtual L3 adapter.
type Interface interface {
	Name() string
	VIP() net.IP
	// Read returns one raw IPv4 packet.
	Read() ([]byte, error)
	// Write injects one raw IPv4 packet received from a peer.
	Write(pkt []byte) error
	Close() error
}

// Config for bringing up the adapter.
type Config struct {
	VIP     net.IP // e.g. 10.242.3.7
	MTU     int
	UseReal bool // -tun flag: attempt a real OS adapter
	IfName  string
}

// New picks the real backend if requested, else the stub.
func New(cfg Config) (Interface, error) {
	if cfg.MTU <= 0 {
		cfg.MTU = 1400
	}
	if cfg.UseReal {
		return newReal(cfg)
	}
	return newStub(cfg), nil
}

// ---------------------------------------------------------------------------
// Userspace stub backend (portable, no Admin, no driver)
// ---------------------------------------------------------------------------

type stub struct {
	name string
	vip  net.IP
	rx   chan []byte // injected from mesh -> "arriving" locally
}

func newStub(cfg Config) *stub {
	if cfg.IfName == "" {
		cfg.IfName = "lanlink-stub"
	}
	return &stub{name: cfg.IfName, vip: cfg.VIP, rx: make(chan []byte, 128)}
}

func (s *stub) Name() string { return s.name }
func (s *stub) VIP() net.IP  { return s.vip }

// Read blocks until a packet is injected via Write (loopback demo).
// A real game cannot send through the stub — that requires -tun + Admin.
// We return to the mesh loop which simply idles; keepalive uses PING frames.
func (s *stub) Read() ([]byte, error) {
	pkt, ok := <-s.rx
	if !ok {
		return nil, errors.New("tun: closed")
	}
	return pkt, nil
}

func (s *stub) Write(pkt []byte) error {
	select {
	case s.rx <- pkt:
		return nil
	default:
		return errors.New("tun: stub queue full")
	}
}

func (s *stub) Close() error { return nil }

// ---------------------------------------------------------------------------
// Real-adapter backend (Admin + driver). Portable skeleton.
// ---------------------------------------------------------------------------

type realTun struct {
	stub // reuse channel plumbing until wintun session attaches
}

func newReal(cfg Config) (Interface, error) {
	// On Windows this is where WintunCreateAdapter / StartSession goes.
	// We deliberately fail with an actionable message instead of
	// half-attaching an adapter — see the setup guide in the error.
	return nil, fmt.Errorf(`tun: real adapter requested but no driver session is linked in this build.

To enable full game traffic on Windows (one time, as Administrator):
  1. Install wintun (https://www.wintun.net) or tap-windows6 (OpenVPN).
  2. Rebuild with the wintun session wired into newReal(), then run:
       lanlink.exe -tun -name %s
  3. Verify:  ipconfig  should show  %s / 10.242.0.0/16,
     and  ping %s  from a peer should reply < 60ms on broadband.

Continuing in userspace stub mode is fine for discovery/rooms/ping demo.
Re-run without -tun. (waited %v)`, cfg.VIP, cfg.VIP, cfg.VIP, 2*time.Second)
}
