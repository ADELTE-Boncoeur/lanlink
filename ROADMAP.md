# Implementation Roadmap

## Phase 0 — Done in this repo (kickstart)
- [x] Wire protocol frozen (`HEADER(20)+CT+TAG(8)`, room-keyed XOR+HMAC, stdlib-only)
- [x] Virtual IP allocation `10.242.0.0/16` (hash + clash log, salt-bump in Go)
- [x] LAN discovery via UDP broadcast `:32442` (Part C)
- [x] Mesh send/recv + PING/PONG/PUNCH keepalive (Part D)
- [x] Room-code signaling server + HTTP heartbeat client (no relay, introduce-only)
- [x] STUN public-endpoint lookup + double-sided hole punch
- [x] Userspace stub TUN so everything runs w/o Admin (Part B skeleton)
- [x] Embedded-style lightweight UI (no Electron): status, peers, room, ping
- [x] Python reference impl proving the protocol end to end

## Phase 1 — Real adapter (Windows first)
1. Link `wintun.dll` (`WintunCreateAdapter("LANLink",…)`, `WintunStartSession`).
2. Implement `newReal()` in `go/internal/tun/tun.go` around the session
   (recv → mesh DATA; mesh DATA → send), set `10.242.x.y/16` via the LUID,
   add route `10.242.0.0/16`, metric 5.
3. Require Admin when `-tun` is passed; keep stub as fallback with a clear log.
4. Verify: `ping 10.242.<peer>` < 60 ms broadband; `ipconfig` shows the adapter.

## Phase 2 — NAT traversal hardening
- Parallel STUN (2 servers) + endpoint cache; port-prediction for symmetric NATs.
- TURN relay fallback (coturn) for the ~10% of NAT pairs that can't punch.
- ICE-lite state machine: gather → exchange (signal) → checks → nominate.

## Phase 3 — Security + performance
- Swap XOR+HMAC for ChaCha20-Poly1305 (`golang.org/x/crypto`), same header
  (nonce = seq‖src); keep interop flag `VER=1` vs `2`.
- Replay window (last 1024 seqs per peer), monotonic seq enforcement.
- Per-room PSK → per-device keys via ECDH through the signal channel.
- Benchmarks: target < 0.3 ms encaps overhead, 0% loss at 500 pps.

## Phase 4 — Product polish
- `go:embed` the `frontend/` into the exe (3 lines) or wrap with Wails
  (`wails build -platform windows/amd64`); keep `*_stub` portable zip layout.
- mDNS responder `_lanlink._udp.local` with TXT (id, vip, port, room) + keep broadcast fallback.
- Game lobby sniffer: list LAN-broadcasting games (CoD/Halo beacons) in the UI.
- Auto-update via signed zip; crash-safe config in `%APPDATA%/lanlink/`.
