"""LANLink node — Python reference implementation (stdlib only).

Same wire protocol as go/internal/mesh/packet.go — interoperable.
Runs NOW without a Go toolchain, Admin rights, or drivers (userspace
stub adapter). The Go build is the shippable single .exe; this file
proves the protocol + lets you play the demo end to end.

Usage (two terminals):
    python py\\signaling.py --port 32440
    python py\\lanlink.py --name Player1 --mesh-port 32443 --ui-port 32441
    python py\\lanlink.py --name Player2 --mesh-port 32444 --ui-port 32442
Open http://127.0.0.1:32441 and ...:32442
"""
import argparse
import hashlib
import hmac
import json
import os
import random
import socket
import struct
import sys
import threading
import time
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse, parse_qs

MAGIC = 0x4C4C4E4B
VER = 1
T_DATA, T_PING, T_PONG, T_PUNCH, T_LOBBY = 0x01, 0x02, 0x03, 0x04, 0x05
HDLEN, TAGLEN = 20, 8
BCAST_PORT = 32442
VERSION = "1.2.0"

# Well-known game ports. If one is already taken on THIS pc, a game server
# is almost certainly running there -> we announce it automatically, so hosts
# don't have to type anything. Heuristic, clearly labelled as auto-detected.
GAME_PORTS = {28960: "CoD4", 28961: "CoD MW2", 2302: "Halo", 27015: "Source game"}


def check_signal(url: str):
    """Validate --signal early with a beginner-friendly error. Returns "" if OK."""
    url = (url or "").strip()
    if not url:
        return ""
    try:
        host = urlparse(url).hostname or ""
    except Exception:
        host = ""
    if not host:
        return (f"[signal] '{url}' has no address in it.\n"
                f"  Same Wi-Fi? Leave the signaling server EMPTY (just press Enter).\n"
                f"  Internet? Use the host's real address, e.g. http://192.168.1.5:32440")
    if host.lower() in ("host-ip", "hostname", "your-ip", "server-ip", "example", "test"):
        return (f"[signal] '{host}' is only an EXAMPLE — it is not a real address.\n"
                f"  Same Wi-Fi? Leave it EMPTY (just press Enter).\n"
                f"  Internet? Ask the friend running LANLink-Server.exe for their real IP.")
    try:
        socket.getaddrinfo(host, None)
    except OSError:
        return (f"[signal] could not find any computer called '{host}'.\n"
                f"  Check the spelling, keep the host's black server window open,\n"
                f"  and remember: same Wi-Fi needs NO address at all — leave it empty.")
    return ""


def room_key(room: str) -> bytes:
    room = (room or "public-lobby").lower()
    return hashlib.sha256(("lanlink-v1:" + room).encode()).digest()


def alloc_vip(node_id: str, salt: int = 0) -> str:
    h = hashlib.sha256(f"{node_id}#{salt}".encode()).digest()
    h1 = h[0] if h[0] not in (0, 255) else 7
    h2 = h[1]
    if h2 in (0, 1, 255):
        h2 = struct.unpack(">H", h[2:4])[0] % 253 + 2
        h2 = min(h2, 254)
    return f"10.242.{h1}.{h2}"


def xor_stream(key: bytes, src4: bytes, seq: int, data: bytes) -> bytes:
    nonce = struct.pack(">I", seq) + src4
    out = bytearray()
    ctr = 0
    off = 0
    while off < len(data):
        ks = hashlib.sha256(key + nonce + struct.pack(">I", ctr)).digest()
        for b in ks:
            if off >= len(data):
                break
            out.append(data[off] ^ b)
            off += 1
        ctr += 1
    return bytes(out)


def seal(key, src, dst, ptype, seq, pt) -> bytes:
    hdr = struct.pack(">IBB4s4sIH", MAGIC, VER, ptype,
                      socket.inet_aton(src), socket.inet_aton(dst), seq, len(pt))
    ct = xor_stream(key, socket.inet_aton(src), seq, pt)
    tag = hmac.new(key, hdr + ct, hashlib.sha256).digest()[:TAGLEN]
    return hdr + ct + tag


def open_frame(key, dg: bytes):
    if len(dg) < HDLEN + TAGLEN:
        raise ValueError("short")
    hdr = dg[:HDLEN]
    magic, ver, ptype, src4, dst4, seq, ln = struct.unpack(">IBB4s4sIH", hdr)
    if magic != MAGIC or ver != VER or len(dg) != HDLEN + ln + TAGLEN:
        raise ValueError("malformed")
    ct = dg[HDLEN:HDLEN + ln]
    tag = dg[HDLEN + ln:]
    want = hmac.new(key, hdr + ct, hashlib.sha256).digest()[:TAGLEN]
    if not hmac.compare_digest(want, tag):
        raise ValueError("integrity")
    pt = xor_stream(key, src4, seq, ct)
    return ptype, socket.inet_ntoa(src4), socket.inet_ntoa(dst4), seq, pt


def parse_stun_response(data: bytes):
    """Extract 'ip:port' from a STUN binding response, or ''."""
    try:
        if len(data) < 20 or data[0:2] != b"\x01\x01":
            return ""
        cookie = struct.unpack(">I", data[4:8])[0]
        off = 20
        while off + 4 <= len(data):
            typ, ln = struct.unpack(">HH", data[off:off + 4])
            val = data[off + 4:]
            if typ == 0x0020 and len(val) >= ln >= 8 and val[1] == 0x01:
                xport = struct.unpack(">H", val[2:4])[0] ^ (cookie >> 16)
                xip = struct.unpack(">I", val[4:8])[0] ^ cookie
                return f"{socket.inet_ntoa(struct.pack('>I', xip))}:{xport}"
            off += 4 + ((ln + 3) & ~3)
    except Exception:
        pass
    return ""


def stun_public(stun_host="stun.l.google.com", stun_port=19302, timeout=3.0):
    """Minimal STUN binding request -> 'ip:port' or ''."""
    try:
        txn = os.urandom(12)
        req = struct.pack(">HHI", 0x0001, 0, 0x2112A442) + txn
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        s.settimeout(timeout)
        s.sendto(req, (stun_host, stun_port))
        data, _ = s.recvfrom(512)
        s.close()
        cookie = struct.unpack(">I", data[4:8])[0]
        off = 20
        while off + 4 <= len(data):
            typ, ln = struct.unpack(">HH", data[off:off + 4])
            val = data[off + 4:]
            if typ == 0x0020 and len(val) >= ln >= 8 and val[1] == 0x01:
                xport = struct.unpack(">H", val[2:4])[0] ^ (cookie >> 16)
                xip = struct.unpack(">I", val[4:8])[0] ^ cookie
                ip = socket.inet_ntoa(struct.pack(">I", xip))
                return f"{ip}:{xport}"
            off += 4 + ((ln + 3) & ~3)
    except Exception:
        pass
    return ""


class Node:
    def __init__(self, args):
        self.name = args.name or ("pc-%06x" % random.randint(0, 0xFFFFFF))
        self.vip = alloc_vip(self.name)
        self.room = args.room
        self.key = room_key(args.room)
        self.mesh_port = args.mesh_port
        self.signal_url = (args.signal or "").rstrip("/")
        self.public = ""
        self.peers = {}  # vip -> {node_id, cands:{(ip,port):src}, primary, rtt, seen}
        self.lock = threading.Lock()
        self.disc_sent = 0    # broadcast hellos transmitted (diagnostics)
        self.disc_heard = 0   # foreign hellos received (diagnostics)
        self.disc_last = None  # timestamp of last foreign hello
        self.serve_title = (getattr(args, "serve", "") or "").strip()[:64]
        self.games = {}  # vip -> {title, node_id, seen} (game servers on the mesh)
        self.auto_game = ""  # auto-detected local game server (no typing needed)
        self._tick = 0
        self.seq = random.randint(1, 1 << 30)
        self.pending = {}
        self._stun_txn = None
        self.sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        self.sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.sock.bind(("0.0.0.0", args.mesh_port))
        self.sock.settimeout(2.0)

    def next_seq(self):
        self.seq = (self.seq + 1) & 0xFFFFFFFF
        return self.seq

    # ---------------- mesh ----------------
    # ---------------- peer candidates (ICE-lite) ----------------
    @staticmethod
    def _is_lan(ip: str) -> bool:
        return (ip.startswith("10.") or ip.startswith("192.168.")
                or ip.startswith("172.16.") or ip.startswith("127."))

    def learn_peer(self, vip, node_id, ep, src, room=""):
        """Add endpoint candidate; LAN/direct candidates always win as primary."""
        if not vip or vip == self.vip:
            return
        ep = (ep[0], int(ep[1]))
        with self.lock:
            e = self.peers.get(vip)
            if e is None:
                e = {"node_id": node_id or "", "cands": {}, "primary": ep,
                     "rtt": 0.0, "seen": time.time(), "room": room or ""}
                self.peers[vip] = e
            if node_id:
                e["node_id"] = node_id
            if room:
                e["room"] = room
            e["cands"][ep] = src
            e["seen"] = time.time()
            cur = e["primary"]
            if self._is_lan(ep[0]) and not self._is_lan(cur[0]):
                e["primary"] = ep
            elif self._is_lan(ep[0]) == self._is_lan(cur[0]):
                e["primary"] = ep  # newest of same class wins

    def _cands(self, vip):
        with self.lock:
            e = self.peers.get(vip)
            if not e:
                return []
            return [e["primary"]] + [c for c in e["cands"] if c != e["primary"]]

    def punch(self, ep):
        try:
            f = seal(self.key, self.vip, self.vip, T_PUNCH, self.next_seq(), b"punch")
            self.sock.sendto(f, (ep[0], int(ep[1])))
        except Exception:
            pass

    def punch_peer(self, vip):
        for ep in self._cands(vip):
            self.punch(ep)

    def send_ping(self, vip, timeout=3.0):
        cands = self._cands(vip)
        if not cands:
            raise RuntimeError(f"unknown peer {vip}")
        seq = self.next_seq()
        ts = struct.pack(">Q", time.time_ns())
        frame = seal(self.key, self.vip, vip, T_PING, seq, ts)
        self.pending[seq] = time.time()
        for ep in cands:  # happy-eyeballs: try every candidate
            try:
                self.sock.sendto(frame, ep)
            except Exception:
                pass
        t0 = time.time()
        while time.time() - t0 < timeout:
            time.sleep(0.02)
            if seq not in self.pending:
                with self.lock:
                    return self.peers.get(vip, {}).get("rtt", 0.0)
        self.pending.pop(seq, None)
        raise TimeoutError("ping timeout")

    def mesh_loop(self):
        while True:
            try:
                dg, frm = self.sock.recvfrom(2048)
            except socket.timeout:
                continue
            except Exception:
                return
            # STUN binding responses arrive on the SAME socket — demux by type.
            if len(dg) >= 20 and dg[0:2] == b"\x01\x01" and self._stun_txn \
                    and dg[8:20] == self._stun_txn:
                ep = parse_stun_response(dg)
                if ep and ep != self.public:
                    self.public = ep
                    print(f"[stun] mesh public endpoint: {ep}")
                continue
            try:
                ptype, src, dst, seq, pt = open_frame(self.key, dg)
            except ValueError:
                continue
            if src != self.vip:
                self.learn_peer(src, "", frm, "mesh")
            if ptype == T_PING:
                self.sock.sendto(seal(self.key, self.vip, src, T_PONG, seq, pt), frm)
            elif ptype == T_PONG:
                t0 = self.pending.pop(seq, None)
                if t0 is not None:
                    with self.lock:
                        if src in self.peers:
                            self.peers[src]["rtt"] = (time.time() - t0) * 1000.0
            elif ptype == T_DATA:
                print(f"[tun-stub] DATA {src} -> {dst} ({len(pt)}B) "
                      f"(run Go build -tun as Admin for real injection)")
            elif ptype == T_LOBBY:
                try:
                    info = json.loads(pt.decode())
                    title = str(info.get("title", ""))[:64]
                    if title:
                        with self.lock:
                            self.games[src] = {"title": title,
                                               "node_id": info.get("node", ""),
                                               "seen": time.time()}
                        print(f"[lobby] game server '{title}' @ {src}")
                except Exception:
                    pass

    # ---------------- LAN discovery (Part C) ----------------
    def discovery_loop(self):
        ls = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        ls.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        try:
            ls.bind(("0.0.0.0", BCAST_PORT))
        except OSError as e:
            print(f"[discovery] port {BCAST_PORT} busy ({e}) — receive disabled")
            ls.close()
            return
        ls.settimeout(2.0)
        threading.Thread(target=self._bcast_loop, daemon=True).start()
        while True:
            try:
                raw, frm = ls.recvfrom(2048)
            except socket.timeout:
                continue
            except Exception:
                return
            try:
                h = json.loads(raw.decode())
            except Exception:
                continue
            if h.get("magic") != "LLNK" or h.get("node_id") == self.name:
                continue
            try:
                rvip, rp = h.get("vip"), int(h.get("mesh_port", 0))
            except (TypeError, ValueError):
                continue
            if not rvip or not rp:
                continue
            self.disc_heard += 1
            self.disc_last = time.time()
            ep = (frm[0], rp)
            if rvip == self.vip:
                print(f"[discovery] VIP clash on {rvip} — staying (salt bump in Go build)")
                continue
            self.learn_peer(rvip, h.get("node_id", ""), ep, "lan", h.get("room", ""))
            print(f"[discovery] LAN peer {h.get('node_id')} ({rvip}) via {ep[0]}:{ep[1]}")
            self.punch(ep)

    def _bcast_targets(self):
        """Global + subnet-directed broadcast addresses.

        Some Wi-Fi drivers/stacks deliver one but not the other, so we send
        both. connect() transmits nothing — it only reveals our local IP.
        """
        targets = [("255.255.255.255", BCAST_PORT)]
        try:
            s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
            s.connect(("192.0.2.1", 9))
            local = s.getsockname()[0]
            s.close()
            parts = (local or "").split(".")
            if len(parts) == 4 and not local.startswith("127."):
                directed = ".".join(parts[:3] + ["255"])
                if (directed, BCAST_PORT) not in targets:
                    targets.append((directed, BCAST_PORT))
        except Exception:
            pass
        return targets

    def _bcast_loop(self):
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        s.setsockopt(socket.SOL_SOCKET, socket.SO_BROADCAST, 1)
        hello = lambda: json.dumps({"magic": "LLNK", "node_id": self.name, "vip": self.vip,
                                    "mesh_port": self.mesh_port, "room": self.room,
                                    "ts": int(time.time())}).encode()

        def burst():
            for tgt in self._bcast_targets():
                try:
                    s.sendto(hello(), tgt)
                except Exception:
                    pass
            self.disc_sent += 1

        try:
            burst()  # immediate announce so hosts appear instantly
        except Exception as e:
            print(f"[discovery] broadcast blocked ({e}) — same-WiFi discovery needs UDP broadcast allowed")
        while True:
            time.sleep(3)
            try:
                burst()
            except Exception:
                pass

    # ---------------- signaling / hole punch ----------------
    def signal_loop(self):
        if not self.signal_url:
            return
        self.refresh_public()  # STUN on the MESH socket -> true public mapping
        n = 0
        while True:
            try:
                peers = self.heartbeat()
                for sp in peers:
                    if sp.get("node_id") == self.name:
                        continue
                    rvip = sp.get("vip")
                    ep_s = sp.get("endpoint", "")
                    if not rvip or ":" not in ep_s:
                        continue
                    host, _, port = ep_s.rpartition(":")
                    try:
                        ep = (host, int(port))
                    except ValueError:
                        continue
                    if rvip == self.vip:
                        continue
                    self.learn_peer(rvip, sp.get("node_id", ""), ep, "signal", sp.get("room", ""))
                    print(f"[signal] room peer {sp.get('node_id')} ({rvip}) via {ep_s}")
                    self.punch_peer(rvip)  # both sides punch -> NATs open
            except Exception as e:
                print(f"[signal] {e}")
            n += 1
            if n % 6 == 0:  # re-resolve public mapping ~every minute
                self.refresh_public()
            time.sleep(10)

    def heartbeat(self):
        body = json.dumps({"room": self.room, "node_id": self.name, "vip": self.vip,
                           "mesh_port": self.mesh_port, "endpoint": self.public}).encode()
        req = urllib.request.Request(self.signal_url + "/room/join", data=body,
                                     headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(req, timeout=8) as r:
            return json.loads(r.read().decode()).get("peers", [])

    def detect_local_game(self):
        """Heuristic: a taken well-known game UDP port means a game server
        runs on this PC. Exclusive bind attempt only — never held, never
        steals traffic from the game."""
        for port, game in GAME_PORTS.items():
            s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
            try:
                s.bind(("0.0.0.0", port))
            except OSError:
                return f"{game} server (auto-detected)"
            finally:
                try:
                    s.close()
                except Exception:
                    pass
        return ""

    def keepalive_loop(self):
        while True:
            time.sleep(5)
            self._tick += 1
            if self._tick % 6 == 1:  # re-scan ~every 30 s
                found = self.detect_local_game()
                if found != self.auto_game:
                    self.auto_game = found
                    if found:
                        print(f"[lobby] auto-detected local game: {found}")
            with self.lock:
                vips = list(self.peers.keys())
                old = [k for k, v in self.peers.items() if time.time() - v["seen"] > 90]
                for k in old:
                    del self.peers[k]
            for vip in vips:
                self.punch_peer(vip)  # hold every NAT mapping open
            title = self.serve_title or self.auto_game
            if title:  # announce our hosted game to the whole mesh
                payload = json.dumps({"title": title, "node": self.name,
                                      "vip": self.vip}).encode()
                for vip in vips:
                    for ep in self._cands(vip):
                        try:
                            self.sock.sendto(seal(self.key, self.vip, vip, T_LOBBY,
                                                  self.next_seq(), payload), ep)
                        except Exception:
                            pass
            with self.lock:  # expire silent game servers
                dead = [k for k, g in self.games.items() if time.time() - g["seen"] > 40]
                for k in dead:
                    del self.games[k]

    def refresh_public(self, stun_host="stun.l.google.com", stun_port=19302):
        """STUN binding request FROM the mesh socket, so the reply reveals
        the mesh port's true public mapping (response is demuxed in mesh_loop)."""
        try:
            self._stun_txn = os.urandom(12)
            req = struct.pack(">HHI", 0x0001, 0, 0x2112A442) + self._stun_txn
            self.sock.sendto(req, (stun_host, stun_port))
        except Exception as e:
            print(f"[stun] request failed ({e}) — LAN mode")


def args_stun_enabled():
    return True


def _frontend_dir():
    # 1) PyInstaller bundle: UI files are embedded next to the bootloader.
    if getattr(sys, "frozen", False):
        p = os.path.join(getattr(sys, "_MEIPASS", os.path.dirname(sys.executable)), "frontend")
        if os.path.isdir(p):
            return p
    # 2) Dev layout: <repo>/py/lanlink.py -> <repo>/frontend
    p = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "frontend")
    if os.path.isdir(p):
        return p
    # 3) Portable layout: LANLink.exe sitting beside a frontend/ folder.
    return os.path.join(os.path.dirname(os.path.abspath(sys.argv[0])), "frontend")


FRONTEND_DIR = _frontend_dir()


class UIHandler(BaseHTTPRequestHandler):
    node: Node = None  # set in main

    def log_message(self, *a):
        pass

    def _json(self, obj, code=200):
        raw = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self):
        u = urlparse(self.path)
        if u.path == "/api/status":
            last = self.node.disc_last
            return self._json({"node_id": self.node.nodeID(), "vip": self.node.vip,
                               "room": self.node.room, "public": self.node.public,
                               "tun": "lanlink-stub (userspace)", "peers": len(self.node.peers),
                               "discovery": {
                                   "hellos_sent": self.node.disc_sent,
                                   "players_heard": self.node.disc_heard,
                                   "last_heard_s_ago": round(time.time() - last, 1) if last else None,
                               }})
        if u.path == "/api/peers":
            with self.node.lock:
                rows = [{"node_id": v.get("node_id"), "vip": k,
                         "endpoint": f"{v['primary'][0]}:{v['primary'][1]}",
                         "source": v["cands"].get(v["primary"], ""),
                         "cands": len(v["cands"]), "room": v.get("room", ""),
                         "rtt_ms": v.get("rtt", 0.0)}
                        for k, v in self.node.peers.items()]
            return self._json({"peers": rows})
        if u.path == "/api/games":
            with self.node.lock:
                rows = [{"vip": k, "title": g["title"], "node_id": g.get("node_id", ""),
                         "seen_s_ago": round(time.time() - g["seen"], 1)}
                        for k, g in self.node.games.items()]
            return self._json({"games": rows})
        # static frontend
        path = u.path.lstrip("/") or "index.html"
        fp = os.path.join(FRONTEND_DIR, path)
        if not os.path.isfile(fp):
            fp = os.path.join(FRONTEND_DIR, "index.html")
        ctype = "text/html"
        if fp.endswith(".js"):
            ctype = "text/javascript"
        elif fp.endswith(".css"):
            ctype = "text/css"
        try:
            with open(fp, "rb") as f:
                raw = f.read()
        except FileNotFoundError:
            self.send_error(404)
            return
        self.send_response(200)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_POST(self):
        u = urlparse(self.path)
        ln = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(ln) if ln else b"{}"
        try:
            data = json.loads(body.decode() or "{}")
        except Exception:
            data = {}
        if u.path == "/api/room":
            if data.get("room"):
                self.node.room = data["room"]
                self.node.key = room_key(data["room"])
            return self._json({"room": self.node.room})
        if u.path == "/api/ping":
            vip = data.get("vip") or parse_qs(u.query).get("vip", [""])[0]
            try:
                rtt = self.node.send_ping(vip)
                return self._json({"ok": True, "rtt_ms": rtt})
            except Exception as e:
                return self._json({"ok": False, "error": str(e)})
        self.send_error(404)


def main():
    ap = argparse.ArgumentParser(description="LANLink portable P2P virtual LAN node")
    ap.add_argument("--name", default="")
    ap.add_argument("--mesh-port", type=int, default=32443)
    ap.add_argument("--ui-port", type=int, default=32441)
    ap.add_argument("--room", default="public-lobby")
    ap.add_argument("--signal", default="")
    ap.add_argument("--no-browser", action="store_true",
                    help="do not auto-open the UI page in a browser")
    ap.add_argument("--serve", default="",
                    help='announce a hosted game, e.g. --serve "CoD4 mp_shipment"')
    ap.add_argument("--version", action="version", version="LANLink " + VERSION)
    args = ap.parse_args()

    err = check_signal(args.signal)
    if err:
        print(err)
        print("Nothing was started. Fix the address above and run again.")
        sys.exit(2)

    n = Node(args)
    UIHandler.node = n
    # attach helper used by handler
    n.nodeID = lambda: n.name

    threading.Thread(target=n.mesh_loop, daemon=True).start()
    threading.Thread(target=n.discovery_loop, daemon=True).start()
    threading.Thread(target=n.signal_loop, daemon=True).start()
    threading.Thread(target=n.keepalive_loop, daemon=True).start()

    srv = ThreadingHTTPServer(("127.0.0.1", args.ui_port), UIHandler)
    url = f"http://127.0.0.1:{args.ui_port}"
    print(f"[lanlink] LANLink v{VERSION} node {n.name} vip={n.vip} room={n.room} "
          f"mesh=:{args.mesh_port} ui={url}")
    print(f"[tun-stub] userspace mode (no Admin needed). "
          f"Go build + '-tun' as Admin attaches the real adapter.")
    if not args.no_browser:
        try:
            import webbrowser
            webbrowser.open(url)
            print(f"[ui] opened {url} in your browser")
        except Exception as e:
            print(f"[ui] could not open a browser ({e}) — open {url} yourself")
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
