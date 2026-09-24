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
    $("pub").textContent = s.public || "LAN only (same Wi-Fi is fine)";
    renderNetHelp(s.discovery || {}, 0);
    renderGames([]);
    try {
      const g = await api("/api/games");
      renderGames(g.games || []);
    } catch (e) { /* games optional on old nodes */ }
    const p = await api("/api/peers");
    const rows = p.peers || [];
    $("peerCount").textContent = rows.length;
    renderNetHelp(s.discovery || {}, rows.length);
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

// Game-server browser: shows hosted games announced over the mesh.
function renderGames(games) {
  $("gameCount").textContent = games.length;
  const tb = $("games");
  tb.innerHTML = "";
  if (!games.length) {
    tb.innerHTML = '<tr><td colspan="4" class="empty">No game servers announced yet.</td></tr>';
    return;
  }
  for (const g of games) {
    const tr = document.createElement("tr");
    tr.innerHTML = `<td>${escapeHtml(g.title || "?")}</td>` +
      `<td>${escapeHtml(g.node_id || "?")}</td><td class="mono">${g.vip}</td>`;
    const td = document.createElement("td");
    const b = document.createElement("button");
    b.textContent = "Copy IP";
    b.onclick = async () => {
      try { await navigator.clipboard.writeText(g.vip); log("copied " + g.vip + " — paste it in your game (CoD: connect " + g.vip + ")"); }
      catch (e) { log("copy manually: " + g.vip); }
    };
    td.appendChild(b);
    tr.appendChild(td);
    tb.appendChild(tr);
  }
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => (
    { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

// Explains in plain words why no players are visible yet.
function renderNetHelp(d, peerCount) {
  const el = $("nethelp");
  if (peerCount > 0) { el.innerHTML = ""; return; }
  const sent = d.hellos_sent || 0, heard = d.players_heard || 0;
  let html = `<p>🔍 Searching for players… (announcements sent: <b>${sent}</b> · players heard: <b>${heard}</b>)</p>`;
  if (sent >= 2 && heard === 0) {
    html += `<p>⚠️ Nobody answers. Check, in order:` +
      `<br>1. All PCs on the <b>same Wi-Fi</b>? (Different houses need internet + a meeting-point address in <code>start-player.bat</code>.)` +
      `<br>2. Windows firewall: allow <b>LANLink.exe</b> (or Python) on <b>Private networks</b>. This blocks most people!` +
      `<br>3. Same <b>room code</b> for the Ping to work (players still appear without it).</p>`;
  } else if (heard > 0) {
    html += `<p>👂 Announcements heard but no players listed yet — they should appear within seconds. If not, firewalls are eating the replies (see step 2 above).</p>`;
  }
  el.innerHTML = html;
}
