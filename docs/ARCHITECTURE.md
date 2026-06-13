# Architecture

The (reverse-engineered) STM network protocol is isolated behind a single
`PHCClient` interface, so the model, state, and UI are completely
transport-agnostic. The app runs against the **real STM** (`STMv3Client`) or an
in-memory **mock** (`MockPHCClient`) chosen on the connection screen.

```
            ┌──────────────────────── SwiftUI Views ────────────────────────┐
            │  ConnectionView · HomeView · FloorView · DeviceCard            │
            └───────────────────────────────┬───────────────────────────────┘
                                            │ observes / sends intents
                              ┌─────────────▼─────────────┐
                              │   HomeStore (@Observable)  │  app state + actions
                              └─────────────┬─────────────┘
                                   uses │        │ persists
                                        │        └────────────► ProjectCache (disk)
                              ┌─────────▼─────────────────┐
                              │   PHCClient (protocol)     │  transport boundary
                              └───────┬───────────┬────────┘
                                      │           │
                       ┌──────────────▼──┐   ┌────▼──────────────┐
                       │  MockPHCClient   │   │   STMv3Client     │
                       │  (demo / preview)│   │  (real STM, live) │
                       └──────────────────┘   └───────────────────┘
```

## Layers

### Models (`Sources/Models`)
Plain `Codable` value types, transport-independent:
- `ChannelRef` — `(moduleClass, dip, channel)` address of a controllable point.
- `Device` — a controllable thing (light / dimmer / outlet / shutter / scene)
  with a `DeviceState`. Shutters also carry a `shutterUpRef` (the heben EMD
  channel; `ref` is the senken channel).
- `Room` — a named grouping of devices. **Today these represent floors** (the
  app groups by floor; a finer room split is future work — the type keeps its
  name for now).
- `PHCProject` — the loaded installation: rooms + devices, as handed over by the
  STM. Being `Codable`, it is what gets cached to disk.

### Client (`Sources/Client`)
- `PHCClient` — async protocol: `connect`, `loadProject`, `setPower`,
  `setBrightness`, `moveShutter`, `registerDevices`, `startPolling` /
  `stopPolling`, and an `events` stream of `StateUpdate`s.
- `STMv3Client` — the **real** XML-RPC-over-HTTP transport (see
  [PROTOCOL.md](PROTOCOL.md)). Downloads the project ZIP via chunked `readFile`,
  extracts it with **ZIPFoundation**, parses `project.ppfx`
  (`PHCProjectParser`), drives `sendTelegram` (lights/outlets) and
  `simInputEvent` (shutters), and polls AMD module state once per module every
  2.5 s.
- `MockPHCClient` — in-memory home with simulated shutter travel, for Demo Mode,
  development, and previews.
- `PHCTelegram.swift` / `PHCFunctions.swift` — RS-485 telegram + CRC builders and
  `functions.xml` com codes. **Reference only** — the STM does bus framing
  internally; the app does not send raw frames.

### State (`Sources/Store`)
- `HomeStore` — an `@Observable` that owns the `PHCClient`, holds the current
  `PHCProject`, applies optimistic UI updates, folds in `StateUpdate`s from the
  client's event stream, and skips no-op polls. On launch it starts instantly
  from the cache, then connects and polls.
- `ProjectCache` — JSON-persists the `PHCProject` to Application Support, keyed
  by host. Writes are debounced so the last-known states survive a relaunch.

### Views (`Sources/Views`)
Pure SwiftUI, driven by `HomeStore`:
- `ConnectionView` — IP entry + Demo Mode, with the app logo.
- `HomeView` — **adaptive**: a `NavigationStack` on iPhone (floor overview that
  pushes to a floor's devices) and a `NavigationSplitView` on iPad (sidebar +
  detail). Sidebar = floors; toolbar = Disconnect + Reload from STM.
- `FloorView` — a floor's devices in **collapsible category sections** (Lights →
  Shutters → Outlets → …), name-sorted within each; Expand/Collapse All.
- `DeviceCard` — the right control per device kind (toggle, slider, shutter
  buttons, scene button).

### Resources
- `Sources/Assets.xcassets` — app icon + in-app `Logo`.
- `Sources/Localizable.xcstrings` — String Catalog (English source + German for
  the menu labels).

## Choosing the backend
`ConnectionView` decides: "Connect to STM" builds
`HomeStore(client: STMv3Client(endpoint:…), cacheKey: host)`; "Demo Mode" builds
`HomeStore(client: MockPHCClient())`. No view or model code is transport-aware.
