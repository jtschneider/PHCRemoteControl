# AGENTS.md

Project memory and current status for **PHC Remote**, a replacement for the
aging official *PHC Home Control* app. The repository now contains two usable
clients for a PEHA/Honeywell PHC installation:

1. A native SwiftUI app for iPhone and iPad.
2. A Go website bridge intended to run on a Raspberry Pi and serve ordinary
   browsers on the trusted home LAN.

> Deep protocol detail lives in [docs/PROTOCOL.md](docs/PROTOCOL.md);
> architecture in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md); dimmer research
> in [docs/DIMMERS.md](docs/DIMMERS.md); and the implemented Go bridge design in
> [docs/GO_WEBSITE_BRIDGE_PLAN.md](docs/GO_WEBSITE_BRIDGE_PLAN.md). The earlier,
> broader PWA/proxy exploration remains in [docs/GO_BRIDGE_PLAN.md](docs/GO_BRIDGE_PLAN.md).

## TL;DR

- Control unit: **STM v3**, TCP port **6680**, path **`/`**.
- Transport: XML-RPC over plain HTTP, with no STM authentication.
- The **iOS app** communicates directly with the STM and is working end to end
  on real hardware.
- The **Go bridge** implements all six planned phases: malformed STM HTTP
  handling, project download/extraction/parsing, control and polling, website
  and API, persistent cache, and hardened `systemd` packaging.
- Both clients expose lights, outlets, motor controls, scenes/tools, and unknown
  visible EMD inputs. Both have English/German UI chrome, favourites, and
  confirmation before panic/security actions.
- Dimmers remain unsupported on real hardware because this installation has no
  dimmer modules with which to verify addressing, payload scaling, and feedback.
- The Go website has deliberately **no user authentication**. Any device that
  can reach its port can inspect and control the installation. The deployment
  assumes a trusted LAN and must restrict the listen port to that LAN.

## Repository map

- `Sources/` - Swift models, STM client, parser, store, and SwiftUI views.
- `Tests/PHCRemoteControlTests/` - iOS fixture/parity tests.
- `bridge/` - standalone Go module (`go 1.26`) for the Raspberry Pi website.
- `protocol-fixtures/` - synthetic parser and command contracts consumed by
  both Go and Swift tests.
- `docs/` - protocol findings, architecture, dimmer notes, and bridge plans.
- `project/`, `setup_PHC/`, captures, and real STM exports are private,
  gitignored research inputs and must never be committed.

## Native iOS app

- SwiftUI, iOS 17+, iPhone and iPad; generated with XcodeGen from `project.yml`.
- `ConnectionView` accepts the STM address, remembers it with `@AppStorage`, and
  offers real connection or Demo Mode.
- `STMv3Client` implements `whoAreYou`, bounded chunked project download,
  ZIPFoundation extraction, PPFX/selected-TPFX parsing, commands, and AMD polling.
- `PHCProjectParser` supports:
  - visible AMD lights and outlets, with unknown AMD categories falling back to
    light-style control;
  - paired EMD shutters and mechanically driven windows, retaining separate
    raise/lower channel references;
  - EMD virtual/central scenes;
  - selected TPFX panic and presence-simulation tools;
  - every otherwise-unclassified visible EMD input as a fallback button with
    short-press and long-press actions.
- Project text follows `N.FLOOR : CATEGORY > LABEL`. Everything between `:` and
  `>` is retained verbatim as the category and becomes a floor-list section.
- `HomeStore` is `@Observable` and `@MainActor`; it folds live events, performs
  optimistic light/outlet updates, persists ordered per-host favourites, and
  debounces project-cache writes.
- `ProjectCache` stores the parsed project in Application Support, keyed by STM
  host, and marks both the directory and files as excluded from iCloud backup.
- Navigation is adaptive: `NavigationStack` on iPhone and
  `NavigationSplitView` on iPad.
- Device cards support lights/outlets, shutters/windows, optional experimental
  jalousie tilt, scenes, fallback buttons, and favourites. Panic/security
  actions require a destructive confirmation.
- Favourites are pinned on the overview and can be reordered.
- Categories are collapsible, with Expand All and Collapse All.
- UI chrome and network/parser errors are localized in English and German.
  Project-provided floor, category, and device names are not translated.
- The app icon and load-screen logo are in `Sources/Assets.xcassets`.
- The iOS test target executes the shared parser/command fixtures so Swift and
  Go behavior cannot silently drift.

## Go website bridge

`bridge/README.md` is the operational guide. The implementation covers Phases
1-6 of `docs/GO_WEBSITE_BRIDGE_PLAN.md`.

### Transport and project

- `internal/stm/transport.go` wraps `net.Conn` and sanitizes the STM's malformed
  HTTP response header before Go's `net/http` parser sees it.
- `internal/stm/xmlrpc.go` is a deliberately small typed XML-RPC codec for the
  value shapes used by the STM.
- `internal/stm/client.go` provides typed `WhoAreYou`, `ReadFile`,
  `SendTelegram`, and `SimInputEvent` calls with response-size and time bounds.
- `internal/project/download.go` bounds chunk count and total archive size,
  reconstructs the unusual base64 ZIP, and uses the ZIP central directory.
- `internal/project/parser.go` and `tools.go` provide Swift-parity bounded
  PPFX/selected-TPFX parsing with stable hardware-derived device IDs.
- CPFX is extracted but not parsed. Real CPFX data is largely typeless and there
  is no Swift reference implementation; CPFX remains research, not a parity goal.

### Controller and concurrency

- One scheduler goroutine is the sole owner of STM calls.
- User commands and background work have separate buffered queues. Queued
  commands are selected before another poll, but do not interrupt an operation
  already in flight.
- A full rocker event sequence is one scheduled operation, so a poll cannot
  split a shutter/window/scene command.
- Project reload schedules each `readFile` chunk separately, allowing user
  commands to run between chunks.
- A separate polling goroutine performs AMD module sweeps only while SSE
  subscribers exist, plus a grace period after the last disconnect. Optional
  idle health polling is disabled by default.
- Revisioned snapshots and best-effort deltas feed SSE subscribers. Slow clients
  detect revision gaps and refresh the full snapshot.
- Mutexes protect state, subscribers, backend replacement, reloads, and project
  notifications; contexts, `WaitGroup`, `Once`, and atomics provide bounded
  cancellation and shutdown.
- `net/http` handles browser requests concurrently, while STM access remains
  serialized. HTML rendering, JSON work, SSE streams, and project parsing may
  run concurrently or in parallel on separate cores.

### Website and API

- Embedded `html/template` pages and assets; no Node.js runtime or external CDN.
- Routes: `/`, `/floors/{floorID}`, `/settings`, and `/acknowledgments`.
- Responsive English/German UI with light/dark styling and semantic controls.
- Vanilla JavaScript handles commands, a single SSE stream, revision recovery,
  category persistence, and browser-local favourites.
- Favourites persist in `localStorage` and support pointer/touch drag reordering
  plus accessible move-earlier/move-later buttons.
- Internal links are prefetched and swap the server-rendered `<main>` content;
  the Go template remains the only structural renderer. Live SSE state is
  reapplied after swaps, and Back/Forward Cache restores reopen SSE cleanly.
- JSON/SSE API under `/api/v1`:
  - `GET /status`, `/project`, `/state`, and `/events`;
  - `POST /project/reload`;
  - `POST /devices/{stableDeviceID}/commands`.
- Commands are capability checked: on/off/toggle; raise/stop/lower;
  experimental tilt open/close; scene activation; fallback short/long press.
- Panic/security controls require browser confirmation.
- Mutation requests require the exact configured `Origin`, JSON content type,
  and a non-cross-site fetch. These checks address browser CSRF/rebinding; they
  are not authentication against another LAN device.

### Startup and deployment

- `internal/cache` atomically stores a mode-0600, STM-keyed parsed-project cache.
- `cmd/phc-bridge` serves immediately from cache, or an empty shell, then loads
  a fresh project in the background with bounded exponential backoff.
- STM unavailability does not crash the service.
- `cmd/stm-probe` provides redacted who-am-I, project, and read-only state
  diagnostics without printing installation names or identity.
- `packaging/phc-bridge.service` runs as the unprivileged `phc-bridge` user with
  a read-only filesystem except systemd's `/var/lib/phc-bridge`
  `StateDirectory`.
- Default bridge port is 8080. It coexists with CUPS on port 631.
- Deployment is a static `linux/arm64` binary at
  `/usr/local/bin/phc-bridge`, configured by
  `/etc/phc-bridge/bridge.env`.

## Confirmed STM protocol

### Transport

```text
POST http://<host>:6680/ HTTP/1.1
Content-Type: application/x-www-form-urlencoded
```

The body is XML-RPC despite the form content type. The STM replies with
HTTP/1.0 and an invalid/non-standard date header line. Swift's URL loading stack
tolerates it; Go requires the sanitizing connection wrapper.

### Startup and project archive

```text
service.stm.whoAreYou()
  -> {STM-Address, Facility-ID, Device-ID, Device-Name}

service.stm.readFile(0, chunkIndex, 1)
  -> {cur, total, crc, bin:<base64 ZIP chunk>}
```

Concatenate decoded `bin` chunks. The ZIP contains `project.ppfx`,
`project.tpfx`, `project.cpfx`, and a `.facl` file.

ZIP local headers set general-purpose flag bit 3, so local compressed and
uncompressed sizes are zero; actual sizes are represented by descriptors and
the central directory. Entries use raw DEFLATE. Swift uses ZIPFoundation; Go
uses central-directory ZIP handling. The STM's `crc` struct member is ignored;
ZIP entry CRCs provide archive integrity.

### Light and outlet control

```text
sendTelegram(0, 0x40 | moduleAddress, (channel << 5) | command)
```

Commands currently used:

```text
2  on
3  off
6  toggle
```

### AMD state polling

```text
sendTelegram(0, moduleBusAddress, 1)
  -> [0, address, toggleEcho, unknown, stateBitmask]
```

Bit `N` in `stateBitmask` is output channel `N`. One call reads all channels in
an AMD module. Only AMD light/outlet state is pollable.

### EMD rocker simulation

```text
simInputEvent(stm=0, emdModule, channel, eventType, keyType=4)
```

Events:

```text
2  press
3  longPress
4  release
5  doublePress
```

Verified behavior on the real installation:

```text
short press = press(2), release(4), doublePress(5) -> start/activate
long press  = press(2), longPress(3)               -> stop/hold path
tip         = press(2), release(4)                 -> experimental jalousie nudge
```

For paired motors, the lower/close channel is the primary reference and the
raise/open channel is the secondary reference. Stop uses the lower reference.
Shutters and motorized windows expose no position or movement feedback.

## Parsing contract

Typical PPFX names:

```text
N.FLOOR : CATEGORY > LABEL
```

- `N` controls floor order.
- `FLOOR` becomes the top-level floor list.
- `CATEGORY` is retained verbatim for grouping.
- `LABEL` is retained for display, except directional suffixes removed when two
  motor channels are paired.
- Recognized light/outlet words influence presentation, and motor words plus
  directional suffixes pair shutters/windows.
- Unknown visible AMD outputs fall back to light-style controls.
- Unknown visible EMD inputs remain separate fallback buttons; their labels are
  not stripped of raise/lower words.
- Stable Go IDs derive from hardware addresses. Swift favourites use a stable
  hardware-derived key because Swift model UUIDs are regenerated on parsing.

## Security and privacy

- The STM and Go website use plain HTTP on the trusted LAN.
- There is no STM authentication and no bridge user authentication.
- Host, Origin, fetch-metadata, JSON-only mutation, CSP, framing, and body-size
  checks reduce browser-origin attacks but do not stop a device with direct LAN
  access.
- Panic/security actions have an explicit confirmation in both UIs.
- Project download, ZIP extraction, XML parsing, labels, tool candidates,
  request bodies, and STM responses have sanity bounds.
- iOS cached project data is excluded from iCloud backup.
- Go cache files are private to the service account and written atomically.
- Never commit real exports, captures, STM identity, addresses, cache files, or
  copied deployment environment files such as `bridge.env.backup`.

## Current limitations

1. **Real dimmers are unsupported.** Demo mode has a slider, but production
   parsers do not create dimmer devices and real brightness control is not
   hardware verified.
2. Shutters, windows, scenes, panic actions, presence simulation, and fallback
   inputs have no readable state. The UI can only acknowledge command dispatch.
3. Jalousie tilt is inferred from naming and remains experimental.
4. The Go command bytes and sequencing are covered by shared fixtures and match
   the hardware-verified Swift implementation. Read-only Go transport, project,
   and AMD polling have been exercised against the real STM; deliberately
   selected live Go command targets and Raspberry Pi latency remain deployment
   checks.
5. There is no off-LAN access. Adding it requires a separate authentication and
   secure-access design rather than exposing the trusted-LAN HTTP service.
6. CPFX has no implemented semantic parser.
7. The iOS client does not explicitly suspend polling on app backgrounding;
   client destruction cancels it.

## Build and verify

### iOS

```sh
brew install xcodegen
xcodegen generate
open PHCRemoteControl.xcodeproj
```

Xcode resolves ZIPFoundation. Run the `PHCRemoteControl` scheme on an iPhone or
iPad simulator for Demo Mode, or select a signing team and physical device for
the STM. The same scheme includes `PHCRemoteControlTests`.

### Go

```sh
cd bridge
go test -race -count=1 ./...
go vet ./...
go build ./...
```

Cross-compile for the 64-bit Raspberry Pi installation:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
  -o dist/phc-bridge-linux-arm64 ./cmd/phc-bridge
```

Do not commit `bridge/dist`, deployment environment files, real project data,
or generated Xcode projects.
