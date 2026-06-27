# Decoding PHC module command codes

The big finding from mining the handbook: the **PHC Systemsoftware V3** ships
data files that, together with the handbook's "Befehle der …" sections, fully
define every module's command set. This is the recipe and an index of what we've
decoded so far. (Those files are PEHA's proprietary data and live only on the
local install — they are **not committed**; only the decoded facts are recorded
here and in the per-module docs.)

## The source files (`<install>/app/system/`)

- **`functions.xml`** — per module type:
  `<functions name="DIM_AUS" commandType="Output" defaultCSType="Dimmer" nr="…">`
  containing `<func com="N" dlg="…" value="…" text="…" pos="…" default="…">KEY</func>`.
  - `com` = the **command code** (the low part of the content byte).
  - `KEY` (e.g. `DIMA_07`, `JRMA_05`) = a localization key for the function name.
  - `dlg="-1"` = no parameters; otherwise `pos`/`default`/`value` describe the
    **extra content bytes** (time, level, priority).
- **`modules.xml`** — per physical module: `<classname>` (EMD/AMD/JRM/DIM/…),
  `<outputs … shift="N">` (the channel shift), the channel layout, and a
  `<telegramm>` block describing the state/feedback bytes.
- **`EXT_<MODULE>_AUS_EXT.xml`** — only for modules using the firmware-V3
  **extended** telegram (e.g. `EXT_DIM_AUS_EXT.xml`): the exact wire payload per
  command (`<COM><PARAM pos="i">byte-or-field</PARAM>…</COM>`). Modules **without**
  an EXT file (AMD, JRM) use the basic `(channel<<shift)|com` format.
- **Handbook "Befehle der …" sections** (§3.4.14 outputs, §3.5.5 dimmers,
  §3.4.8 JRM, …) list the same functions **in the same order as the `com` codes**,
  and note which ones need a time/value.

## The encoding model

- **Content byte:** `content = (channel << shift) | com`
  (`shift` from `modules.xml` `<outputs shift>`: 5 for AMD/DIM/JRM, 4 for EMD-LED).
- **Module bus address:** `(class << 5) | dip`. Known classes: EMD `0x00`,
  AMD `0x40`, JRM `0x60`. Other output classes (DIM, …) are firmware-internal.
- **Parameterised commands** append extra content bytes (priority / time / level),
  per `functions.xml` `pos`/`default` or the exact bytes in the EXT file.

## The decoding recipe

1. Find the module in `functions.xml` → the `com` list and which entries take
   parameters (`dlg != "-1"`).
2. Read the handbook's "Befehle der \<module\>" list. The functions are in the
   **same order** as the `com` codes (`com = first_com + index`). Cross-check:
   the handbook functions that need a time/value line up with the parameterised
   `com` entries — a strong confirmation the mapping is right.
3. Get framing (`shift`) and channel/telegram layout from `modules.xml`.
4. If an `EXT_<module>` file exists, it gives the exact V3 wire bytes.

## Decoded so far

| Module (`functions.xml`) | CSType | Key codes | Detail |
|---|---|---|---|
| `AMD24_AUS` | Light/outlet | `com 2` = on, `com 3` = off — **confirmed on hardware** | [PROTOCOL.md](PROTOCOL.md) |
| `DIM_AUS` | Dimmer | `com 2` = on, `4` = off, `8`/`9` = Heller/Dunkler Dimmen, `22` = Dimmwert+Zeit | [DIMMERS.md](DIMMERS.md) |
| `JRM_AUS` | Shutter | `com 2` = stop, `5` = up, `6` = down, `7`/`8` = slat jog — **validates `PHCTelegram.jrm*`** | [SHUTTERS.md](SHUTTERS.md) |

`simInputEvent` (used for shutters and the central scenes) sidesteps all of this
by simulating the **input** side instead — see [PROTOCOL.md](PROTOCOL.md) §1.

## Common remaining unknowns

Resolvable with a short packet capture of the official app — or the PC software's
service function (handbook §6.4) — driving the module:

- **Bus-class numbers** for non-AMD output modules (e.g. DIM) — firmware-internal,
  not in any XML.
- **How `sendTelegram` carries multi-byte content** (the parameter bytes). Our
  confirmed calls only ever sent a single content byte.
- **Value/time scaling** for parameterised commands.
