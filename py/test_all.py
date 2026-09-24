"""LANLink full test battery — proves the mesh actually works.

Runs in one go: unit checks (crypto, VIPs, STUN parsing) + live checks
(signaling server + 4 nodes as subprocesses: 3 in HALO-42, 1 in OTHER-99).

    python py\\test_all.py
Exit code 0 = all green.
"""
import json
import socket
import struct
import subprocess
import sys
import time
import urllib.request
import os

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
from lanlink import (seal, open_frame, room_key, alloc_vip,
                      parse_stun_response, T_PING, Node, GAME_PORTS, VERSION,
                      check_signal)

PASS, FAIL = 0, 0


def check(name, cond, extra=""):
    global PASS, FAIL
    if cond:
        PASS += 1
        print(f"  [PASS] {name}")
    else:
        FAIL += 1
        print(f"  [FAIL] {name} {extra}")


def get(url, timeout=10):
    with urllib.request.urlopen(url, timeout=timeout) as r:
        return r.status, r.read()


def post(url, obj, timeout=10):
    raw = json.dumps(obj).encode()
    req = urllib.request.Request(url, data=raw,
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return json.loads(r.read().decode())


# ---------------------------------------------------------------- unit
print("== UNIT: crypto / protocol ==")
k = room_key("HALO-42")
for n in (0, 1, 100, 1400):
    pt = os.urandom(n)
    f = seal(k, "10.242.1.2", "10.242.3.4", T_PING, 7, pt)
    t, s, d, seq, out = open_frame(k, f)
    check(f"roundtrip {n}B (overhead {len(f)-n}B)", out == pt and s == "10.242.1.2"
          and d == "10.242.3.4" and seq == 7 and len(f) - n == 28, f"len={len(f)}")

_, _, _, _, _ = open_frame(k, seal(k, "10.242.1.2", "10.242.3.4", 1, 1, b"x"))
try:
    open_frame(room_key("OTHER-99"), seal(k, "10.242.1.2", "10.242.3.4", 1, 1, b"x"))
    check("wrong room key rejected", False)
except ValueError:
    check("wrong room key rejected", True)

f = bytearray(seal(k, "10.242.1.2", "10.242.3.4", 1, 1, b"tamper-me"))
f[25] ^= 0xFF
try:
    open_frame(k, bytes(f))
    check("tampered frame rejected", False)
except ValueError:
    check("tampered frame rejected", True)

for bad in (b"", b"junk", seal(k, "10.242.1.2", "10.242.3.4", 1, 1, b"ok")[:10]):
    try:
        open_frame(k, bad)
        check(f"malformed ({len(bad)}B) rejected", False)
    except ValueError:
        check(f"malformed ({len(bad)}B) rejected", True)

check("room key case-insensitive", room_key("HALO-42") == room_key("halo-42"))
v1, v2 = alloc_vip("Player1"), alloc_vip("Player1")
check("VIP deterministic", v1 == v2, f"{v1} vs {v2}")
for name in ("A", "B", "Player1", "x" * 40):
    v = alloc_vip(name)
    a, b, c, d = map(int, v.split("."))
    check(f"VIP {name!r} -> {v} in 10.242/16, no .0/.1/.255",
          (a, b) == (10, 242) and d not in (0, 1, 255))

# crafted STUN binding response -> parser must recover ip:port
cookie, ip, port = 0x2112A442, 0xCB9A6816, 45678  # 203.154.104.22
val = struct.pack(">BBHI", 0, 1, (port ^ (cookie >> 16)) & 0xFFFF, ip ^ cookie)
resp = (struct.pack(">HHI", 0x0101, 8, cookie) + os.urandom(12)
        + struct.pack(">HH", 0x0020, 8) + val)
check("STUN response parsed", parse_stun_response(resp) == "203.154.104.22:45678",
      parse_stun_response(resp))
check("non-STUN ignored", parse_stun_response(b"LLNK" + b"\x00" * 30) == "")

ver = subprocess.run([sys.executable, "py/lanlink.py", "--version"],
                     cwd=os.path.dirname(HERE), capture_output=True, text=True)
check("version flag reports build", "LANLink " + VERSION in (ver.stdout + ver.stderr),
      (ver.stdout + ver.stderr).strip())
check("signal validator flags placeholder",
      "EXAMPLE" in check_signal("http://host-ip:32440/").upper(),
      check_signal("http://host-ip:32440/"))
check("signal validator accepts empty (same-WiFi)", check_signal("") == "")
check("signal validator accepts real IP",
      check_signal("http://127.0.0.1:32440") == "")
check("signal validator rejects unresolvable host",
      check_signal("http://no-such-host-xyz123:32440") != "")

# auto-detect: occupy a well-known game port -> node must report a server
probe = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
try:
    probe.bind(("0.0.0.0", 28960))
    held_here = True
except OSError:
    held_here = False  # something real already serves CoD4 here — even better
check("auto-detect sees occupied game port",
      Node.detect_local_game(None) == "CoD4 server (auto-detected)",
      Node.detect_local_game(None))
probe.close()
if held_here:
    check("auto-detect quiet when ports free", Node.detect_local_game(None) == "",
          Node.detect_local_game(None))

# multi-homed peer: hello carries a hotspot IP + a good LAN IP -> we must
# shortlist both but prefer the one on OUR subnet (no sockets needed here)
mk = Node.__new__(Node)
mk.vip = "10.242.0.1"
mk.peers = {}
mk.lock = __import__("threading").Lock()
mk._ips_cache = ["192.168.1.50"]
mk._ips_at = 9999999999.0
c = Node.candidates_from_hello(
    {"ips": ["192.168.137.9", "bad", "127.0.0.1", "192.168.1.9"]},
    "192.168.1.9", 32443)
check("hello candidates: sender first, junk filtered",
      c == [("192.168.1.9", 32443), ("192.168.137.9", 32443)], str(c))
mk.learn_peer("10.242.0.2", "Bob", ("192.168.137.9", 32443), "signal", "R")
mk.learn_peer("10.242.0.2", "Bob", ("192.168.1.9", 32443), "lan", "R")
e = mk.peers["10.242.0.2"]
check("primary prefers our subnet over hotspot net",
      e["primary"] == ("192.168.1.9", 32443) and len(e["cands"]) == 2,
      str(e["primary"]))

# ---------------------------------------------------------------- live
print("== LIVE: signaling + 4 nodes ==")
procs = []


def spawn(*args):
    p = subprocess.Popen([sys.executable, "-u"] + list(args),
                         cwd=os.path.dirname(HERE),
                         stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    procs.append(p)
    return p


def wait_up(url, label, tries=30):
    for _ in range(tries):
        try:
            st, _ = get(url, timeout=3)
            if st == 200:
                print(f"  [info] {label} up")
                return True
        except Exception:
            time.sleep(1)
    check(f"{label} online", False)
    return False


B = 32440  # base port; override with: python py\test_all.py --base=32540
for _a in sys.argv[1:]:
    if _a.startswith("--base="):
        B = int(_a.split("=", 1)[1])
SIG, M1, U1, M2, U2, M3, U3, M4, U4 = B, B + 3, B + 1, B + 4, B + 2, B + 5, B + 6, B + 7, B + 8

# Preflight: refuse to test against a stranger's ports (a stale LANLink
# holding them would silently answer our HTTP checks and fake the results).
def _free(port, udp=False):
    import socket as _s
    s = _s.socket(_s.AF_INET, _s.SOCK_DGRAM if udp else _s.SOCK_STREAM)
    try:
        s.bind(("0.0.0.0" if udp else "127.0.0.1", port))
        return True
    except OSError:
        return False
    finally:
        s.close()


_taken = [p for p in (SIG, U1, U2, U3, U4) if not _free(p)]
_taken += [p for p in (M1, M2, M3, M4) if not _free(p, udp=True)]
if _taken:
    print(f"ABORT: ports already in use {_taken} — close old LANLink windows or use --base=NNNNN")
    sys.exit(2)
try:
    spawn("py/signaling.py", "--port", str(SIG))
    nodes = [("Alpha", M1, U1, "HALO-42"),
             ("Bravo", M2, U2, "HALO-42"),
             ("Coco", M3, U3, "HALO-42"),
             ("Stranger", M4, U4, "OTHER-99")]
    for name, mp, up, room in nodes:
        extra = ["--serve", "CoD4 Test Server"] if name == "Alpha" else []
        spawn("py/lanlink.py", "--name", name, "--mesh-port", str(mp),
              "--ui-port", str(up), "--room", room, "--no-browser", *extra,
              "--signal", f"http://127.0.0.1:{SIG}")
    ok = wait_up(f"http://127.0.0.1:{SIG}/health", "signaling")
    for _, _, up, _ in nodes:
        ok = wait_up(f"http://127.0.0.1:{up}/api/status", f"ui:{up}") and ok
    if not ok:
        raise SystemExit(1)
    time.sleep(14)  # let discovery (3s) + heartbeat (10s) + STUN settle

    st = {up: json.loads(get(f"http://127.0.0.1:{up}/api/status")[1]) for _, _, up, _ in nodes}
    v = {up: s["vip"] for up, s in st.items()}
    check("all nodes have 10.242.x.y VIPs",
          all(x.startswith("10.242.") for x in v.values()), str(v))
    check("VIPs unique", len(set(v.values())) == 4, str(v))
    check("discovery counters published + heard peers",
          all(s.get("discovery", {}).get("hellos_sent", 0) > 0
              and s.get("discovery", {}).get("players_heard", 0) > 0
              for s in st.values()),
          str({u: s.get("discovery") for u, s in st.items()}))
    check("STUN public endpoints resolved",
          all(s.get("public") for s in st.values()),
          str({u: s.get("public") for u, s in st.items()}))

    peers = {up: json.loads(get(f"http://127.0.0.1:{up}/api/peers")[1])["peers"]
             for _, _, up, _ in nodes}
    for up in (U1, U2, U3):
        got = {p["vip"] for p in peers[up]} - {v[up]}
        check(f"node :{up} sees both room peers", len(got) >= 2, str(got))
    check("peers carry their room codes",
          all(p.get("room") for up in (U1, U2, U3) for p in peers[up]),
          str(peers[U1]))

    # game-server announcement rides the mesh: Alpha serves, Bravo must list it
    bg = json.loads(get(f"http://127.0.0.1:{U2}/api/games")[1])["games"]
    check("game server announced across mesh",
          any(g["vip"] == v[U1] and "CoD4" in g.get("title", "") for g in bg),
          str(bg))
    check("peers API flags same-LAN direct play",
          all(p.get("same_lan") for up in (U1, U2, U3) for p in peers[up]
              if p["vip"] != v[up]),
          str(peers[U1]))
    check("games API carries host LAN address",
          all(g.get("lan") for g in bg), str(bg))
    r = post(f"http://127.0.0.1:{U1}/api/chat", {"text": "ready?"})
    check("chat send ok", r.get("ok") is True, str(r))
    time.sleep(2)  # chat is instant UDP — no interval to wait out
    c = json.loads(get(f"http://127.0.0.1:{U2}/api/chat")[1])["chat"]
    check("chat arrives over encrypted mesh",
          any(m.get("text") == "ready?" and "Alpha" in m.get("from", "") for m in c),
          str(c))

    # every pair in HALO-42 pings both ways through the encrypted mesh
    for a, b in [(U1, U2), (U1, U3), (U2, U3)]:
        for src, dst in ((a, b), (b, a)):
            try:
                r = post(f"http://127.0.0.1:{src}/api/ping", {"vip": v[dst]}, timeout=12)
                check(f"ping :{src} -> :{dst}", r.get("ok") is True
                      and r.get("rtt_ms", 9999) < 500, str(r))
            except Exception as e:
                check(f"ping :{src} -> :{dst}", False, str(e))

    # room isolation: server must not leak Stranger into HALO-42; cross-room
    # pings must fail (different keys -> silent drop -> timeout)
    with urllib.request.urlopen(f"http://127.0.0.1:{SIG}/room/peers?room=HALO-42",
                                timeout=5) as r:
        members = {m["node_id"] for m in json.loads(r.read())["peers"]}
    check("signaling isolates rooms", members == {"Alpha", "Bravo", "Coco"}, str(members))
    try:
        r = post(f"http://127.0.0.1:{U1}/api/ping", {"vip": v[U4]}, timeout=12)
        check("cross-room ping fails closed", r.get("ok") is False, str(r))
    except Exception as e:
        check("cross-room ping fails closed", False, str(e))

    # UI serves the app + room switching works
    stt, body = get(f"http://127.0.0.1:{U1}/")
    check("UI page served", stt == 200 and b"LANLink" in body)
    r = post(f"http://127.0.0.1:{U4}/api/room", {"room": "HALO-42"})
    check("room switch API", r.get("room") == "HALO-42", str(r))
    post(f"http://127.0.0.1:{U4}/api/room", {"room": "OTHER-99"})
finally:
    for p in procs:
        try:
            p.terminate()
        except Exception:
            pass
    time.sleep(1)
    for p in procs:
        try:
            p.kill()
        except Exception:
            pass

print(f"\n==== RESULT: {PASS} passed, {FAIL} failed ====")
sys.exit(1 if FAIL else 0)
