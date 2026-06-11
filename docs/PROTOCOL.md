# PHC protocol notes

Everything we know about talking to a PEHA/Honeywell PHC system. Two completely
different protocols live here — don't confuse them:

1. **STM-IP protocol** — between *this app* and the **STM** over the LAN. The one
   we need. **Now well understood** from decompiling the PHC Systemsoftware (§1).
2. **PHC bus protocol** — between the STM and the modules over **RS-485**. Fully
   understood (§2). The app never speaks it directly, but it is the *payload*
   carried inside the STM-IP `sendTelegram` calls.

Sources: decompiled **PHC Systemsoftware V2.70** (2010, native `iserver.exe`) and
**V3.0** (2013/14, Java `toolframework.jar` + `rs232_interface.exe`), plus the
shipped `functions.xml` / `modules.xml`, cross-checked against
[ESPHome-PHC-Controller](https://github.com/TillFleisch/ESPHome-PHC-Controller).

---

## 1. STM-IP protocol (app ⇄ STM) — XML-RPC

### Transport
- **XML-RPC over TCP** (GNU CommonC++ `ost::XMLRPC`; the V3 sources reference
  `PhcXmlRpcClient.cpp` / `XmlRpcServer.cpp`).
- Default port **6680** (*"Socket-Port for XML-RPC (default: 6680)"*). The local
  `iserver`/`rs232_interface` bridge listens here; the STM v3 firmware exposes
  the same surface over the LAN (still to be 100%-confirmed for port — see
  "remaining unknowns").
- Standard `<methodCall>`/`<methodResponse>` envelope; params are
  string/int/double/boolean/dateTime/array/struct (`addParam*`, `begArray`,
  `endStruct`, `addParamDateTime`). There's also a **keep-alive timer** on the
  connection.

### Full XML-RPC method set (verbatim from the V3 binary)
| Method                          | Purpose                                              |
|---------------------------------|------------------------------------------------------|
| `service.stm.connect`           | Open/initialise the STM connection                   |
| `service.stm.activate`          | **Activate visualisation** (required before control — see `VisuNotActivated`) |
| `service.stm.getModule`         | Enumerate modules present on the STM                 |
| `service.stm.getState`          | **Read current channel states** (build/refresh UI)   |
| `service.stm.sendTelegram`      | **Send a raw PHC bus telegram to a module** (core control) |
| `service.stm.getVersion`        | STM firmware version                                 |
| `service.stm.getVoltage`        | Bus voltage                                          |
| `service.stm.getClock` / `setClock` | Real-time clock                                  |
| `service.stm.getProgress`       | Progress of a long operation                         |
| `service.stm.sendPOR` / `sendSinglePOR` | Send POR (config) table / single entry       |
| `service.stm.writeBinary` / `deleteBinary` / `checkBinaryCRC` | Project binary up/down |
| `service.stm.setStandardText`   | Set display text                                     |
| `service.module.writeProject`   | Write a project to a module                          |
| `service.module.firmwareUpdate` / `…AES` | Module firmware update                       |
| `iserver.ping` / `getVersion` / `getPath` / `shutdown` | Server utilities             |

### Control flow (how the app drives the house)
1. `service.stm.connect` — attach to the control unit.
2. `service.stm.activate` — enable the visualisation session (otherwise calls
   fault with `VisuNotActivated`).
3. `service.stm.getModule` + `service.stm.getState` — enumerate modules and read
   channel states to build the UI.
4. Control: `service.stm.sendTelegram` with a PHC bus telegram (§2) addressed to
   the AMD / DIM / JRM channel.
5. Live updates: the STM reports async **events** (button presses, state
   changes) which the client folds into state. (Exact delivery — server-pushed
   methodCall vs. polling `getState` — to confirm via capture.)

### XML-RPC fault codes (`enums/EErrorCode`)
`MethodNotFound`, `ParameterCount`, `ExecutionFailed`, `UnregisteredBusAdr`,
`RequestedParamEmpty`, `ParameterOutOfRange`, `FeatureNotSupported`,
`ModuleNotFound`, `RequestTimeout`, `STMNotConnected`, **`VisuNotActivated`**,
`MethodCallFailed`, `UnexpectedEndOfFile`, `WriteMCCError`,
`CommandNotProgrammed`, `WriteFUIError`.

### Connection model (`interfaces/IAnlagenSTMDetails`)
Each STM in a project carries: `ip`, `mac`, `serial`, `phcIP`, a
`connectedVia` ∈ {**LAN**, USB, Gateway, RS232} and a `phcKommunikation` ∈
{**LAN**, RS485}, plus gateway chaining (one STM can be the gateway for others —
the app supports up to 3 STMs per project). STM version is `V2`/`V3`
(`enums/ESTMVersion`). Discovery populates `ip`/`mac` automatically.

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

### Remaining unknowns (need a capture or the main-app classes)
The framework jar is the tool-plugin SDK; the **XML-RPC client + discovery +
auth live in the main `PHC Systemsoftware V3.0.exe`** (not uploaded). Still to
pin down for the STM v3 over LAN:
- the **exact TCP port** the STM v3 firmware listens on (6680 is the documented
  default),
- **LAN discovery** (the app auto-finds `ip`/`mac` — almost certainly a UDP
  broadcast; need port + payload),
- **auth** — what `connect`/`activate` take (project password?),
- the **exact XML-RPC parameter order/types** for `sendTelegram`, `getState`,
  `getModule` (telegram likely passed as a base64 string or int array).

**Fastest way to finish:** a 2-minute packet capture of the official iOS app
(launch + one light on/off) nails all four at once. Alternatively, upload the
main `PHC Systemsoftware V3.0.exe` and we decompile its `PhcXmlRpcClient`.

These findings are encoded in `Sources/Client/STMv3Client.swift`.

---

## 2. PHC bus protocol (STM ⇄ modules, RS-485) — the telegram payload

Fully reverse-engineered by the community and implemented in
[ESPHome-PHC-Controller](https://github.com/TillFleisch/ESPHome-PHC-Controller).
This is exactly what rides inside `service.stm.sendTelegram`.

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
</content>
