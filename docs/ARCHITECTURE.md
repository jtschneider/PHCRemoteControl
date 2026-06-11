# Architecture

The app is organised so the unknown/proprietary network protocol is the *only*
thing left to fill in. Everything else — model, state management, UI — is built
and runnable today against a mock.

```
            ┌──────────────────────── SwiftUI Views ────────────────────────┐
            │  HomeView · RoomSectionView · LightRowView · DimmerRowView ·   │
            │  ShutterRowView · ConnectionSettingsView                       │
            └───────────────────────────────┬───────────────────────────────┘
                                            │ observes / sends intents
                              ┌─────────────▼─────────────┐
                              │   HomeStore (@Observable)  │  app state + actions
                              └─────────────┬─────────────┘
                                            │ uses
                              ┌─────────────▼─────────────┐
                              │   PHCClient (protocol)     │  transport boundary
                              └───────┬───────────┬────────┘
                                      │           │
                       ┌──────────────▼──┐   ┌────▼──────────────┐
                       │  MockPHCClient   │   │   STMv3Client     │
                       │  (works today)   │   │  (to implement)   │
                       └──────────────────┘   └───────────────────┘
```

## Layers

### Models (`Sources/Models`)
Plain value types, transport-independent:
- `ChannelRef` — `(moduleClass, dip, channel)` address of a controllable point.
- `Device` — a user-facing controllable thing (light / dimmer / outlet /
  shutter / scene) with a current `DeviceState`.
- `Room` — a named grouping of devices.
- `PHCProject` — the loaded installation: rooms + devices. This mirrors what the
  STM hands over when the official app "loads the project".

### Client (`Sources/Client`)
- `PHCClient` — async protocol: `connect`, `loadProject`, `setPower`,
  `setBrightness`, `moveShutter`, and an `events` stream of `StateUpdate`s for
  live push from the bus.
- `MockPHCClient` — in-memory implementation with a believable sample home and
  simulated shutter travel, so the UI is fully exercised without hardware.
- `STMv3Client` — skeleton with the call sites we need to implement once the
  protocol is captured (see docs/PROTOCOL.md).

### State (`Sources/Store`)
- `HomeStore` — an `@Observable` that owns the `PHCClient`, holds the current
  `PHCProject`, applies optimistic UI updates, and folds in `StateUpdate`s from
  the client's event stream.

### Views (`Sources/Views`)
Pure SwiftUI, driven by `HomeStore`. Adaptive layout (uses `NavigationSplitView`
so it feels native on iPad and collapses gracefully on iPhone).

## Swapping in the real backend
When `STMv3Client` is ready, the only change is in `HomeStore`'s initialiser
(or a settings toggle): construct `STMv3Client(host:…)` instead of
`MockPHCClient()`. No view or model code changes.
</content>
