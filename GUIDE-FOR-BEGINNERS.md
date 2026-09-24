# 🎮 LANLink — Easy Guide (no experience needed)

**What is this?** A free tool that lets you play old "same Wi-Fi only"
multiplayer games (Call of Duty, Halo, Age of Empires, Minecraft LAN…)
with friends over the internet, as if everyone sat in the same room.
No port forwarding. No accounts. No installation — it runs from a folder.

**How it works (30-second version):** each PC gets a virtual address like
`10.242.51.102`. Your game thinks that address is a computer next to you.
Your PC sends the game data directly to your friend's PC, locked with a
password (your Room Code), so strangers can't read it or join in.

> ✅ Tested working: 33/33 automatic checks pass — discovery, encrypted
> ping between 3 players, room isolation (strangers can't reach your game).
> Run `python py\test_all.py` yourself to see the proof.

---

## THE 5-MINUTE EXAMPLE — you + 1 friend + Call of Duty, same Wi-Fi

Imagine: **you** host, your friend **Mike** joins. Same Wi-Fi. No internet needed.

| Who | Runs | Room code |
|---|---|---|
| You (host) | `LANLink.exe` | `HALO-42` (you invent it) |
| Mike | `LANLink.exe` | `HALO-42` (**same** code — yes, he types it too) |
| Nobody | `LANLink-Server.exe` ❌ | — |

`LANLink-Server.exe` is ONLY for friends in **different houses** (internet).
On the same Wi-Fi, forget it exists — nobody runs it.

1. **Both**: double-click `LANLink.exe` → browser opens → allow the firewall
   popup (Private networks). Wait ~10 s → you see **each other in the
   Players table**. Click **Ping** — a number means you're linked.
2. **You (host)**: open CoD → Multiplayer → **Start New Server** (a normal
   LAN server, like your friend sits next to you). Note YOUR virtual IP
   from the LANLink page, e.g. `10.242.51.102`. Tell it to Mike.
3. **Mike**: on his LANLink page, find your game in **🎮 Game servers** →
   click **Copy join IP** (it copies the address that actually works for him)
   → open CoD → press **`~`** (enable console first: Options → Game Options →
   Enable Console: Yes) → type `connect` + paste → Enter.
   (Same Wi-Fi usually pastes a `192.168.x.y` address — that one works today.
   The `10.242.x.y` virtual address needs TUN mode below.)
4. **You both play.** 🎉 Chat in the page's 💬 Lobby chat to coordinate.

⚠️ Honest note: Mike will probably **NOT** see your server in CoD's automatic
server list — that auto-list needs a special network driver. Until the TUN
mode below is battle-tested, connect with the address the page gives you
(same Wi-Fi: the direct `192.168.x.y` one). If a game has "Connect to IP"
in its menus, use that instead of the console.

**TUN mode (experimental — makes virtual IPs work in games):**
1. Download `wintun.dll` from https://www.wintun.net (64-bit `amd64` folder)
   and put it next to `LANLink.exe`.
2. Right-click `LANLink.exe` → **Run as administrator**, then start it with
   `--tun` (or edit `start-player.bat`). Windows gets a real `LANLink`
   adapter with your `10.242.x.y`, and `connect 10.242.x.y` works in-game.
   Please report whether it worked — this path is new and untested on
   locked-down PCs.

## PART 1 — Install (every player, one time, ~2 minutes)

**Easiest (no Python needed):** download **`LANLink.exe`** (single 5.6 MB file —
button on the website or in the project folder) and double-click it.
If Windows SmartScreen says "Unknown publisher", click **More info → Run anyway**.
Then skip to Part 2 — the `.bat` launchers use the `.exe` automatically.

**From source (needs Python):** only if you want the code, not the app:
1. **Install Python** (the only thing the source version needs):
   - Go to https://www.python.org/downloads/ → big yellow **Download** button → run it.
   - ⚠️ On the FIRST install screen, tick the little box
     **"Add python.exe to PATH"** at the bottom, then click Install.
   - Check it worked: press `Win+R`, type `cmd`, Enter, then type
     `python --version` → you should see `Python 3.x`. If Windows asks
     you to install from the Microsoft Store instead, do that — it works too.
2. **Get LANLink**: download the ZIP of this project
   (green `<> Code` button on GitHub → `Download ZIP`), unzip it anywhere
   (Desktop is fine, USB stick is fine).
3. **Allow it through the firewall**: the first time you run it, Windows
   will pop up "Windows Defender Firewall has blocked some features" →
   tick **Private networks** (and Public if you trust your network) →
   **Allow access**. Every player must do this, or PCs can't see each other.

## PART 2 — Play with friends on the SAME Wi-Fi (easiest, 1 minute)

1. Everyone double-clicks **`start-player.bat`**, types their name, presses Enter.
2. A page opens in the browser (`http://127.0.0.1:32441`) showing **Your virtual IP**
   (e.g. `10.242.51.102`) and, after a few seconds, the other players.
3. One person hosts the game in **LAN / Local / Offline multiplayer** mode.
4. Everyone else joins using the **host's virtual IP** (`10.242.x.y`) exactly like
   a normal LAN address. Done — play! 🎉
5. Not sure it's connected? Click **Ping** next to a player — you want a number
   well under 100 ms.

## PART 3 — Play over the INTERNET (different houses)

You need one extra piece: a tiny "meeting point" program (the signaling
server) that introduces the players. Game data still flows directly
PC-to-PC; the server only swaps addresses for ~10 seconds.

**Option A — one friend hosts the meeting point (free, 2 minutes):**
1. That friend double-clicks **`start-signaling.bat`** and leaves the black
   window open. It shows something like `room server on :32440`.
2. That friend finds their address and sends it to everyone:
   press `Win+R` → `cmd` → type `ipconfig` → read the `IPv4 Address`
   (same Wi-Fi case) — or for different houses, Google "what is my ip"
   on the host's PC and share that number (their router must let port
   `32440` through; if it doesn't work, use Option B).
3. Everyone (including the host) runs `start-player.bat` and when asked for
   the signaling server, pastes `http://THAT-ADDRESS:32440`.
4. Everyone types the **same Room Code** (invent one, e.g. `HALO-42`) and the
   same password-like code keeps strangers out. Then play exactly like Part 2.

**Option B — free cloud meeting point (works always, 5 minutes):**
put `py/signaling.py` on any free Python host (Render.com, Replit, etc.)
with the command `python signaling.py --port 32440` — it's one file, no
extra libraries. Share its `https://...` address as the signaling server.

## PART 4 — How a game night goes (cheat sheet for the group chat)

1. 📥 Everyone: install Python once (Part 1).
2. 🖥️ Host: `start-signaling.bat` (internet games only), share address + Room Code.
3. 🧑‍🤝‍🧑 Everyone: `start-player.bat` → name → server address → Room Code.
4. 🌐 Everyone: open the page, check all players appear, Ping < 100 ms.
5. 🎮 Host: create LAN game. Others: join with host's `10.242.x.y`.
6. ❌ After playing: just close the black windows. Nothing was installed.

## PART 5 — If something doesn't work

| Symptom | Fix |
|---|---|
| `python is not recognized` | Reinstall Python WITH "Add to PATH" ticked (Part 1, step 1) |
| No players appear after 30 s | Everyone on same Room Code (capitals matter); firewall allowed; same signaling server address |
| Ping button says `timeout` | Firewall blocked it — allow Python; both players keep the window open |
| `address already in use` | An old window is still running — close black windows, wait 10 s, retry |
| Players never appear on same Wi-Fi | Run `fix-firewall.bat` **as Administrator** (sets Private + opens ports). Still nothing? Router → turn **AP/Client Isolation OFF** |
| CoD says `server timed out` on `connect` | You pasted a `10.242.x.y` without TUN mode, or a wrong-subnet address. Use the page's **Copy join IP** button — it picks the working one |
| `unrecognized arguments: --serve` | Your `LANLink.exe` is older than your `.bat` — re-download the exe from GitHub (the launcher prints the version: you want v1.2.0+) |
| Typed `http://host-ip:32440` / `ERR_NAME_NOT_RESOLVED` | `host-ip` is only an EXAMPLE, not an address! Same Wi-Fi → leave it **empty** (press Enter). Internet → type the host friend's **real** numbers |
| Game can't find the host | Join with the host's **virtual** IP (`10.242.x.y` from the page), not their normal IP; host must use the game's LAN mode |
| Strangers in the room | Change the Room Code — it's the password |

## PART 6 — For the curious (advanced, optional)

- **Real game packets** (this Python version already carries ping/discovery;
  full raw-packet injection for every game needs the compiled version):
  install Go, `cd go`, `go build -o lanlink.exe ./cmd/lanlink`, then run
  `lanlink.exe -tun` **as Administrator** with wintun installed. See `ROADMAP.md`.
- **Developers**: protocol spec in `README.md`, diagrams in `ARCHITECTURE.md`,
  tests in `py/test_all.py` (33 checks, exit code 0 = green).
