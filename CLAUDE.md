# CLAUDE.md

Project memory & status for **PHC Remote** — a modern iOS app to control a
PEHA/Honeywell PHC installation over the LAN, replacing the aging official
*PHC Home Control* app.

> Deep protocol detail lives in [docs/PROTOCOL.md](docs/PROTOCOL.md);
> architecture in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## TL;DR of the situation

- Control unit: **STM v3** on the LAN (host entered on the connection screen),
  port **6680**, path **`/`**.
- Transport: **XML-RPC over plain HTTP**. **No authentication** for LAN.
- **Working end to end on real hardware:** the app connects, downloads & parses
  the project, lists floors/devices, controls **lights, outlets, and shutters**,
  and polls light/outlet state. Project is cached on disk for instant relaunch.
- App is **bilingual** (English + German, menu labels only) and ships an icon.

## What's done

- ✅ SwiftUI app (iPhone + iPad, iOS 17+). Builds via XcodeGen (`project.yml`);
  one SPM dependency, **ZIPFoundation**, auto-resolves.
- ✅ **Adaptive navigation** — iPhone uses a `NavigationStack` (floor overview →
  pushes to a floor's devices, classic slide); iPad uses `NavigationSplitView`
  (sidebar + detail). The old single split view misbehaved on iPhone.
- ✅ `ConnectionView` — IP entry screen with the app logo; last IP in
  `@AppStorage`. "Connect to STM" or "Demo Mode".
- ✅ `PHCClient` protocol + `MockPHCClient` (simulated home, shutter travel).
- ✅ `HomeStore` — `@Observable`; optimistic updates, live event stream,
  disk-cached project, debounced cache writes.
- ✅ `STMv3Client.swift` — **fully wired & verified on hardware**:
  - `connect()` → `service.stm.whoAreYou`
  - `loadProject()` → `readFile` loop → ZIP reassembly → **ZIPFoundation** extract → ppfx parse
  - `setPower()` → `sendTelegram(0, 0x40|dip, (ch<<5)|com)`
  - `moveShutterFull()` → `simInputEvent` sequences (see below)
  - `startPolling()` → AMD module state poll, **once per module, every 2.5 s**
- ✅ `PHCProjectParser.swift` — parses `project.ppfx` into `PHCProject`
  (floors → lights/outlets/shutters). Shutters pair heben/senken EMD channels.
- ✅ `ProjectCache.swift` — JSON-persists the project to Application Support,
  keyed by host, so relaunch skips the ZIP download.
- ✅ `FloorView` shows devices grouped into **collapsible category sections**
  (Lights → Shutters → Outlets → …), sorted alphabetically within each; a
  toolbar menu does Expand All / Collapse All.
- ✅ App icon + in-app logo (`Sources/Assets.xcassets`). App display name **"PHC Remote"**.
- ✅ German localization via `Sources/Localizable.xcstrings` (menu labels only).

## Wire protocol — confirmed by mitmproxy capture

### Transport
- `POST http://<host>:6680/ HTTP/1.1`
- `Content-Type: application/x-www-form-urlencoded` (body is XML-RPC)
- STM responds with `HTTP/1.0 200 OK` and a non-standard `Date` header line
  (mitmproxy needs `--set validate_inbound_headers=false`).

### Startup sequence (no auth)
```
service.stm.whoAreYou()
  → {STM-Address:0, Facility-ID:"…", Device-ID:"…", Device-Name:"Steuermodul 0"}

service.stm.readFile(0, chunkIdx, 1)   ← loop until cur == total-1
  → {cur, total, crc, bin:<base64 ZIP chunk>}
```
Concatenate the base64-decoded chunks → ZIP archive containing
`project.ppfx` (hardware config), `project.tpfx` (automation logic),
`project.cpfx` (comfort/UI groupings), and a `.facl` file.

### ZIP extraction (the decompression saga)
The STM's ZIP entries set **general-purpose flag bit 3**, so `cSize`/`uSize` in
the local file headers are **0** (real sizes live in the trailing data
descriptor), and the entries are **raw DEFLATE** (no zlib header/Adler-32).
A hand-rolled parser kept failing (scanning for the `PK\x07\x08` descriptor is
unreliable — that sequence can occur inside deflate data). **Resolved by using
ZIPFoundation**, which reads the central directory at the end of the archive.
See `STMv3Client.extractPPFX`.

### State polling (AMD outputs only)
```
sendTelegram(stm_idx=0, module_bus_addr, content=1)
  → [0, addr, toggle_echo, ?, state_bitmask]
```
`state_bitmask` bit N set ⇒ output channel N is active. Polled once **per AMD
module** (one telegram reads all 16 channels), every **2.5 s**. Shutters (EMD)
and scenes are not polled — they have no pollable on/off state.

### Light / outlet control
```
sendTelegram(0, 0x40|dip, (channel<<5)|com)
```
`com`: 2 = ON, 3 = OFF, 6 = toggle.

### Shutter control (via input simulation)
```
simInputEvent(stm=0, emd_module, channel, event_type, key_type=4)
```
Param layout confirmed on two shutters (module 2 ch 4/5; module 3 ch 10/11):
- `emd_module` = EMD module adr (raw ppfx `MOD adr`).
- `channel`    = EMD channel adr (`CHA adr`).
- `key_type`   = constant **4** (EMD_RUE rocker input).

Events: 2 = press, 3 = long-press, 4 = release, 5 = doublePress.
**Verified on hardware:** a **short tap** (press→release→doublePress) STARTS the
motor in that channel's direction; a **long press** (press→longPress) STOPS it
(no-op when idle).
- **Lower (down):** press(2) + release(4) + doublePress(5) on the `senken` channel.
- **Raise (up):**   press(2) + release(4) + doublePress(5) on the `heben` channel.
- **Stop:**         press(2) + longPress(3) on the `senken` channel.

Shutters have **no position/movement feedback** — the UI shows only a brief
"command sent" indicator (auto-clears ~4 s), not a percentage.

### XML-RPC encoding
- **Request:** standard `<methodCall>` XML, params as `<i4>` integers.
- **Response for sendTelegram:** `<array>` of 5 `<i4>` values.
- **Response for readFile:** `<struct>` with `cur`, `total`, `crc` (`<i4>`), `bin` (`<base64>`).
- **Fault:** `<fault>` with `<string>` message.

## Project file structure (`project.ppfx`)

```
<PROJECT name="…" ver="3.2.8">
  <STM adr="0" ver="V3">
    <MODS grp="Eingangsmodule">      ← EMD input modules (adr 0–N)
      <MOD adr="N" name="EMD_RUE"><CHAS grp="Eingang">
        <CHA adr="C" visu="true">FLOOR : Rollo > NAME heben/senken</CHA>
    <MODS grp="Ausgangsmodule">      ← AMD/JRM output modules (adr 0–N)
      <MOD adr="N" name="AMD230_16|AMD230_4|JRM"><CHAS grp="Ausgang">
        <CHA adr="C" visu="true">FLOOR : Licht/Steckdose > NAME</CHA>
```

Channel name convention: **`"N.ROOM : TYPE > LABEL"`**
- N = sort index (used to order floors).
- TYPE → DeviceKind: `Licht`→light, `Steckdose`/`Pumpe`→outlet, `Rollo`→shutter.

Module bus address:
- AMD: `0x40 | adr`  ·  JRM: `0x60 | adr`  ·  EMD: `adr` (only in `simInputEvent`).

## UI behaviour notes

- **Floors** are the top-level grouping (model type is still `Room`; finer room
  grouping is future work). Floor order comes from the channel-name sort index.
- Within a floor, devices are grouped by category and sorted by name
  (natural/numeric) within each category.
- **Project cache:** used verbatim on launch for instant startup. Structural
  changes are picked up via the **"Reload from STM"** toolbar button.
- **Localization:** only UI chrome is translated. Device & floor *names* are
  project data and stay as-is. German terms: Stockwerke (floors), Deckenlichter
  (lights), Rollläden (shutters), Steckdosen (outlets), hochfahren/herunterfahren
  (open/close shutter).

## Remaining / possible next steps

1. Dimmer brightness read-back (poll gives on/off only; this installation has no dimmers).
2. Background-refresh the project structure (currently manual via "Reload from STM").
3. Scenes / favourites; off-LAN access.
4. Stop polling explicitly on backgrounding (today the client's `deinit` cancels it).

## Build / run

```sh
brew install xcodegen          # one time
xcodegen generate              # regenerate after adding/removing files
open PHCRemoteControl.xcodeproj # Xcode resolves ZIPFoundation automatically
```

Pick an iPhone/iPad simulator → ⌘R (runs against `MockPHCClient`, no hardware).
**Real iPhone:** Xcode → target → Signing & Capabilities → set your Team → select device.
To preview German without changing device language, add `-AppleLanguages (de)`
to the scheme's run arguments.

## Privacy / git note

The real installation export (`project/`, `*.zip`) and proxy captures
(`*.flows`, `phc_*.txt`) are **gitignored** and were purged from history with
`git filter-repo`; the GitHub repo is **private**. Do not commit installation
data. `decode.jl` is a local helper that parses proxy captures (reads files, no
embedded data).
