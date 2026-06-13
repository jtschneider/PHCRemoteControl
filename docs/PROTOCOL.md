# PHC protocol notes

Everything we know about talking to a PEHA/Honeywell PHC system. Two completely
different protocols live here — don't confuse them:

1. **STM-IP protocol** — between *this app* and the **STM** over the LAN. The one
   we need. **Confirmed by packet capture** (§1).
2. **PHC bus protocol** — between the STM and the modules over **RS-485**. Fully
   understood (§2). The app never speaks it directly; for lights/outlets it is
   the *payload* the STM frames internally, and for shutters the app bypasses it
   entirely (simulated input events).

Sources: a **mitmproxy capture** of the official iOS app driving a real STM v3
(authoritative for §1), plus decompiled **PHC Systemsoftware V2.70/V3.0** for
the wider framework surface and `functions.xml`, cross-checked against
[ESPHome-PHC-Controller](https://github.com/TillFleisch/ESPHome-PHC-Controller)
(§2).

---

## 1. STM-IP protocol (app ⇄ STM) — XML-RPC over HTTP

### Transport (confirmed by capture)
- **XML-RPC over plain HTTP** — `POST http://<host>:6680/ HTTP/1.1`, body is the
  XML-RPC `<methodCall>`, `Content-Type: application/x-www-form-urlencoded`.
- The STM replies `HTTP/1.0 200 OK` with a **non-standard `Date` header line**
  (mitmproxy rejects it without `--set validate_inbound_headers=false`).
- **No authentication and no `connect`/`activate` handshake** on the LAN — the
  app goes straight to `whoAreYou` + `readFile`. The decompiled framework's
  `connect`/`activate`/`VisuNotActivated` flow is **not used** on this path.

### What the app actually uses (confirmed)
| Method                     | Purpose                                                     |
|----------------------------|-------------------------------------------------------------|
| `service.stm.whoAreYou`    | Identity probe / reachability: `{STM-Address, Facility-ID, Device-ID, Device-Name}` |
| `service.stm.readFile`     | Download the project as a chunked base64 **ZIP** (see below) |
| `service.stm.sendTelegram` | Light/outlet control **and** AMD state read (3 int params)  |
| `service.stm.simInputEvent`| Shutter control by simulating EMD rocker input              |

Params are plain `<i4>` integers; `sendTelegram` takes **three ints**
`(stm_idx, module_bus_addr, content_byte)` — the STM does CRC/framing
internally, so the app does **not** send raw RS-485 frames.

### Loading the project (`readFile`)
```
readFile(0, chunkIdx, 1)  → {cur, total, crc, bin:<base64 ZIP chunk>}   (loop until cur == total-1)
```
Concatenate the base64-decoded chunks → a ZIP holding `project.ppfx`
(hardware config), `project.tpfx` (automation logic), `project.cpfx` (UI
groupings) and a `.facl`. **Gotcha:** every entry sets general-purpose flag
**bit 3**, so the local-header `cSize`/`uSize` are `0` (real sizes live in the
trailing data descriptor) and the data is **raw DEFLATE** (no zlib header /
Adler-32). A hand-rolled parser is unreliable (the `PK\x07\x08` descriptor
signature can occur inside deflate data); use a library that reads the central
directory — the app uses **ZIPFoundation**.

### Light / outlet control + state
- **Set:** `sendTelegram(0, 0x40|dip, (channel<<5)|com)` — `com` 2=ON, 3=OFF, 6=toggle.
- **Read:** `sendTelegram(0, busAddr, 1)` → `[0, addr, toggle, ?, state_bitmask]`;
  bit N set ⇒ output channel N active. Polled once per AMD module every 2.5 s.

### Shutter control (`simInputEvent`)
The app does not send JRM telegrams; it **simulates the physical rocker**:
```
simInputEvent(stm=0, emd_module, channel, event_type, key_type=4)
```
`emd_module` = EMD `MOD adr`, `channel` = `CHA adr`, `key_type` = 4 (EMD_RUE).
Events: 2=press, 3=longPress, 4=release, 5=doublePress. Verified on hardware:
**short tap** (press→release→doublePress) = move in that channel's direction;
**long press** (press→longPress) = stop (no-op when idle). Lower uses the
`senken` channel, raise the `heben` channel.

### Live updates
The app uses **polling** (the `sendTelegram … content=1` state read above) on a
2.5 s timer for AMD outputs. Shutters/EMD inputs have no pollable state. No
server-pushed events are used.

### Framework reference (decompiled, *not* all used on the LAN path)
The V3 framework jar exposes a much larger surface — `connect`, `activate`,
`getModule`, `getState`, `getVersion`, `getVoltage`, `get/setClock`, `sendPOR`,
`writeBinary`/`checkBinaryCRC`, `service.module.*`, `iserver.*` — and fault
codes (`enums/EErrorCode`: `VisuNotActivated`, `UnregisteredBusAdr`, …). These
describe the full configuration tool, not the runtime control path the iOS app
takes; treat them as reference. Discovery/gateway chaining
(`IAnlagenSTMDetails`: `ip`/`mac`/`serial`, up to 3 STMs) also exists in the
framework, but the app simply takes the STM's IP from the user.

### Command/telegram model (`interfaces/ICommand`, `functions.xml`)
A command has an `internalCommandName`, an integer `command` code, a
`commandGroupname`, optional `extendedBytes`, and flags
(`STOP_ANALOG_PROCESSING`, `INVISIBLE`, `GLOBAL`, `INI`). `functions.xml` maps
every module type to its commands; the integer `com` **is** the function code
placed in the telegram content byte. Key output commands (derived; encoded in
`Sources/Client/PHCFunctions.swift`):

| Module type (`defaultCSType`) | Command | `com` |
|-------------------------------|---------|-------|
| AMD24_AUS (Light), UTM_AUS    | ON      | `2`   |
| AMD24_AUS (Light)             | OFF     | `3`   |
| EMD24_LED (LED)               | ON / OFF| `2` / `3` |
| DIM_AUS (Dimmer)              | ON / OFF (+ ramp variants 4–25) | `2` / `3` |
| JRM_AUS (Shutter)             | up / down / stop (parameterised, `com` 2–15) | see file |

The telegram content byte is `(channel << shift) | com` (shift = 5 for
AMD/DIM/JRM, 4 for EMD-LED), matching §2.

### Resolved by the capture
The earlier open questions are now answered:
- **Port:** 6680 (as documented). **Transport:** HTTP `POST /`, not raw TCP.
- **Discovery:** not needed — the user enters the STM's IP on the connection screen.
- **Auth:** none on the LAN; no `connect`/`activate`.
- **Param types/order:** plain `<i4>` ints; `sendTelegram` is
  `(stm_idx, module_bus_addr, content_byte)`, *not* a raw byte array — the STM
  frames the bus telegram itself. Shutters use `simInputEvent`, not JRM telegrams.

These findings are implemented in `Sources/Client/STMv3Client.swift`.

---

## 2. PHC bus protocol (STM ⇄ modules, RS-485) — the telegram payload

Fully reverse-engineered by the community and implemented in
[ESPHome-PHC-Controller](https://github.com/TillFleisch/ESPHome-PHC-Controller).
**Reference only for this app:** over XML-RPC the STM frames these telegrams
itself — the app supplies just the address + content byte to `sendTelegram`, and
drives shutters via `simInputEvent` rather than JRM telegrams. The section
explains the bytes the STM emits and the `com` codes the content byte reuses.
(`Sources/Client/PHCTelegram.swift` builds full frames for reference/testing.)

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
  content), appended low byte first. (Implemented + verified in
  `Sources/Client/PHCTelegram.swift`.)

### Module classes (the 3 high bits of the address byte)
| Class            | Address bits | Role                                   |
|------------------|--------------|----------------------------------------|
| EMD (input)      | `0x00`       | 16 inputs (buttons) / 8 LED outputs    |
| AMD (output)     | `0x40`       | 8 relay outputs (lights, outlets)      |
| JRM (shutter)    | `0x40`*      | 4 shutter/blind channels               |

\* AMD and JRM share the `0x40` class prefix; distinguished by project config.

### Commands (content byte semantics)
- **AMD output** (content length 1): `(channel << 5) | fn`, `fn` = `0x02` ON,
  `0x03` OFF (matches `functions.xml` `com` 2/3).
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
