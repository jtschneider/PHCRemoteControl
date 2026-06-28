#!/usr/bin/env python3
"""Mock PHC STM v3 — a stand-in control unit for offline testing.

The app speaks XML-RPC over HTTP (POST /, port 6680). This serves the four calls
it makes, so you can run "Connect to STM" against your Mac instead of the real
hardware — handy when you have your iPhone but not the STM:

    service.stm.whoAreYou      -> identity struct
    service.stm.readFile       -> the project ZIP, base64, in chunks
    service.stm.sendTelegram   -> ack; tracks on/off so polling reflects toggles
    service.stm.simInputEvent  -> ack (shutters / central commands)

Usage:
    python3 tools/mock-stm.py [PROJECT_DIR_OR_ZIP] [--port 6680]

PROJECT_DIR_OR_ZIP defaults to ./project (zips its project.ppfx/tpfx/cpfx).
Point the app at this Mac's LAN IP (printed on startup) : 6680.
"""
import argparse, base64, glob, io, os, re, socket, sys, zipfile
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

def build_zip(path: str) -> bytes:
    if os.path.isfile(path) and path.lower().endswith(".zip"):
        return open(path, "rb").read()
    # Otherwise treat as a directory holding the .ppfx/.tpfx/.cpfx
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as z:
        found = False
        for ext in ("ppfx", "tpfx", "cpfx"):
            for f in glob.glob(os.path.join(path, f"*.{ext}")):
                z.write(f, arcname=f"project.{ext}")
                found = True
        if not found:
            sys.exit(f"No .ppfx/.tpfx/.cpfx found in {path!r}")
    return buf.getvalue()

def lan_ip() -> str:
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    try:
        s.connect(("8.8.8.8", 80)); return s.getsockname()[0]
    except OSError:
        return "127.0.0.1"
    finally:
        s.close()

# --- XML-RPC response builders -------------------------------------------------

def resp(body: str) -> bytes:
    return (f'<?xml version="1.0" encoding="iso-8859-1"?>\n'
            f'<methodResponse>{body}</methodResponse>').encode("utf-8")

def struct(members: dict) -> bytes:
    out = ""
    for k, v in members.items():
        val = f"<base64>{v[1]}</base64>" if v[0] == "b" else \
              f"<i4>{v[1]}</i4>" if v[0] == "i" else f"<string>{v[1]}</string>"
        out += f"<member><name>{k}</name><value>{val}</value></member>"
    return resp(f"<params><param><value><struct>{out}</struct></value></param></params>")

def array(vals) -> bytes:
    items = "".join(f"<value><i4>{v}</i4></value>" for v in vals)
    return resp(f"<params><param><value><array><data>{items}</data></array></value></param></params>")

# --- server --------------------------------------------------------------------

class Mock(BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(n).decode("utf-8", "replace")
        m = re.search(r"<methodName>([^<]+)</methodName>", body)
        method = (m.group(1) if m else "").split(".")[-1]
        ints = [int(x) for x in re.findall(r"<i4>(-?\d+)</i4>", body)]
        data = self.handle_call(method, ints)
        print(f"  → {method}{ints}")
        self.send_response(200)
        self.send_header("Content-Type", "text/xml")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def handle_call(self, method, ints):
        if method == "whoAreYou":
            return struct({"STM-Address": ("i", 0),
                           "Facility-ID": ("s", "{mock}"),
                           "Device-ID": ("s", "0000"),
                           "Device-Name": ("s", "Mock STM")})
        if method == "readFile":
            idx = ints[1] if len(ints) >= 2 else 0
            idx = max(0, min(idx, len(CHUNKS) - 1))
            return struct({"cur": ("i", idx), "total": ("i", len(CHUNKS)),
                           "crc": ("i", 0),
                           "bin": ("b", base64.b64encode(CHUNKS[idx]).decode())})
        if method == "sendTelegram" and len(ints) >= 3:
            _, addr, content = ints[0], ints[1], ints[2]
            if content != 1:                    # a set command: (channel<<5)|com
                ch, com = content >> 5, content & 0x1F
                bits = STATE.get(addr, 0)
                if com == 2:   bits |= (1 << ch)      # on
                elif com == 3: bits &= ~(1 << ch)     # off
                elif com == 6: bits ^= (1 << ch)      # toggle
                STATE[addr] = bits
            return array([0, addr, 0, 0, STATE.get(addr, 0)])
        return array([0, 0, 0, 0, 0])           # simInputEvent etc.

    def log_message(self, *a):  # we print our own concise line
        pass

if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("project", nargs="?", default="project")
    ap.add_argument("--port", type=int, default=6680)
    args = ap.parse_args()

    ZIP = build_zip(args.project)
    CHUNK = 30000
    CHUNKS = [ZIP[i:i + CHUNK] for i in range(0, len(ZIP), CHUNK)] or [b""]
    STATE: dict[int, int] = {}

    ip = lan_ip()
    print(f"Mock STM serving {args.project!r} ({len(ZIP)} B, {len(CHUNKS)} chunk(s))")
    print(f"In the app → Connect to STM → enter:  {ip}:{args.port}")
    print(f"(simulator can also use 127.0.0.1:{args.port}).  Ctrl-C to stop.\n")
    ThreadingHTTPServer(("0.0.0.0", args.port), Mock).serve_forever()
