# CLAUDE.md

Project memory & status for **PHC Remote Control** — a modern iOS app to control
a PEHA/Honeywell PHC installation over the LAN, replacing the aging official
*PHC Home Control* app.

> Deep protocol detail lives in [docs/PROTOCOL.md](docs/PROTOCOL.md);
> architecture in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## TL;DR of the situation

- Control unit: **STM v3** on the LAN at `192.168.x.x`.
- Transport: **XML-RPC over plain HTTP**, port **6680**, path **`/`**.
- **No authentication required** for LAN connections — confirmed by capture.
- Project structure is downloaded from the STM as a ZIP file and parsed locally.
- App builds and runs in the iOS simulator against the mock client.
- **Blocked:** ZIP decompression fails when running against the real STM;
  fix in progress based on a new proxy capture.

## What's done

- ✅ SwiftUI app (iPhone + iPad, iOS 17+). `NavigationSplitView`: room sidebar
  + device cards. Builds via XcodeGen (`project.yml`). Simulator build works.
- ✅ `ConnectionView` — IP entry screen; stores last IP in `@AppStorage`.
- ✅ `PHCClient` protocol + `MockPHCClient` (full simulated home, shutter travel).
- ✅ `HomeStore` — `@Observable`, optimistic updates, live event stream.
- ✅ `PHCTelegram.swift` — CRC-16/X.25, AMD/EMD/JRM telegram builders
  (used for reference; STM handles framing internally on the wire).
- ✅ `PHCFunctions.swift` — com codes from `functions.xml`.
- ✅ `STMv3Client.swift` — **fully wired**:
  - `connect()` → `service.stm.whoAreYou`
  - `loadProject()` → `readFile` loop → ZIP reassembly → ppfx parse
  - `setPower()` → `sendTelegram(0, amdBusAddr, (ch<<5)|com)`
  - `moveShutter()` → `simInputEvent` with EMD up/down refs
  - State polling loop (AMD modules, 1 s interval)
- ✅ `PHCProjectParser.swift` — parses `project.ppfx` XML into `PHCProject`
  with rooms, lights, outlets, shutters.
- ✅ Project files extracted and committed under `project/` for reference
  (`project.ppfx`, `project.tpfx`, `project.cpfx`).

## Wire protocol — fully confirmed by mitmproxy capture

### Transport
- `POST http://192.168.x.x:6680/ HTTP/1.1`
- `Content-Type: application/x-www-form-urlencoded` (body is XML-RPC)
- STM responds with `HTTP/1.0 200 OK` and a non-standard `Date` header line
  (causes mitmproxy to reject without `--set validate_inbound_headers=false`).

### Startup sequence (no auth)
```
service.stm.whoAreYou()
  → {STM-Address:0, Facility-ID:"...", Device-ID:"[redacted-device-id]", Device-Name:"Steuermodul 0"}

service.stm.readFile(0, 0, 1)   ← chunk 0
  → {cur:0, total:2, crc:63006, bin:<base64 ZIP chunk>}

service.stm.readFile(0, 1, 1)   ← chunk 1
  → {cur:1, total:2, crc:36691, bin:<base64 ZIP chunk>}
```
Concatenate the two base64-decoded blobs → ZIP archive containing:
- `project.ppfx` — hardware config XML (modules, channels, `visu="true"` flags)
- `project.tpfx` — automation logic XML (tools, input→output mappings)
- `project.cpfx` — comfort/UI groupings XML

### State polling (AMD modules only)
```
sendTelegram(stm_idx=0, module_bus_addr, content=1)
  → [0, addr, toggle_echo, ?, state_bitmask]
```
`state_bitmask` has bit N set when output channel N is active.
AMD bus address = `0x40 | dip`. Polled addresses observed: 64–78.

### Light / outlet control
```
sendTelegram(0, 0x40|dip, (channel<<5)|com)
```
`com`: 2 = ON, 3 = OFF, 6 = toggle.

### Shutter control (via input simulation)
```
simInputEvent(stm=0, emd_module, channel, event_type, key_type=4)
```
Param layout confirmed by capturing the official app on two shutters
(shutter A → module 2, channels 4/5; shutter B → module 3, channels 10/11):
- `emd_module` = EMD module adr (raw ppfx `MOD adr`).
- `channel`    = EMD channel adr (`CHA adr`).
- `key_type`   = constant **4** (EMD_RUE rocker input).

Events: 2 = press, 3 = long-press, 4 = release, 5 = doublePress ("click confirmed").
A long hold starts movement; a short tap (which the firmware reports as
press→release→doublePress) halts the motor in either direction.
- **Lower (down):** press(2) + longPress(3) on the `senken` channel.
- **Raise (up):**   press(2) + longPress(3) on the `heben` channel.
- **Stop:**         press(2) + release(4) + doublePress(5) on the `senken` channel.

### XML-RPC encoding
- **Request:** standard `<methodCall>` XML, params as `<i4>` integers.
- **Response for sendTelegram:** `<array>` of 5 `<i4>` values.
- **Response for readFile:** `<struct>` with `cur`, `total`, `crc` (`<i4>`),
  and `bin` (`<base64>`).
- **Fault:** `<fault>` with `<string>` message.

## Project file structure (`project.ppfx`)

XML schema:
```
<PROJECT name="[redacted-project]" ver="3.2.8">
  <STM adr="0" ver="V3">
    <MODS grp="Eingangsmodule">      ← EMD input modules (adr 0–N)
      <MOD adr="N" name="EMD_RUE">
        <CHAS grp="Eingang">
          <CHA adr="C" visu="true">FLOOR : Rollo > NAME heben/senken</CHA>
    <MODS grp="Ausgangsmodule">      ← AMD/JRM output modules (adr 0–N)
      <MOD adr="N" name="AMD230_16|AMD230_4|JRM">
        <CHAS grp="Ausgang">
          <CHA adr="C" visu="true">FLOOR : Licht/Steckdose > NAME</CHA>
```

Channel name convention: **`"N.ROOM : TYPE > LABEL"`**
- N = sort index (0 = KG, 1 = Einlieger, 2 = EG, 3 = DG, 4 = Außen)
- TYPE → DeviceKind: `Licht`→light, `Steckdose`→outlet, `Rollo`→shutter

Module bus address:
- AMD: `0x40 | adr`
- JRM: `0x60 | adr` (not polled; shutters controlled via simInputEvent)
- EMD: `adr` (used only in simInputEvent, not sendTelegram)

Actual modules in this installation:
```
Ausgangsmodule:
  adr 0  AMD230_4   → bus 64   (2.EG lights/living)
  adr 1  AMD230_16  → bus 65   (2.EG lights/guest/office/bath)
  adr 2  AMD230_16  → bus 66   (2.EG lights/outdoor/outlets)
  adr 3  AMD230_16  → bus 67   (2.EG outlets + reserve)
  adr 4  JRM        → bus 100  (shutters)
  adr 5  JRM        → bus 101  (shutters)
  adr 6  JRM        → bus 102  (shutters)
  adr 7  AMD230_4   → bus 71   (3.DG lights)
  adr 8  AMD230_16  → bus 72   (3.DG lights)
  adr 9  AMD230_16  → bus 73   (3.DG outlets)
  adr 10 JRM        → bus 106  (shutters)
  adr 11 JRM        → bus 107  (shutters)
  adr 12 AMD230_4   → bus 76   (1.Einlieger lights)
  adr 13 AMD230_16  → bus 77   (1.Einlieger lights)
  adr 14 AMD230_16  → bus 78   (0.KG pump + reserve)
```

## NEXT STEP — fix ZIP decompression

The `inflateDeflate` helper in `STMv3Client.swift` prepends a zlib header
(`0x78 0x9C`) to the raw deflate stream before calling
`NSData.decompressed(using: .zlib)`. This may be wrong if the ZIP uses
a different deflate variant or the Adler-32 checksum appended is invalid.

A new proxy capture of the **new app** (not the old one) against the STM has
been taken and will be analysed next to verify the exact ZIP bytes and fix
the decompressor.

**After fixing decompression:**
1. Test `loadProject` on real device — should produce real rooms/devices.
2. Wire up start-polling after project load.
3. Test `setPower` / `simInputEvent` shutter control on real hardware.
4. Add shutter state polling (EMD modules return position somehow — TBD).
5. Persist STM IP properly; add disconnect/reconnect flow.

## Build / run

```sh
brew install xcodegen   # one time
xcodegen generate && open PHCRemoteControl.xcodeproj
```

Select an iPhone simulator → ⌘R. Runs against `MockPHCClient` with no hardware.

**To run on real iPhone:** Xcode → target → Signing & Capabilities →
Automatically manage signing → set Team to your Apple ID → select iPhone.

## Reference files (local, committed)

- `project/project.ppfx` — hardware module/channel config for this installation
- `project/project.tpfx` — automation logic (input→output tool mappings)
- `project/project.cpfx` — comfort UI groupings
- `project.zip` — raw ZIP from STM readFile (the two base64 chunks combined)
