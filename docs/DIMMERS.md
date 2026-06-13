# Dimmers — not supported (and why)

**Honest status:** this app does **not** support PHC dimmers on real hardware.
The brightness slider you see is a **Demo-Mode simulation only**. There are no
dimmers in the installation this app was built against, so nothing here has been
tested against a real dimmer module.

## What's actually in the code

| Piece | Reality |
|---|---|
| `MockPHCClient.setBrightness` | Instantly sets brightness 0–100 % and animates it. Pure simulation — used only by Demo Mode / the sample project ("Ceiling"). |
| `STMv3Client.setBrightness` | **Stub.** It calls `setPower(ref, on: value > 0)` — i.e. it just switches the channel fully on/off and **ignores the level**. |
| `PHCProjectParser.deviceKind` | Has **no `DIM` mapping** (`Licht`→light, `Steckdose`→outlet, `Rollo`→shutter, else→light). So a real STM connection never produces a `.dimmer` device — the slider only ever appears in Demo Mode. A real dimmable light would show as a plain on/off toggle. |

Net effect: on real hardware the slider would never appear, and even if it did,
it would only toggle the channel on/off.

## How PHC dimming actually works (handbook §3.5 "Dimmermodule")

PHC dimming is **ramp-based and stateful in the module**, not "set to X %":

- **Heller Dimmen / Dunkler Dimmen** (dim up / down): you *hold* the button
  (programmed as "Ein > 1 Sek.", i.e. long-press) and the level ramps over a
  configured **Dimmzeit** (full-range traversal time). On release the program
  issues **"Speichern Memory"** to store the reached level. This is the same
  hold-to-act pattern as the shutters.
- **Dimmen in Gegenrichtung**: each press flips the ramp direction.
- **Einschalten max. Helligkeit (mit/ohne Memory)**, **Dimmer ausschalten**,
  **Umschalten EIN/AUS**: tap to turn on at full or at the last saved level, off,
  or toggle.
- **Light scenes + memory value**: preset levels live **inside the dimmer
  module**, alongside a per-channel **curve** (Standard / 1–10 V / 5–10 V) and
  **soft-start** behaviour, configured once and transferred to the module.
- **Absolute level *is* possible** on some module types via **"Dimmwert und Zeit
  setzen"** / **"Dimmwert anfahren"** — but it's a **value + ramp-time** command
  (a multi-byte telegram), not a single `com` byte, and not supported by every
  dimmer.

On the wire: base on/off is `com` 2/3 like any output
(`content = (channel << 5) | com`); the ramp/scene/value variants are `com`
4…25 and carry **extended bytes** (level/time). Those exact bytes are **not
confirmed** here — they'd need a packet capture of a real dimmer.

Module families (handbook §3.5.1): phase-cut / phase-control / universal dimmers
(e.g. 944/2 DM AN/AB, 949 DM M-AN/AB/UN, 439 UN REG slave), plus a DALI gateway
(940/8 Dali-G) with its own extended command set (§3.5.6).

## What it would take to support dimmers properly

1. **Parser:** map the `DIM`/dimmer module + `Licht` channel on a dimmer module
   to `DeviceKind.dimmer`.
2. **Transport:** implement the dimmer telegrams — either hold-to-dim
   (up/stop/down, mirroring the shutter input simulation) or the absolute
   "Dimmwert und Zeit setzen" (level + time as extended bytes).
3. **UI:** for hardware, hold-to-dim up/stop/down matches the real interaction
   model better than an absolute slider.
4. **Capture:** confirm the exact telegram/extended bytes against a **real
   dimmer** — which this project has never had access to.

Until then, treat the dimmer slider as a demo showcase, not a working feature.
