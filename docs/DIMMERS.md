# Dimmers — command set decoded, not yet wired up

**Status:** the app still does **not** drive real dimmers — the brightness slider
is a **Demo-Mode simulation**. But the PHC dimmer command set is now **decoded**
from the PHC Systemsoftware's own data files, so a real implementation is finally
within reach (it just needs a capture against a dimmer to confirm two wire-level
details — see "Still unknown"). There are no dimmers in the installation this app
was built against.

## What's in the code today

| Piece | Reality |
|---|---|
| `MockPHCClient.setBrightness` | Simulates 0–100 % instantly. Demo Mode only. |
| `STMv3Client.setBrightness` | **Stub** — `setPower(ref, on: value > 0)`, ignores the level. |
| `PHCProjectParser` | No `DIM` mapping → a real STM connection never yields a `.dimmer`. |
| `PHCFunctions` | Had `dimOff = 3` — **wrong** (see below); off is `com 4`. |

## Decoded command set (DIM_AUS)

Sources: PHC Systemsoftware V3 `system/functions.xml` (`DIM_AUS`, `nr=4`),
`system/modules.xml` (`DIM_AN`), `system/EXT_DIM_AUS_EXT.xml`, cross-checked
against handbook §3.5.5 (which lists the functions in the same order, and flags
exactly which ones need a time/value — matching the parameterised `com` codes).

**Framing** (`modules.xml` → `DIM_AN` `<outputs … shift="5">`): same as AMD —
`content = (channel << 5) | com`. A dimmer module has 2 dimmer channels (adr 0/1).

**`com` → function** (from `functions.xml`, confirmed by handbook order + which
codes carry parameters):

| `com` | Function | Extra bytes |
|---|---|---|
| 2 | On — max brightness, with memory | — |
| 3 | On — max brightness, no memory | — |
| 4 | **Off** | — |
| 5 | Toggle max on/off | — |
| 7 | Dim in opposite direction | time (default 3) |
| **8** | **Heller Dimmen** (dim up) | time (default 3) |
| **9** | **Dunkler Dimmen** (dim down) | time (default 3) |
| 10 | Save memory | — |
| 12 | On to memory light value | — |
| 13–21 | Save / toggle / on for MEMORY 1·2·3 | — |
| **22** | **Dimmwert und Zeit setzen** (set level+time) | value (pos 0, default 50) + time (pos 1, default 3) |

The parameter layout corroborates the mapping: `com 7/8/9` each carry one time
byte (handbook: these need a *Dimmzeit*), and `com 22` carries two — value then
time — i.e. "set brightness X over time T".

**Extended V3 payloads** (`EXT_DIM_AUS_EXT.xml`): firmware-V3 dimmers use a longer
telegram whose first byte is a wire op-code followed by named fields. The
absolute-set entry is op **`0x08`** with `Zielwert1` (target brightness) and
`Zeitglied1` (time); dim up/down map to op `0x14`/`0x15`. So both a simple
`(channel<<5)|com` form and an extended multi-byte form exist.

## Still unknown (needs one capture against a real dimmer)

1. **DIM bus-class prefix.** `modules.xml` names the class `"DIM"` symbolically
   but not the numeric address prefix (AMD is `0x40`, JRM `0x60`, EMD `0x00` —
   that mapping lives in STM firmware). Without it we can't address a DIM module.
2. **How `sendTelegram` carries multi-byte content.** Our confirmed calls send a
   single content byte. `com 8/9` (+time) and `com 22` (+value+time) need 2–3
   content bytes, or the extended V3 telegram — unconfirmed over the XML-RPC
   `sendTelegram` path.
3. **Value/time scaling.** `Zielwert` default 50 ⇒ likely brightness %, but the
   units/range and `Zeitglied` units aren't confirmed.

All three fall out of a 2-minute capture of the official app (or the PC
software's service function, handbook §6.4) driving a real dimmer — which this
project has never had access to.

## What implementing it would look like

1. **Parser:** map a `DIM`/`DIM_AUS` output channel → `DeviceKind.dimmer`.
2. **Model/transport:** add a `.dim` bus class (prefix TBD from #1); on/off via
   `com 2`/`com 4`; brightness via `com 22` (value+time) once #2/#3 are confirmed;
   or hold-to-dim via `com 8`/`com 9`.
3. **UI:** hold-to-dim up/stop/down matches the real interaction model; an
   absolute slider maps to `com 22`.
4. **Fix** `PHCFunctions.dimOff` `3 → 4`.

Until a dimmer + capture exist, treat the slider as a demo showcase. This file is
the implementation spec for when one does.
