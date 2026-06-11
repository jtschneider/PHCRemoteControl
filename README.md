# PHC Remote Control

A modern, iPad-friendly iOS app to remotely control a **PEHA / Honeywell PHC**
(Peha Home Control) electrical installation over the local network.

It is a from-scratch replacement for the aging official
[*PHC Home Control*](https://apps.apple.com/de/app/phc-home-control/id1141475941)
app, which talks directly to a networked **STM v3** control unit but was never
laid out for larger displays.

> Status: **iteration 1 — runnable UI skeleton with a mock backend.**
> The real STM transport is not wired up yet; see
> [docs/PROTOCOL.md](docs/PROTOCOL.md) for why and what's next.

## How it works

```
┌────────────┐   Wi-Fi / LAN     ┌──────────────┐   RS-485 bus   ┌──────────────┐
│  iPhone /  │ ────────────────► │   STM v3     │ ─────────────► │ AMD/EMD/JRM  │
│   iPad     │  STM IP protocol  │ (Steuermodul)│   PHC modules  │ output/input │
└────────────┘                   └──────────────┘                └──────────────┘
```

The iPhone never touches the RS-485 bus. It speaks the STM v3's IP protocol;
the STM relays commands onto the bus and reports module state back.

## Architecture

The app is deliberately split so the (still-being-reverse-engineered) wire
protocol is isolated behind one interface:

- `PHCClient` — the transport protocol abstraction (connect, load project,
  switch/dim/shutter commands, live state stream).
- `MockPHCClient` — an in-memory fake used for development and previews. **This
  is what the app runs against today.**
- `STMv3Client` — skeleton for the real XML-RPC transport (not functional yet).
- SwiftUI views + an `@Observable` `HomeStore` that are completely transport-agnostic.

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Building

This repo contains the Swift sources and an [XcodeGen](https://github.com/yonaskolb/XcodeGen)
`project.yml`. On a Mac with Xcode 16+:

```sh
brew install xcodegen   # one time
xcodegen generate       # creates PHCRemoteControl.xcodeproj
open PHCRemoteControl.xcodeproj
```

Then pick an iPhone/iPad simulator and Run. No real STM is required — it boots
against the mock with a sample project (rooms, lights, dimmers, shutters).

(If you'd rather not use XcodeGen: create a new iOS App in Xcode named
`PHCRemoteControl`, delete its default files, and drag the contents of the
`Sources/` folder in.)

## Roadmap

1. ✅ Runnable UI skeleton against a mock backend.
2. ✅ **Protocol decoded** by decompiling the PHC Systemsoftware: it's
   **XML-RPC** (`service.stm.*`, default port 6680) carrying raw PHC bus
   telegrams. The telegram builder + CRC are implemented and verified in
   `Sources/Client/PHCTelegram.swift`. See [docs/PROTOCOL.md](docs/PROTOCOL.md).
3. ⏳ **Confirm STM v3 network specifics** — packet-capture the official app for
   the exact port, LAN discovery, auth, and XML-RPC param order.
4. ⏳ Finish `STMv3Client` (XML-RPC over TCP: connect, getModule → project, send
   telegrams, fold STM events into live state).
5. ⏳ Replace mock with the real client behind a settings toggle.
6. ⏳ Scenes, favourites, remote (off-LAN) access.
</content>
