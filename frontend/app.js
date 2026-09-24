// Lightweight UI — plain fetch() polling, no framework (keeps binary tiny).
const $ = (id) => document.getElementById(id);
const logEl = $("log");
let lastStatus = {};
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
    lastStatus = s;
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
    refreshChat();
    const p = await api("/api/peers");
    const rows = p.peers || [];
    $("peerCount").textContent = rows.length;
    renderNetHelp(s.discovery || {}, rows.length);
    const tb = $("peers");
    tb.innerHTML = "";
    if (!rows.length) {
      tb.innerHTML = '<tr><td colspan="7" class="empty">No peers yet — start a second node or share your room code.</td></tr>';
      return;
    }
    const myRoom = (s.room || "").toLowerCase();
    let strangers = 0;
    for (const r of rows) {
      const rRoom = r.room || "";
      const mismatch = rRoom && rRoom.toLowerCase() !== myRoom;
      if (mismatch) strangers++;
      const tr = document.createElement("tr");
      if (mismatch) tr.style.opacity = "0.55";
      const rtt = !r.rtt_ms ? "—" : (r.rtt_ms < 1 ? "&lt;1 ms" : r.rtt_ms.toFixed(0) + " ms");
      const reach = r.same_lan
        ? `<br><span style="background:#238636;border-radius:10px;padding:0 8px;font-size:11px">same Wi-Fi ✓ CoD: connect ${(r.endpoint || "").split(":")[0]}</span>`
        : `<br><span style="color:#8b949e;font-size:11px">remote — CoD via virtual IP needs TUN mode</span>`;
      tr.innerHTML = `<td>${r.node_id || "?"}</td><td class="mono">${r.vip}</td>` +
        `<td class="mono">${r.endpoint || ""}${reach}</td><td>${r.source || ""}</td>` +
        `<td class="mono">${escapeHtml(rRoom) || "?"}</td><td>${rtt}</td>`;
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
    if (strangers > 0) {
      $("nethelp").innerHTML += `<p>⚠️ ${strangers} player(s) use a <b>different room code</b> — Ping and games only work in the same room. Type the identical code on all PCs.</p>`;
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
    const best = (g.same_lan && g.lan) ? g.lan : g.vip;
    const join = `<span class="mono">${g.vip}</span>` + ((g.same_lan && g.lan)
      ? `<br><span style="color:#8b949e;font-size:12px">same Wi-Fi — or connect direct: <span class="mono">${escapeHtml(g.lan)}</span></span>` : "");
    tr.innerHTML = `<td>${escapeHtml(g.title || "?")}</td>` +
      `<td>${escapeHtml(g.node_id || "?")}</td><td>${join}</td>`;
    const td = document.createElement("td");
    const b = document.createElement("button");
    b.textContent = "Copy join IP";
    b.onclick = async () => {
      try { await navigator.clipboard.writeText(best); log(`copied ${best} — in your game: connect ${best}`); }
      catch (e) { log("copy manually: " + best); }
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
  if (sent >= 2 && heard === 0 && !lastStatus.has_signal) {
    html += `<p>🛜 Same Wi-Fi but total silence? Your router may isolate devices — log in and turn <b>AP/Client Isolation OFF</b> — or Windows set this network to <b>Public</b>. Run <code>fix-firewall.bat</code> as Administrator: it sets Private + opens the ports.</p>`;
  }
  el.innerHTML = html;
}

async function refreshChat() {
  try {
    const c = await api("/api/chat");
    const box = $("chatbox");
    if (!box) return;
    box.innerHTML = "";
    const msgs = c.chat || [];
    if (!msgs.length) box.innerHTML = '<div class="empty">No messages yet — say hi to coordinate the match.</div>';
    for (const m of msgs) {
      const div = document.createElement("div");
      div.innerHTML = `<b>${escapeHtml(m.from || "?")}:</b> ${escapeHtml(m.text || "")}`;
      box.appendChild(div);
    }
    box.scrollTop = box.scrollHeight;
  } catch (e) { /* ignore */ }
}

$("sendBtn").onclick = async () => {
  const t = $("chatInput").value.trim();
  if (!t) return;
  $("chatInput").value = "";
  await api("/api/chat", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ text: t }),
  });
  refreshChat();
};
