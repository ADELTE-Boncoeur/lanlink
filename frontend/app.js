// Lightweight UI — plain fetch() polling, no framework (keeps binary tiny).
const $ = (id) => document.getElementById(id);
const logEl = $("log");
function log(m) {
  logEl.textContent += new Date().toLocaleTimeString() + "  " + m + "\n";
  logEl.scrollTop = logEl.scrollHeight;
}

async function api(path, opts) {
  const r = await fetch(path, opts);
  return r.json();
}

async function refresh() {
  try {
    const s = await api("/api/status");
    $("nodeId").textContent = s.node_id || "…";
    $("vip").textContent = s.vip || "…";
    $("room").textContent = s.room || "…";
    $("tun").textContent = s.tun || "…";
    $("pub").textContent = s.public || "LAN only";
    const p = await api("/api/peers");
    const rows = p.peers || [];
    $("peerCount").textContent = rows.length;
    const tb = $("peers");
    tb.innerHTML = "";
    if (!rows.length) {
      tb.innerHTML = '<tr><td colspan="6" class="empty">No peers yet — start a second node or share your room code.</td></tr>';
      return;
    }
    for (const r of rows) {
      const tr = document.createElement("tr");
      const rtt = r.rtt_ms ? r.rtt_ms.toFixed(0) + " ms" : "—";
      tr.innerHTML = `<td>${r.node_id || "?"}</td><td class="mono">${r.vip}</td>` +
        `<td class="mono">${r.endpoint || ""}</td><td>${r.source || ""}</td><td>${rtt}</td>`;
      const td = document.createElement("td");
      const b = document.createElement("button");
      b.textContent = "Ping";
      b.onclick = async () => {
        b.disabled = true;
        const res = await api("/api/ping", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ vip: r.vip }),
        });
        log(res.ok ? `pong ${r.vip} ${res.rtt_ms.toFixed(1)} ms` : `ping ${r.vip} failed: ${res.error}`);
        b.disabled = false;
        refresh();
      };
      td.appendChild(b);
      tr.appendChild(td);
      tb.appendChild(tr);
    }
  } catch (e) {
    log("UI error: " + e);
  }
}

$("joinBtn").onclick = async () => {
  const room = $("roomInput").value.trim() || "public-lobby";
  await api("/api/room", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ room }),
  });
  log("joined room " + room);
  refresh();
};
$("refreshBtn").onclick = refresh;
setInterval(refresh, 4000);
refresh();
