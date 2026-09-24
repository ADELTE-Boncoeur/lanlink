"""Windows TUN adapter via wintun.dll (ctypes, stdlib-only, no extra packages).

Why this file exists: Ping proves OUR packets flow, but a game's packets
addressed to 10.242.x.y have no road into our app until Windows owns a
virtual adapter for them. With wintun + Administrator, this attaches a real
"LLANLink" adapter, sets 10.242.x.y/16 on it and routes the whole virtual
subnet through us — then `connect 10.242.x.y` in CoD Just Works.

Requirements (one time):
  1. Download wintun.dll from https://www.wintun.net  (match your Windows:
     64-bit -> amd64/wintun.dll) and put it NEXT TO LANLink.exe (same folder).
  2. Right-click -> "Run as administrator" (creating adapters needs Admin).

Without either, attach_tun() returns None and the app keeps working in
lobby/ping mode. EXPERIMENTAL: driver attach can't be tested on locked-down
PCs; please report results.
"""
import ctypes
import os
import subprocess
import sys
import uuid

_ADAPTER_NAME = "LANLink"
_TUNNEL_TYPE = "LANLink"
_GUID = uuid.uuid5(uuid.NAMESPACE_DNS, "lanlink-virtual-adapter")


class _GUID(ctypes.Structure):
    _fields_ = [("Data1", ctypes.c_uint32),
                ("Data2", ctypes.c_uint16),
                ("Data3", ctypes.c_uint16),
                ("Data4", ctypes.c_ubyte * 8)]


def _to_guid(u):
    g = _GUID()
    g.Data1 = u.time_low
    g.Data2 = u.time_mid
    g.Data3 = u.time_hi_version
    g.Data4 = (ctypes.c_ubyte * 8)(*u.bytes[8:16])
    # note: uuid bytes[0:8] layout differs from GUID fields on little-endian,
    # but wintun only needs a STABLE id — this is stable per uuid value.
    g.Data1 = int.from_bytes(u.bytes[0:4], "big")
    g.Data2 = int.from_bytes(u.bytes[4:6], "big")
    g.Data3 = int.from_bytes(u.bytes[6:8], "big")
    return g


def is_admin():
    try:
        return bool(ctypes.windll.shell32.IsUserAnAdmin())
    except Exception:
        return False


def _dll_path():
    here = os.path.dirname(os.path.abspath(sys.argv[0]))
    for cand in (os.path.join(here, "wintun.dll"),
                 os.path.join(here, "amd64", "wintun.dll"),
                 "wintun.dll"):
        if os.path.isfile(cand):
            return cand
    return None


class TunDevice:
    """A real L3 adapter. read() -> raw IPv4 packet from Windows;
    write(pkt) -> inject a packet received from a peer."""

    def __init__(self, dll, adapter, session):
        self._dll = dll
        self._adapter = adapter
        self._session = session

    def read(self):
        size = ctypes.c_uint32(0)
        ptr = self._dll.WintunReceivePackets(self._session, ctypes.byref(size))
        if not ptr or not size.value:
            return None
        try:
            return ctypes.string_at(ptr, size.value)
        finally:
            self._dll.WintunReleaseReceivePacket(self._session, ptr)

    def write(self, pkt: bytes):
        if not pkt:
            return
        ptr = self._dll.WintunAllocateSendPacket(self._session, len(pkt))
        if not ptr:
            raise RuntimeError("wintun: send packet allocation failed")
        ctypes.memmove(ptr, pkt, len(pkt))
        self._dll.WintunSendPacket(self._session, ptr)

    def close(self):
        try:
            self._dll.WintunEndSession(self._session)
        except Exception:
            pass
        try:
            self._dll.WintunCloseAdapter(self._adapter)
        except Exception:
            pass


def attach_tun(vip: str):
    """Try to bring up the real adapter. Returns TunDevice or None (with logs)."""
    path = _dll_path()
    if not path:
        print("[tun] wintun.dll not found next to the app — staying in lobby mode.")
        print("[tun] To enable real game traffic: download it from https://www.wintun.net")
        return None
    if not is_admin():
        print("[tun] not Administrator — Windows won't let us create the adapter.")
        print("[tun] Right-click LANLink.exe -> 'Run as administrator', then use --tun.")
        return None
    try:
        dll = ctypes.WinDLL(path)
    except Exception as e:
        print(f"[tun] could not load {path} ({e})")
        return None
    try:
        dll.WintunCreateAdapter.argtypes = [ctypes.c_wchar_p, ctypes.c_wchar_p,
                                            ctypes.POINTER(_GUID)]
        dll.WintunCreateAdapter.restype = ctypes.c_void_p
        dll.WintunStartSession.argtypes = [ctypes.c_void_p, ctypes.c_uint32]
        dll.WintunStartSession.restype = ctypes.c_void_p
        dll.WintunReceivePackets.argtypes = [ctypes.c_void_p,
                                             ctypes.POINTER(ctypes.c_uint32)]
        dll.WintunReceivePackets.restype = ctypes.c_void_p
        dll.WintunReleaseReceivePacket.argtypes = [ctypes.c_void_p, ctypes.c_void_p]
        dll.WintunAllocateSendPacket.argtypes = [ctypes.c_void_p, ctypes.c_uint32]
        dll.WintunAllocateSendPacket.restype = ctypes.c_void_p
        dll.WintunSendPacket.argtypes = [ctypes.c_void_p, ctypes.c_void_p]
        dll.WintunEndSession.argtypes = [ctypes.c_void_p]
        dll.WintunCloseAdapter.argtypes = [ctypes.c_void_p]

        adapter = dll.WintunCreateAdapter(_ADAPTER_NAME, _TUNNEL_TYPE,
                                          ctypes.byref(_to_guid(_GUID)))
        if not adapter:
            print("[tun] WintunCreateAdapter failed (driver installed? admin?).")
            return None
        session = dll.WintunStartSession(adapter, 0x200000)
        if not session:
            dll.WintunCloseAdapter(adapter)
            print("[tun] WintunStartSession failed.")
            return None
    except Exception as e:
        print(f"[tun] wintun call failed ({e})")
        return None

    # Address + route the virtual subnet through the new adapter (needs Admin).
    for cmd in (
            ["netsh", "interface", "ipv4", "set", "address", f"name={_ADAPTER_NAME}",
             "static", vip, "255.255.0.0", "none"],
            ["netsh", "interface", "ipv4", "add", "route", "10.242.0.0/16",
             _ADAPTER_NAME]):
        try:
            r = subprocess.run(cmd, capture_output=True, text=True, timeout=20)
            if r.returncode != 0 and "already exists" not in (r.stdout + r.stderr).lower():
                print(f"[tun] netsh {' '.join(cmd[3:6])} says: {(r.stdout + r.stderr).strip()[:200]}")
        except Exception as e:
            print(f"[tun] netsh failed ({e}) — continuing anyway")
    print(f"[tun] adapter '{_ADAPTER_NAME}' UP with {vip}/16 — game packets now flow.")
    return TunDevice(dll, adapter, session)
