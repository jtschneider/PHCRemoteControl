# Shutters / Jalousie (JRM) — command set decoded

Shutters **work today** in the app, driven via **`simInputEvent`** (simulating
the rocker input — see `STMv3Client.moveShutterFull` and
[PROTOCOL.md](PROTOCOL.md) §1). This file decodes the **JRM output telegrams**
themselves, which (a) **confirm our existing `PHCTelegram` JRM builders are
correct** and (b) document the full command set for reference.

Sources: PHC Systemsoftware V3 `system/functions.xml` (`JRM_AUS`, `nr=9`) and
`system/modules.xml` (`JRM`), cross-checked against handbook §3.4.8 +
"Beschreibung Befehle Jalousie/Rollladenmodule". There is **no `EXT_JRM` file**,
so JRM uses the basic telegram format (no V3 extended payloads, unlike dimmers).

## Framing

`modules.xml` → `JRM` `<outputs … shift="5">`: `content = (channel << 5) | com`,
same as AMD/DIM. Bus address `0x60 | dip` (JRM class). A JRM drives up to 4
shutter channels (8 relays). Each command carries a **priority** (6 levels,
0 = highest … 5 = lowest); moves also carry a **run time**.

## `com` → function (functions.xml + handbook order)

| `com` | Function | Extra bytes | Meaning |
|---|---|---|---|
| **2** | Lauf stoppen | priority | **Stop** |
| 3 | Umschalten heben/aus | priority, time | Up; pressing again while moving stops |
| 4 | Umschalten senken/aus | priority, time | Down; toggle-stop |
| **5** | Einschalten heben | priority, time | **Up** for the programmed run time |
| **6** | Einschalten senken | priority, time | **Down** for the programmed run time |
| 7 | Tippbetrieb heben | priority, short time | Jog up (venetian-blind slat adjust) |
| 8 | Tippbetrieb senken | priority, short time | Jog down (slat adjust) |
| 9 / 10 | Prioritätsebenen ver-/entriegeln | priority | Lock / unlock priority levels |
| 13 / 14 | Prioritätsebenen setzen / löschen | priority | Set / clear priority levels |
| 15 | Sensorik Rollladen heben | priority, offset, time | Up with start-offset delay |
| 16 | Sensorik Jalousie heben | priority, offset, time, slat | Up + slat-reverse at end |
| 17 | Sensorik Rollladen senken | priority, offset, time | Down with start-offset delay |
| 18 | Sensorik Jalousie senken | priority, offset, time, slat | Down + slat-reverse |

(`com 11/12` are unused in `functions.xml`.)

## This validates our telegram layer

`Sources/Client/PHCTelegram.swift` already builds (from the ESPHome reference):
- `jrmStop`  → `[(ch<<5)|0x02, 0xFC]` — **`com 2` (Lauf stoppen)** ✓
- `jrmMove` up → `[(ch<<5)|0x05, 0x07, time_lo, time_hi]` — **`com 5` (Einschalten heben)** ✓
- `jrmMove` down → `[(ch<<5)|0x06, …]` — **`com 6` (Einschalten senken)** ✓

The opcodes and the priority+time payload match the decoded command set exactly.

## Notable findings

- **No absolute-position command exists.** Shutters move for a *programmed run
  time*; there is no "go to X %". This confirms why the app shows only a brief
  "command sent" hint, not a position — the hardware has none to give.
- **`com 3/4` (Umschalten)** are a nice single-button model: one command both
  starts the move and stops it mid-travel.
- **`com 7/8` (Tippbetrieb)** are the slat-jog commands for venetian blinds —
  something the current up/stop/down UI doesn't expose.

## Why we still use `simInputEvent`, not direct JRM telegrams

Direct JRM control is now well-understood, but offers **no advantage** for this
app and **costs more**:
- It gives no position control either (none exists), so the UX is identical.
- The shutters are addressed in the project by their **EMD input** channels
  (heben/senken), which is what `simInputEvent` uses and what the parser already
  has. Driving the **JRM output** instead would require parsing the `tpfx`
  input→output mapping to learn each shutter's JRM channel.
- It would still need a capture to confirm the **JRM bus class** (`0x60`,
  currently from the decompile, not a packet capture), the **multi-byte
  `sendTelegram`** path (priority + time bytes), and the exact **priority byte
  encoding** (`0x07`/`0xFC` from ESPHome vs the 0–5 levels in the dialog).

So `simInputEvent` stays the right call. The one feature direct JRM would unlock
is **slat adjustment** (`com 7/8`) for venetian blinds — worth revisiting only if
such blinds turn up in an installation.
