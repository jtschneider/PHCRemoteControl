# CLAUDE.md

Project memory & status for **PHC Remote Control** — a modern iOS app to control
a PEHA/Honeywell PHC installation over the LAN, replacing the aging official
*PHC Home Control* app (which works but isn't built for larger displays).

> Read this first when resuming. Deep protocol detail lives in
> [docs/PROTOCOL.md](docs/PROTOCOL.md); architecture in
> [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## TL;DR of the situation

- The user's control unit is an **STM v3** on the LAN. The app talks to it over
  IP — it never touches the RS‑485 bus. (No ESP32 bridge needed.)
- The STM speaks **XML‑RPC over plain HTTP**, default port **6680**.
- The user connects by **typing the STM's IP** (their old app works that way), so
  **LAN auto‑discovery is optional** — we don't need to reverse it to ship.
- We have fully reverse‑engineered the protocol from the PHC Systemsoftware
  (decompiled **V2.70** native `iserver.exe` and **V3.0** Java
  `toolframework.jar` + `rs232_interface.exe` + `functions.xml`).
- The app **runs today** end‑to‑end against an in‑memory mock; the real
  transport (`STMv3Client`) is ~90% specified and stubbed, pending one packet
  capture to confirm wire details.

## What's done

- ✅ SwiftUI app skeleton (iPhone + iPad, iOS 17+), transport‑agnostic.
  Adaptive `NavigationSplitView`: rooms sidebar + device cards (lights, dimmers,
  outlets, shutters). Builds via XcodeGen (`project.yml`).
- ✅ `PHCClient` protocol boundary; `MockPHCClient` drives the whole app with a
  sample home + simulated shutter travel.
- ✅ `@Observable HomeStore` with optimistic updates + live event stream.
- ✅ `PHCTelegram.swift` — CRC‑16/X.25 (verified against the `0x906E` check
  value) and AMD/EMD/DIM/JRM telegram builders.
- ✅ `PHCFunctions.swift` — function/`com` codes from `functions.xml`.
- ✅ `STMv3Client.swift` — full XML‑RPC method set + connect→activate→getState
  flow, builds correct telegrams; only the HTTP/XML‑RPC wire calls remain.
- ✅ Protocol fully documented in `docs/PROTOCOL.md`.

## Protocol summary (confirmed)

- **Transport:** XML‑RPC over HTTP, default TCP port **6680** (GNU CommonC++).
- **Method set:** `service.stm.{connect, activate, getModule, getState,
  sendTelegram, getVersion, getVoltage, getClock/setClock, getProgress, sendPOR,
  sendSinglePOR, writeBinary, deleteBinary, checkBinaryCRC, setStandardText}`,
  `service.module.{writeProject, firmwareUpdate(AES)}`, `iserver.{ping,
  getVersion, getPath, shutdown}`.
- **Control flow:** `connect` → `activate` (required — else `VisuNotActivated`
  fault) → `getModule`/`getState` to build UI → `sendTelegram` to control →
  async STM events report state changes back.
- **Payload:** `sendTelegram` carries a raw PHC bus telegram —
  `address(class<<5|dip)`, `toggle<<7|length`, content, CRC‑16/X25 (LE). Light
  ON `com=2` / OFF `com=3`; content byte `= (channel<<5)|com` (shift 4 for
  EMD‑LED). See docs/PROTOCOL.md §2.
- **Connection model:** STM has `ip`/`mac`/`serial`, `connectedVia`
  {LAN,USB,Gateway,RS232}, version V2/V3, up to 3 STMs per project.

## Remaining unknowns (need ONE packet capture to finish)

These live only in the main `PHC Systemsoftware V3.0.exe` (not yet decompiled):

1. The exact **TCP port** the STM v3 firmware listens on (6680 is the default).
2. **Auth** — what `connect`/`activate` take (project password?).
3. The exact **XML‑RPC parameter encoding** for `sendTelegram` / `getState` /
   `getModule` (telegram likely a base64 string or int array).
4. (Optional) LAN **discovery** payload — not required since the user enters the
   STM IP manually.

## NEXT STEP — capture the iPhone↔STM traffic from the Mac

The official app uses **plain HTTP XML‑RPC**, so an HTTP proxy reads the bodies
directly. We do **not** need `rvictl` (it requires full Xcode; the user only has
Command Line Tools → it fails with `bootstrap_look_up(): 1102`).

### Procedure (mitmproxy as a Wi‑Fi proxy)

1. **Install:** `brew install mitmproxy`
2. **Mac IP + start capture to a file:**
   ```sh
   ipconfig getifaddr en0          # Mac's Wi‑Fi IP (try en1 if blank)
   mitmdump -p 8080 -w ~/Desktop/phc.flows
   ```
   Leave it running.
3. **iPhone → Settings → Wi‑Fi → (ⓘ) → Configure Proxy → Manual:**
   - Server = Mac IP from step 2, Port = `8080`, Authentication off → Save.
4. **Drive the official PHC app:** open it, let it connect to the STM, toggle one
   light on/off, move one shutter. Wait a few seconds.
5. **Stop:** Ctrl‑C the mitmdump window; set iPhone **Configure Proxy → Off**
   (or the phone loses internet once mitmproxy is closed).
6. **Hand off `~/Desktop/phc.flows`** for analysis.

**Watch the mitmdump terminal while tapping:** you should see requests to the
STM's IP (likely port `6680`).
- If they appear → success, capture is good.
- If the app works but nothing shows for the STM → it bypasses the proxy
  (raw sockets). Fallback: install full Xcode, then
  `sudo xcode-select -s /Applications/Xcode.app/Contents/Developer` and use
  `rvictl -s <UDID>` + `sudo tcpdump -i rvi0 -w ~/Desktop/phc.pcap` instead.

### Get the iPhone UDID (if needed for the rvictl fallback)
Finder → iPhone in sidebar → click the small grey text under the device name to
cycle it to the **UDID** → right‑click Copy. (`xcrun xctrace list devices` needs
full Xcode.)

## After the capture

1. Decode port + auth + `sendTelegram`/`getState` param formats from `phc.flows`.
2. Implement the XML‑RPC wire calls in `STMv3Client` (connect→activate→getState,
   `sendTelegram(bytes)`, event handling).
3. Add a connection settings screen (STM IP + password) and a toggle to switch
   `HomeStore` from `MockPHCClient` to `STMv3Client`.
4. Map a real project (`getModule`/`getState`) into rooms/devices.

## Build / run

```sh
brew install xcodegen   # one time
xcodegen generate && open PHCRemoteControl.xcodeproj
```
Runs against the mock with no hardware. Source under `Sources/`; the app can't be
compiled in the cloud Linux env — build on the Mac.

## Reference material (local only, NOT committed — proprietary)

Decompiled/extracted under `/tmp` during the session (gone after the container is
reclaimed): PHC Systemsoftware V2.70 `iserver.exe`, V3.0 `toolframework.jar`
(Procyon‑decompiled), `rs232_interface.exe`, `functions.xml`, `modules.xml`. The
ESPHome‑PHC‑Controller repo is the open reference for the RS‑485 bus protocol.
</content>
