# PHC protocol notes

Everything we know (so far) about talking to a PEHA/Honeywell PHC system, and
the plan for finishing the reverse-engineering. Two completely different
protocols live here — don't confuse them:

1. **STM-IP protocol** — between *this app* and the **STM v3** over the LAN.
   This is the one we ultimately need. It is proprietary and only partially
   documented publicly.
2. **PHC bus protocol** — between the STM and the modules over **RS-485**. We
   fully understand this one (documented below) but the app never speaks it
   directly. It's useful for understanding command/state semantics and as a
   fallback path (replace the STM with an ESP32 bridge).

---

## 1. STM-IP protocol (app ⇄ STM v3) — TARGET

### What we know

- There are three STM generations:
  - **STM v1 / v2** — RS-232 serial; remote access needs an external IP↔RS-232
    converter and speaks a **binary protocol over TCP**.
  - **STM v3** — built-in Ethernet; speaks **XML-RPC over HTTP**. This is the
    one networked installations (and our user) have.
- The official **PHC Home Control** app (Honeywell) requires *"control units
  from version 3 with network connection, firmware V3.28+"*, max 3 control
  units per project. It **auto-discovers** the system on the Wi-Fi LAN, then
  **auto-loads the PHC project** and shows live status. It can switch, dim, and
  drive shutters/blinds. This is exactly the surface we are rebuilding.
- The STM v3 runs an embedded Linux daemon, **STMD**, which exposes the XML-RPC
  management interface. The PHC *Systemsoftware* (the Windows/Java tool the user
  mentioned) uses it to transfer the project and for remote control/monitoring.
  STMD parses the project's module-list XML to drive its reporting and command
  handling.
- Hobbyist integrations (FHEM, Homey) also use STM v3 **HTTP "Action URLs"** — a
  simpler trigger mechanism — for one-way control. Good enough for "press this
  button" but not for full state sync.

### What we DON'T know yet (the gap to close)

- Exact discovery mechanism (likely a UDP broadcast on the LAN; need port +
  payload).
- XML-RPC endpoint URL, TCP port, and method names (e.g. how to enumerate
  modules, read channel state, set an output, dim, move a shutter, subscribe to
  events).
- Authentication (the Systemsoftware uses a project password — need to know how
  it's presented over the wire).

### How to close the gap (in priority order)

1. **Decompile the PHC Systemsoftware (best path).** It's a Java app. Grab the
   install dir's `.jar`s and run them through a decompiler (CFR / Procyon /
   `jadx` works on jars too). Search for `XmlRpc`, `STMD`, `4070`/port
   constants, `setOutput`, `getState`, `broadcast`, `Modul`. This yields the
   protocol exactly. **If you can send me the jar(s), I'll extract it.**
2. **Packet-capture the official app.** Put the iPhone and STM on a network you
   can sniff (mirror port, or run the app through a laptop hotspot with
   Wireshark, or mitmproxy if it's plain HTTP). Capture: app launch (discovery),
   project load, and a few light/shutter actions. Share the pcap.
3. **STM v3 Action URLs.** Lowest effort, partial functionality. Documented in
   PHC forums / FHEM threads; lets us drive inputs via HTTP GET without the full
   XML-RPC story. A useful stopgap for a first end-to-end "it actually toggled a
   light" milestone.

Findings get encoded in `Sources/Client/STMv3Client.swift`.

---

## 2. PHC bus protocol (STM ⇄ modules, RS-485) — REFERENCE

Fully reverse-engineered by the community and implemented in
[TillFleisch/ESPHome-PHC-Controller](https://github.com/TillFleisch/ESPHome-PHC-Controller)
and the openHAB `phc` binding. Source for the details below.

### Physical layer
- RS-485, **19200 baud, 8 data bits, no parity, 2 stop bits**.

### Frame format
```
byte 0      : address  = (class << 5) | dip        (3-bit class, 5-bit DIP addr)
byte 1      : (toggle << 7) | length               (toggle bit + 7-bit content length)
byte 2..n   : content  (length bytes, 0..3 for received msgs)
byte n+1..2 : CRC16     (little-endian: low byte first)
```
- **Toggle bit**: flipped on each new command; an ack/retransmit keeps the
  original value. Used to match confirmations and dedupe retransmits.
- **CRC**: CRC-16/X-25 — poly `0x1021` reflected (`0x8408`), init `0xFFFF`,
  reflected in/out, final XOR `0xFFFF`. Computed over bytes `0..n` (address +
  length + content), appended low byte first.

### Module classes (the 3 high bits of the address byte)
| Class            | Address bits | Role                                   |
|------------------|--------------|----------------------------------------|
| EMD (input)      | `0x00`       | 16 inputs (buttons) / 8 LED outputs    |
| AMD (output)     | `0x40`       | 8 relay outputs (lights, outlets)      |
| JRM (shutter)    | `0x40`*      | 4 shutter/blind channels               |

\* AMD and JRM share the `0x40` class prefix; they're distinguished by project
config, not by the address byte.

### Commands (content byte semantics)
- **AMD output** (5-byte frame, content length 1): content =
  `(channel << 5) | fn`, where `fn` = `0x02` ON, `0x03` OFF.
- **EMD LED output**: content = `(channel << 4) | fn`, `0x02` ON / `0x03` OFF
  (channel uses 4 bits here, not 3).
- **JRM shutter idle/stop** (length 2): `content[0] = (channel<<5)|0x02`,
  `content[1] = 0xFC` (priority).
- **JRM shutter move** (length 4): `content[0] = (channel<<5) | (0x05 up |
  0x06 down)`, `content[1] = 0x07` (priority), `content[2..3]` = movement time
  (little-endian, units of 100 ms).
- **Acknowledgement**: `address, (toggle<<7)|0x01, 0x00, crc_lo, crc_hi`.
- **State reports** arrive as messages with `content[0] == 0x00`; `content[1]`
  is a bitmask of channel states for that module.
- **Config request** from a module: `content[0] == 0xFF` → controller replies
  with that module's config (see `send_amd_config` / `send_emd_config` in the
  ESP project).

These semantics are what the STM exposes (in some encoded form) over the XML-RPC
interface; understanding them makes the IP-protocol capture much easier to read.
</content>
