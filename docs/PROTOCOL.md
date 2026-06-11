# PHC protocol notes

Everything we know about talking to a PEHA/Honeywell PHC system. Two completely
different protocols live here — don't confuse them:

1. **STM-IP protocol** — between *this app* and the **STM** over the LAN. The one
   we ultimately need. **Now largely understood** thanks to decompiling the PHC
   Systemsoftware (see §1).
2. **PHC bus protocol** — between the STM and the modules over **RS-485**. Fully
   understood (see §2). The app never speaks it directly, but it is the *payload*
   carried inside the STM-IP `sendTelegram` calls, so it matters a lot.

---

## 1. STM-IP protocol (app ⇄ STM) — XML-RPC

### Confirmed by decompiling PHC Systemsoftware V2.70 (2010)

The Systemsoftware ships an **Integration Server** (`app/iserver/iserver.exe`),
built on **GNU CommonC++** (`ccgnu2`/`ccext2`), whose `ost::XMLRPC` class is an
**XML-RPC server over TCP**. The GUI (project editor) is the XML-RPC *client*.
The server bridges XML-RPC ⇄ the STM. Extracted facts:

- Banner: *"Running in XMLRPC-mode!"*, *"Listening on Port: %d"*,
  *"Socket-Port for XML-RPC (default: **6680**)"*.
- Config keys: `tcpRpcPort`, `tcpRpcIPAddress`, `tcpRpcMaxConnections`,
  `busaddress`, `serPort` (this 2010 build reaches the STM over a **serial COM
  port**, default COM1; STM v3 does it over Ethernet instead — see caveat).
- Standard XML-RPC envelope: `<methodCall>` / `<methodName>` / `<methodResponse>`;
  param types via `addParam` string/int/bool + `begArray`/`endArray` +
  struct/member — i.e. plain `<string>`, `<int>`, `<boolean>`, `<array>`,
  `<struct>`.

### The XML-RPC method set (exact names from the binary)

| Method                          | Purpose                                            |
|---------------------------------|----------------------------------------------------|
| `service.stm.connect`           | Open/initialise the STM connection                 |
| `service.stm.getModule`         | Query module(s) / state                            |
| `service.stm.sendTelegram`      | **Send a raw PHC bus telegram to a module** (core) |
| `service.stm.sendPOR`           | Send POR (power-on-reset / config) table           |
| `service.stm.sendSinglePOR`     | Send a single POR entry                            |
| `service.stm.getProgress`       | Progress of a long operation                       |
| `service.module.writeProject`   | Write a project to a module                        |
| `service.module.firmwareUpdate` / `…AES` | Module firmware update                     |
| `iserver.ping` / `getVersion` / `getPath` / `shutdown` / `conf` / `log` | Server utilities |

### STM-internal command/telegram names (seen in log strings)
`STM_VERSION_READ`, `STM_TELE_PASSTHRU` (STM passes a telegram through to a
module — *"module not found (address 0x%2X)"*), `STM_GET_REALTIMECLOCK`,
`STM_POR_TAB_FREE`, `STM_EINZEL_POR` (single POR), `PHC_STM_INTERN_TO_EXTERN`.
Plus async **events**: *"event from STM %d: %02x%02x - dd.mm.yyyy hh:mm:ss"* —
this is how button presses / state changes are reported back to the client.

### How control works, end to end
1. `service.stm.connect` to attach to the control unit.
2. `service.stm.getModule` to enumerate modules and read state (build the UI).
3. To switch a light / move a shutter: `service.stm.sendTelegram` with a PHC bus
   telegram (format in §2) addressed to the AMD/JRM channel.
4. Live updates arrive as STM **events** (the app folds these into its state).

So: **the app is an XML-RPC client; commands are PHC bus telegrams wrapped in
`service.stm.sendTelegram`.** We already know the telegram bytes exactly (§2).

### Caveat: V2.70 (2010) vs the user's STM v3
This 2010 build runs `iserver` locally and reaches the STM over **serial**. The
user has an **STM v3** that the official iOS app reaches over **Ethernet**
directly. The overwhelmingly likely reality: the STM v3 firmware runs an onboard
equivalent of `iserver` (the **STMD** daemon) exposing this same `service.stm.*`
XML-RPC surface over TCP. Still to confirm for v3 specifically:
- the **TCP port** the STM v3 listens on (6680 is the documented default, but the
  STM may differ),
- **discovery** (the official app auto-finds the STM — likely a UDP broadcast),
- **authentication** (the project password, and how it's presented).

### Fastest way to confirm v3 specifics
1. **Packet-capture the official iOS app** against the real STM (one light
   on/off + app launch). Plain XML-RPC over HTTP/TCP is trivially readable. This
   nails port + discovery + auth + exact param order in one shot.
2. Or obtain a **newer (V3.x-era) Systemsoftware** whose `iserver` does TCP-to-STM
   and decompile the same way.

Findings get encoded in `Sources/Client/STMv3Client.swift`.

---

## 2. PHC bus protocol (STM ⇄ modules, RS-485) — the telegram payload

Fully reverse-engineered by the community and implemented in
[TillFleisch/ESPHome-PHC-Controller](https://github.com/TillFleisch/ESPHome-PHC-Controller)
and the openHAB `phc` binding. This is exactly what rides inside
`service.stm.sendTelegram`.

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
  original value. Matches confirmations and dedupes retransmits.
- **CRC**: CRC-16/X-25 — poly `0x1021` reflected (`0x8408`), init `0xFFFF`,
  reflected in/out, final XOR `0xFFFF`. Over bytes `0..n` (address + length +
  content), appended low byte first.

### Module classes (the 3 high bits of the address byte)
| Class            | Address bits | Role                                   |
|------------------|--------------|----------------------------------------|
| EMD (input)      | `0x00`       | 16 inputs (buttons) / 8 LED outputs    |
| AMD (output)     | `0x40`       | 8 relay outputs (lights, outlets)      |
| JRM (shutter)    | `0x40`*      | 4 shutter/blind channels               |

\* AMD and JRM share the `0x40` class prefix; distinguished by project config.

### Commands (content byte semantics)
- **AMD output** (content length 1): `(channel << 5) | fn`, `fn` = `0x02` ON,
  `0x03` OFF.
- **EMD LED output**: `(channel << 4) | fn`, `0x02` ON / `0x03` OFF (4-bit channel).
- **JRM shutter idle/stop** (length 2): `content[0]=(channel<<5)|0x02`,
  `content[1]=0xFC` (priority).
- **JRM shutter move** (length 4): `content[0]=(channel<<5)|(0x05 up | 0x06 down)`,
  `content[1]=0x07` (priority), `content[2..3]`=movement time (LE, units 100 ms).
- **Acknowledgement**: `address, (toggle<<7)|0x01, 0x00, crc_lo, crc_hi`.
- **State reports**: messages with `content[0]==0x00`; `content[1]` is a bitmask
  of channel states for that module.
- **Config request** from a module: `content[0]==0xFF` → controller replies with
  that module's config (see `send_amd_config` / `send_emd_config` in the ESP project).

The Swift implementations of CRC + telegram builders live in
`Sources/Client/PHCTelegram.swift`.
</content>
