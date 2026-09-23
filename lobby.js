/* LANLink browser lobby: room presence + direct WebRTC latency test.
 * Signaling rides a public MQTT broker (no server to host); the actual
 * ping runs PC-to-PC over an encrypted WebRTC data channel.
 * Pure static JS — runs on Vercel as-is. */
(function () {
  "use strict";
  const $ = (id) => document.getElementById(id);
  const logEl = $("log"), peersEl = $("peers");
  const log = (m) => {
    logEl.textContent += new Date().toLocaleTimeString() + "  " + m + "\n";
    logEl.scrollTop = logEl.scrollHeight;
  };
  const setStatus = (on, msg) => {
    $("dot").className = "dot" + (on ? " on" : "");
    $("status").textContent = msg;
  };

  const BROKER = "wss://broker.emqx.io:8084/mqtt";
  const STUN = { iceServers: [{ urls: "stun:stun.l.google.com:19302" }] };
  const myId = "web-" + Math.random().toString(36).slice(2, 10);
  let client = null, room = "", name = "", peers = {}; // id -> {name,pc,dc,seen,rtt,connected}

  function topicPresence() { return `lanlink/v1/${room}/presence`; }
  function topicSig(id) { return `lanlink/v1/${room}/sig/${id}`; }

  $("joinBtn").onclick = () => {
    name = $("name").value.trim() || ("Guest-" + myId.slice(4, 8));
    room = ($("room").value.trim() || "public-lobby").toLowerCase();
    if (typeof mqtt === "undefined") {
      setStatus(false, "Could not load the signaling library (CDN blocked?). Check connection and reload.");
      return;
    }
    $("joinBtn").disabled = true;
    setStatus(false, `Connecting to room “${room}” as ${name}…`);
    log(`joining room ${room}`);
    client = mqtt.connect(BROKER, { clientId: myId, clean: true, connectTimeout: 8000 });
    client.on("connect", () => {
      setStatus(true, `In room “${room}” as ${name}. Waiting for players…`);
      $("leaveBtn").disabled = false;
      client.subscribe([topicPresence(), topicSig(myId)], (err) => {
        if (err) { setStatus(false, "Subscribe failed: " + err.message); return; }
        announce();
      });
      log("connected to meeting-point broker");
    });
    client.on("message", onSignal);
    client.on("error", (e) => { setStatus(false, "Connection error: " + (e.message || e)); });
    client.on("reconnect", () => setStatus(false, "Reconnecting…"));
  };

  $("leaveBtn").onclick = () => {
    try { client && client.end(true); } catch (e) { /* noop */ }
    Object.values(peers).forEach((p) => { try { p.pc.close(); } catch (e) { /* noop */ } });
    peers = {};
    render();
    $("joinBtn").disabled = false;
    $("leaveBtn").disabled = true;
    setStatus(false, "Not connected.");
    log("left room");
  };

  function announce() {
    if (!client || !client.connected) return;
    client.publish(topicPresence(), JSON.stringify({ id: myId, name, ts: Date.now() }));
  }
  setInterval(announce, 4000);
  setInterval(() => { // expire silent peers
    const now = Date.now();
    let changed = false;
    for (const id of Object.keys(peers)) {
      if (now - peers[id].seen > 15000) {
        try { peers[id].pc.close(); } catch (e) { /* noop */ }
        delete peers[id]; changed = true;
        log(`player left (${id.slice(0, 8)}…)`);
      }
    }
    if (changed) render();
  }, 5000);

  function onSignal(topic, raw) {
    let msg;
    try { msg = JSON.parse(raw.toString()); } catch (e) { return; }
    if (topic === topicPresence()) {
      if (!msg.id || msg.id === myId) return;
      const known = !!peers[msg.id];
      peers[msg.id] = peers[msg.id] || { name: msg.name || "?", pc: null, dc: null, rtt: 0 };
      peers[msg.id].name = msg.name || "?";
      peers[msg.id].seen = Date.now();
      if (!known) {
        log(`found player ${peers[msg.id].name}`);
        if (myId > msg.id) connectPeer(msg.id); // higher id calls: no offer glare
        render();
      }
      return;
    }
    if (!msg.from || !peers[msg.from]) return;
    handlePeerSignal(msg.from, msg);
  }

  function connectPeer(id) {
    const p = peers[id];
    if (p.pc) return;
    const pc = new RTCPeerConnection(STUN);
    p.pc = pc; p.seen = Date.now();
    const dc = pc.createDataChannel("ll");
    wireChannel(id, dc);
    pc.onicecandidate = (e) => {
      if (e.candidate) sendSig(id, { candidate: e.candidate });
    };
    pc.ondatachannel = (e) => wireChannel(id, e.channel);
    pc.createOffer().then((o) => pc.setLocalDescription(o)).then(() => {
      sendSig(id, { sdp: pc.localDescription });
    }).catch((e) => log("offer failed: " + e.message));
  }

  function handlePeerSignal(id, msg) {
    const p = peers[id];
    if (!p.pc) {
      const pc = new RTCPeerConnection(STUN);
      p.pc = pc;
      pc.onicecandidate = (e) => { if (e.candidate) sendSig(id, { candidate: e.candidate }); };
      pc.ondatachannel = (e) => wireChannel(id, e.channel);
    }
    const pc = p.pc;
    if (msg.sdp) {
      pc.setRemoteDescription(new RTCSessionDescription(msg.sdp)).then(() => {
        if (msg.sdp.type === "offer") {
          return pc.createAnswer().then((a) => pc.setLocalDescription(a)).then(() => {
            sendSig(id, { sdp: pc.localDescription });
          });
        }
      }).catch((e) => log("sdp failed: " + e.message));
    } else if (msg.candidate) {
      pc.addIceCandidate(new RTCIceCandidate(msg.candidate)).catch(() => { /* late candidate */ });
    }
  }

  function sendSig(id, obj) {
    if (!client || !client.connected) return;
    client.publish(topicSig(id), JSON.stringify(Object.assign({ from: myId }, obj)));
  }

  function wireChannel(id, dc) {
    const p = peers[id];
    p.dc = dc;
    dc.onopen = () => {
      p.connected = true;
      log(`direct link open with ${p.name}`);
      render();
      ping(id);
    };
    dc.onclose = () => { p.connected = false; render(); };
    dc.onmessage = (e) => {
      const m = String(e.data || "");
      if (m.startsWith("ping:")) dc.send("pong:" + m.slice(5));
      else if (m.startsWith("pong:")) {
        p.rtt = Date.now() - Number(m.slice(5));
        render();
      }
    };
  }

  function ping(id) {
    const p = peers[id];
    if (p && p.dc && p.dc.readyState === "open") {
      p.dc.send("ping:" + Date.now());
      log(`ping → ${p.name}`);
    } else {
      log(`no direct link to ${(p && p.name) || id} yet — still connecting…`);
    }
  }

  function render() {
    const ids = Object.keys(peers);
    $("count").textContent = ids.length;
    peersEl.innerHTML = "";
    if (!ids.length) {
      peersEl.innerHTML = '<tr><td colspan="4" class="empty">No other players yet — ask a friend to join this room code.</td></tr>';
      return;
    }
    ids.forEach((id) => {
      const p = peers[id];
      const tr = document.createElement("tr");
      const link = p.connected ? "🟢 direct" : "🟡 connecting…";
      const rtt = p.rtt ? Math.round(p.rtt) + " ms" : "—";
      tr.innerHTML = `<td>${escapeHtml(p.name)}</td><td>${link}</td><td>${rtt}</td>`;
      const td = document.createElement("td");
      const b = document.createElement("button");
      b.textContent = "Ping";
      b.onclick = () => ping(id);
      td.appendChild(b);
      tr.appendChild(td);
      peersEl.appendChild(tr);
    });
  }

  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, (c) => (
      { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  }
})();
