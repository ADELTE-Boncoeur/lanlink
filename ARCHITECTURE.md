# Architecture

```
 ┌──────────── PC-A ────────────┐            ┌──────────── PC-B ────────────┐
 │ Game (CoD/Halo)              │            │ Game                          │
 │   ↕ 10.242.3.7               │            │   ↕ 10.242.9.44               │
 │ TUN adapter (wintun/-tun)    │            │ TUN adapter                   │
 │   ↕ raw IPv4                 │            │   ↕ raw IPv4                  │
 │ lanlink node                 │            │ lanlink node                  │
 │  ├─ discovery :32442 bcast   │──LAN bcast─┼─► discovery                  │
 │  ├─ mesh UDP :32443 ─────────┼──P2P UDP (hole-punched)──► mesh :32444    │
 │  └─ UI 127.0.0.1:32441       │            │  └─ UI :32442                  │
 └──────────────┬───────────────┘            └──────────────┬────────────────┘
                │ HTTP heartbeat + peer list (introduce only, NO relay)
                ▼                                           ▼
        ┌──────────────────────────────────────────────────────────┐
        │ Signaling server :32440  rooms[code] -> [{id,vip,endpoint}]│
        │ + STUN (stun.l.google.com:19302) for public ip:port       │
        └──────────────────────────────────────────────────────────┘
```

**Hot path (per game packet):** `TUN.Read → route(vip→endpoint) → Seal → UDP send`
(≈28 B overhead). **Receive:** `UDP → Open/verify → TUN.Write`.
PING/PONG reuses the same sealed channel for RTT. PUNCH keepalives (5 s)
hold NAT mappings open. Peers prune after 90 s silence.

**Why UDP hole punching works:** the first outbound packet from A→B creates
a NAT mapping on A's router; B's simultaneous punch does the same; the
stateful firewalls then see both flows as "replies" and pass return traffic.
The signaling server only exchanges `ip:port` + `vip` — game data never
touches it, so latency = direct ping A↔B and server bandwidth ≈ 0.
