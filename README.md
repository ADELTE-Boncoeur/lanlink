# LANLink — Portable P2P Virtual LAN for LAN Gaming

> **New here? Start with [GUIDE-FOR-BEGINNERS.md](GUIDE-FOR-BEGINNERS.md)** —
> plain-language setup, double-click launchers (`start-player.bat`,
> `start-signaling.bat`), and troubleshooting. No experience needed.

Run LAN games (CoD, Halo, Age of Empires, Minecraft LAN, etc.) with friends
over the internet as if you were on the same Wi-Fi / ethernet switch.

* **Single static binary, no installer, USB-portable** (Go, stdlib-only)
* **Virtual subnet `10.242.0.0/16`** — every player gets a stable virtual IP
* **LAN discovery** via UDP broadcast; **internet rooms** via tiny signaling server + room codes
* **Direct P2P UDP mesh** with hole-punching (STUN/ICE-lite), minimal 28-byte overhead
* **TUN/TAP adapter** when run as Admin; **userspace fallback** otherwise (demo still runs)
* **UI served from the binary** — open `http://127.0.0.1:32441`, no Electron/Node needed
  (Wails/Tauri can wrap the same `frontend/` later)

```
lanlink/
  go/          Go backend (the shippable product — 1 static .exe)
    cmd/lanlink/        node: TUN + discovery + mesh + UI server
    cmd/signaling/      public room-code coordination server
    internal/tun/       Part B — virtual interface setup
    internal/discovery/ Part C — LAN broadcast discovery
    internal/mesh/      Part D — packet encapsulation + peer table
    internal/signaling/ room client + STUN + hole-punch
  frontend/    lightweight HTML/CSS/JS (embedded via go:embed)
  py/          Python reference impl — SAME wire protocol, runs here without Go
```

## Quick start (no Go toolchain needed — runs now)

```powershell
# 1) Start the public-style signaling server (room codes)
python py\signaling.py --port 32440

# 2) Start two nodes (separate terminals) — they find each other via LAN broadcast
python py\lanlink.py --name Player1 --mesh-port 32443 --ui-port 32441
python py\lanlink.py --name Player2 --mesh-port 32444 --ui-port 32442

# 3) Open UI:  http://127.0.0.1:32441  and  http://127.0.0.1:32442
#    Set the same Room Code (e.g. HALO-42) on both, click Ping to measure latency.
#    Host your game on one PC, join via the peer's 10.242.x.y address on the other.
```

## Quick start (Go — the real portable binary)

```powershell
# Requires Go 1.21+. Produces ONE file, runs from any folder/USB, no install.
cd go
go build -trimpath -ldflags "-s -w" -o lanlink.exe ./cmd/lanlink
go build -trimpath -ldflags "-s -w" -o signaling.exe ./cmd/signaling
.\signaling.exe -addr :32440
.\lanlink.exe -name Player1   # UI on http://127.0.0.1:32441
```

Run `lanlink.exe` **as Administrator** with `-tun` to attach a real
L3 TUN adapter (Windows: wintun / tap-windows6 must be present, see
`go/internal/tun/tun.go`). Without Admin it runs in userspace mode —
discovery, room codes, mesh ping and the game-list still work; only raw
game-packet injection needs the adapter.

## Wire protocol (Go <-> Python interoperable)

```
UDP payload = HEADER(20) || CIPHERTEXT(n) || TAG(8)      # 28 bytes overhead
HEADER = MAGIC u32be 0x4C4C4E4B ("LLNK") | VER u8=1 | TYPE u8 | SRC_VIP u32be
         | DST_VIP u32be | SEQ u32be | LEN u16be
TYPE: 0x01 DATA (raw IP packet)  0x02 PING  0x03 PONG  0x04 PUNCH keepalive
key = SHA256(room_code.lower())
nonce = SEQ || SRC_VIP
keystream block_i = SHA256(key || nonce || counter_be32)
ciphertext = plaintext XOR keystream
tag = HMAC-SHA256(key, HEADER || ciphertext)[:8]
```

See `ROADMAP.md` for the full build plan and `ARCHITECTURE.md` for diagrams.
