"""LANLink signaling server — room-code coordination (stdlib only).

The server NEVER relays game traffic; it only introduces peers so they
can UDP hole-punch directly. Mirrors go/cmd/signaling/main.go.

    python py\\signaling.py --port 32440
"""
import argparse
import json
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

rooms = {}  # room -> {node_id: member}


class H(BaseHTTPRequestHandler):
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
        if self.path == "/health":
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"ok")
            return
        if self.path.startswith("/room/peers"):
            from urllib.parse import urlparse, parse_qs
            q = parse_qs(urlparse(self.path).query)
            room = q.get("room", [""])[0]
            return self._json({"peers": fresh(room)})
        self.send_error(404)

    def do_POST(self):
        if self.path != "/room/join":
            self.send_error(404)
            return
        ln = int(self.headers.get("Content-Length", 0))
        try:
            data = json.loads((self.rfile.read(ln) or b"{}").decode())
        except Exception:
            return self._json({"error": "bad join"}, 400)
        room, nid = data.get("room", ""), data.get("node_id", "")
        if not room or not nid:
            return self._json({"error": "bad join"}, 400)
        # Prefer the client's STUN-resolved mesh endpoint (true public UDP
        # mapping). Fall back to server-seen IP + advertised mesh port so a
        # node without STUN is still punchable on port-preserving NATs.
        ep = data.get("endpoint") or ""
        if ":" not in ep:
            try:
                mp = int(data.get("mesh_port", 0)) or 32443
            except (TypeError, ValueError):
                mp = 32443
            ep = f"{self.client_address[0]}:{mp}"
        members = rooms.setdefault(room, {})
        members[nid] = {"node_id": nid, "vip": data.get("vip", ""),
                        "endpoint": ep, "room": room, "ts": time.time()}
        print(f"[signal] {nid} ({data.get('vip')}) joined '{room}' via {ep} "
              f"[{len(members)} in room]")
        self._json({"peers": fresh(room)})


def fresh(room):
    now = time.time()
    return [dict(v, ts=int(v["ts"])) for v in rooms.get(room, {}).values()
            if now - v["ts"] < 60]


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=32440)
    args = ap.parse_args()
    srv = ThreadingHTTPServer(("0.0.0.0", args.port), H)
    print(f"[signal] room server on :{args.port}  (health: /health)")
    srv.serve_forever()
