# Go PHC Bridge Website Implementation Plan

This is the current implementation handoff for the Raspberry Pi bridge. It is
derived from [GO_BRIDGE_PLAN.md](GO_BRIDGE_PLAN.md), but makes one product
decision explicit: the MVP is a normal responsive website served directly by
the Go process. It is not an offline PWA and does not require browser-trusted
TLS on the trusted home LAN.

> **Security posture:** this local HTTP profile has no user authentication. Any
> host that can reach the bridge must be treated as fully authorized to inspect
> the exposed project and execute every supported command. The MVP explicitly
> accepts that risk for devices admitted to the trusted home LAN.

Protocol facts remain authoritative in [PROTOCOL.md](PROTOCOL.md).

### Reference authority

The shipping Swift app is the **single authoritative, hardware-verified
reference** for this bridge. It has been tested against the real PHC installation
and works; this document has not. Where this plan's prose and the Swift behaviour
disagree on anything verified on hardware, **the Swift code wins** and this
document is corrected to match it. The bridge's job is to *reproduce* Swift's
behaviour button-for-button and byte-for-byte, not to reinterpret the protocol.

Reproduce, do not redesign — the canonical sources are:

- **`Sources/Client/STMv3Client.swift`** — the exact command each button sends:
  `setPower` (on/off telegram encoding), `moveShutterFull` / `tapMove` / `tip`
  (raise / lower / stop / tilt event sequences), `longPressButton`,
  `activateScene`, `sendTelegram`, and the AMD state poll in `pollOnce`.
- **`Sources/Client/PHCProjectParser.swift`** — *which* buttons exist: floor and
  category parsing, AMD light/outlet classification, EMD motor pairing, motorized
  windows, scenes, fallback buttons, and the supported `project.tpfx` tools.
- **`Sources/Models/PHCKeywords.swift`** — the German/English classification vocabulary.
- **`Sources/Models/Device.swift`** — the device and command model (`DeviceKind`,
  `ShutterCommand`, including the `tiltOpen` / `tiltClose` jalousie cases).

The shared `protocol-fixtures/` corpus is how this reproduction is *enforced*: a
behaviour is correct when the Go bridge and the Swift reference produce the same
result for the same fixture. If the Swift reference gains a capability (as it did
with jalousie tilt), this plan and its fixtures must be updated to match it.

**The one exception with no Swift counterpart to copy:** the STM's malformed HTTP
response header. Swift's `URLSession` tolerates it transparently, so there is *no*
Swift handling code to port — the Go transport sanitizer (§8) is the only piece
this bridge must originate rather than reproduce. Do not search the Swift client
for header-repair logic; it does not exist there.

When this document and a new hardware capture disagree, update
[PROTOCOL.md](PROTOCOL.md), add a synthetic regression fixture, and update the
Swift reference and this plan together before broadening the implementation.

## 1. Product decision

The deployed system is:

```text
iPhone, iPad, Android, or desktop browser
                  |
                  | HTTP on the trusted home LAN
                  | HTML + CSS + JS + JSON + SSE
                  v
       Go bridge on Raspberry Pi 3
                  |
                  | STM XML-RPC over malformed HTTP
                  v
                STM v3
```

The Go binary owns both sides:

- it serves the complete browser interface;
- it exposes the same-origin JSON command API;
- it publishes state changes over Server-Sent Events (SSE);
- it serializes communication with the STM;
- it downloads, parses, and caches the PHC project;
- it polls readable output state at the active cadence while a browser is
  observing it, and refreshes state when the first observer connects.

The browser is a temporary view onto the service. Closing it does not stop the
bridge or invalidate the cached project, but fast state polling stops after a
short subscriber grace period.

### Why this is not a PWA

The MVP deliberately has:

- no service worker;
- no offline application cache;
- no background synchronization;
- no queued physical commands;
- no push notifications;
- no PWA installation requirement.

An offline shell cannot operate the house while the Pi or LAN is unavailable.
Replaying a cached command after reconnection could be dangerous, especially
for shutters, motorized windows, and security actions.

The site may still provide an app icon and iOS Home Screen metadata. Those are
presentation details, not a promise of offline operation.

## 2. MVP goals

1. Run as one unprivileged systemd service on a Raspberry Pi 3.
2. Serve a responsive, server-rendered website directly from Go.
3. Work from Safari on an iPhone over the trusted home LAN and from ordinary
   desktop browsers without installing a certificate.
4. Download and parse the STM project with strict resource limits.
5. Display all parsed floors, categories, and active controls.
6. Control lights, outlets, shutters, motorized windows, scenes, and fallback
   buttons using the hardware-proven event sequences.
7. Display live light and outlet state through SSE.
8. Make STM connectivity and stale state visible without claiming feedback that
   the hardware does not provide.
9. Start automatically after a Pi reboot and recover after STM or LAN outages.
10. Keep real project data, installation addresses, and captures out of git.

## 3. Non-goals

The first version will not:

- expose the service to the public internet;
- provide off-LAN access;
- provide user authentication in the local HTTP profile;
- require a private CA, public certificate, domain, or reverse proxy;
- use React, Vue, Angular, Node.js, npm, or a frontend bundler;
- duplicate the entire project model in a browser-side application;
- implement a service worker or offline command queue;
- support multiple STMs;
- accept an STM address supplied by a browser request;
- claim shutter position or movement feedback;
- implement real dimmer control before hardware validation;
- implement HomeKit, Matter, MQTT, or Home Assistant integration;
- replace Go's ZIP, XML, template, or HTTP libraries with hand-written general
  parsers.

Remote access and publicly trusted HTTPS are possible later without changing
the STM packages or browser API contract.

## 4. Delivery assumptions

The recommended local URL is an example:

```text
http://phc-bridge.local:8080
```

Do not assume that name exists automatically. Before implementation:

1. Reserve the Pi's address in the FRITZ!Box.
2. Configure a stable local name using router DNS or mDNS/Avahi.
3. Confirm the exact URL opens on the target iPhone.
4. Add the page to the Home Screen and relaunch it after closing Safari.
5. Reboot the Pi and confirm the saved icon reaches the same origin again.

Use the stable IP URL if local naming proves unreliable. The configured public
origin and the address presented to users must remain stable because they are
part of the browser security policy and saved Home Screen entry.

## 5. Browser interface architecture

Use Go's standard `html/template` package for the initial document and a small
external JavaScript file for interaction. Prefer semantic HTML and ordinary
links so navigation and initial rendering do not depend on JavaScript.

### Design principles: minimal and static-first

The interface is a server-rendered HTML5 site with the **smallest viable amount
of JavaScript and CSS** and no build step, framework, or bundler. Everything is
embedded in the binary via `//go:embed`.

**HTML — server-rendered, the sole renderer.** Go `html/template` renders every
page as semantic HTML with ordinary `<a>` links, so **reading and navigation work
with JavaScript disabled**. Device rows carry the stable `data-device-id` /
`data-role` anchors from the DOM update contract below.

**CSS — one small hand-written file.** A single `app.css`, on the order of a
couple of hundred lines: a system font stack (no web fonts, no CDN), CSS custom
properties, `prefers-color-scheme` light/dark, and flexbox/grid for a responsive
phone-and-desktop layout. Minimal is not unstyled — touch targets stay ≥44px and
text stays legible. No preprocessor, utility framework, or purge step.

**JavaScript — one small file, a few tight jobs.** `app.js` is plain, framework-
free, and uses event delegation. It does exactly:

1. **Command dispatch** — a single delegated click handler on `[data-action]`
   elements issues `fetch(POST /api/v1/devices/{id}/commands, {action})`, disables
   the control while the request is in flight (momentary commands must not double-
   fire, and the API does not auto-retry), and re-enables it on completion.
2. **State updates** — one `EventSource('/api/v1/events')`; `state` events patch
   the matching `[data-role]` text and `aria-pressed`, `connection` events update
   a status region, and a `project` event triggers `location.reload()`. A small
   in-memory state map is kept current so a swapped-in page (below) shows live
   state immediately.
3. **Confirmation** — a native `confirm()` for panic/security actions.
4. **In-place navigation** — intercepts internal links, fetches the target page,
   and swaps only its `<main>` (with hover/`pointerdown` prefetch and History-API
   Back/Forward), keeping one persistent SSE across navigations so each hop is
   crisp and avoids re-subscribe churn. **The Go template still renders that
   `<main>`, so it remains the sole structural renderer** — JavaScript lifts the
   server's markup, never builds device cards. The SSE also closes on `pagehide`
   and reopens on `pageshow`, so full Back/Forward can restore from bfcache.

It stores no authoritative state, never renders device markup, and needs no
minification for LAN delivery. This is progressive enhancement of navigation,
**not a PWA** — no service worker and no offline cache (see "Why this is not a
PWA"); an unreachable bridge simply fails to navigate rather than serving a stale
shell.

**Control requires JavaScript, by design.** The security boundary (§14) accepts
only JSON `POST`s with a matching `Origin`, which a plain HTML `<form>` cannot
send. The site is therefore fully *viewable* without JavaScript but not
*controllable*; a `<noscript>` notice states this rather than pretending forms
will work.

**Motion — cosmetic only, never fake progress.** A pressed button may have a small
tactile press effect (a CSS `:active` transition, no JavaScript) — that is welcome.
What is deliberately dropped is the Swift app's **shutter loading spinner**:
because PHC reports no position or movement feedback, a spinning "in progress"
indicator would imply progress the bridge cannot observe. Shutter, tilt, and scene
commands instead show a brief **textual** "command sent" acknowledgement that
clears on a timer — no spinner, no sliding blind indicator. Light and outlet
indicators change only when real state arrives over SSE.

**Accessibility from plain controls.** Favour native elements: on/off is a
`<button aria-pressed>` rather than a CSS-styled switch — less CSS *and* better
screen-reader semantics. Every control has a discernible label and a visible focus
ring. This keeps both the CSS and the accessibility surface small.

### Pages

```text
GET /                         floor overview or selected default floor
GET /floors/{floorID}         devices grouped by category
GET /settings                 connection and project information
GET /acknowledgments          licenses and project acknowledgments
```

The exact route used for the first screen may be refined during UI work, but a
page refresh must always reconstruct the same useful view from server state.

### Progressive enhancement

The initial response contains the current project and state as rendered HTML.
JavaScript then:

- sends command requests with `fetch`;
- subscribes to `/api/v1/events` using `EventSource`;
- updates power indicators and connection status;
- shows short command acknowledgements for state-less devices;
- handles confirmation dialogs for panic and security actions;
- preserves purely local disclosure state such as expanded categories.

Do not turn the browser into a second authoritative state store. On SSE
reconnect, reload a state snapshot or consume a complete snapshot event.

### DOM update contract

There must not be two independent structural renderers. Go templates own the
device and category DOM. JavaScript may patch state and transient status, but it
must not create an alternative device-card representation.

Every rendered device root uses a stable anchor:

```html
<article data-device-id="stable-device-id">
  <output data-role="power-state">Off</output>
  <button data-role="power-command" data-action="toggle">...</button>
</article>
```

`app.js` locates a device by `data-device-id` and a field by `data-role`. It
updates attributes, text, and state classes only. When an SSE project event
announces a structural change, the browser performs a normal page reload so the
Go template remains the sole structural renderer.

Contract tests must render representative templates, assert that every
state-capable device has the required anchors, then exercise the real JavaScript
updater in a browser against that rendered page. A selector change that no
longer reaches the expected element must fail the test.

### Static assets

Embed templates and assets into the executable using `//go:embed`:

```text
internal/web/templates/*.html
internal/web/static/app.css
internal/web/static/app.js
internal/web/static/icons/*
```

Use the existing PHC Remote logo assets where their format is suitable. Do not
introduce a frontend asset pipeline solely to resize or hash a handful of
files. Prefer text labels over an icon font or sprite sheet; use a few inline
SVGs only where a glyph genuinely helps.

Serve the embedded CSS/JS with a strong `ETag` computed from each file's content
hash at startup (a few lines of Go, not an asset pipeline), so a bridge upgrade is
never stuck behind a stale browser cache while unchanged assets still validate
with `304 Not Modified`.

### UI behaviour

Parallel the Swift app's information architecture, not its exact visual layout:

- floors are the primary navigation level;
- devices are grouped by parsed category;
- categories are collapsible;
- device names remain project data and are not translated;
- chrome is available in English and German;
- lights and outlets use a power control and readable state;
- shutters and motorized windows expose raise, stop, and lower controls;
- jalousies additionally expose experimental slat-tilt controls (§11);
- fallback buttons expose clearly labelled short-press and long-press actions;
- panic and security actions require an explicit confirmation dialog;
- state-less commands show only a brief textual acknowledgement, never invented
  state; a cosmetic button-press effect is fine, but the shutter loading spinner
  is dropped — there is no observable progress to animate (see Design principles).

The interface must clearly distinguish:

1. bridge reachable and STM connected;
2. bridge reachable but STM unavailable;
3. state snapshot stale;
4. command accepted by the bridge;
5. command rejected or failed.

An HTTP response means the bridge handled a request. It does not prove that a
motor reached a physical position.

### Favourites

The Swift app lets the user star devices and reorder them into a pinned section.
The website reproduces that feature, but the storage question has no equivalent
answer here: the Swift app keeps favourites per user in `UserDefaults`, while this
profile has **no user identity at all** (§14). Server-side favourites would
therefore be a single household-wide list that any permitted client could silently
reorder for everyone.

**Decision for the MVP: favourites live in the browser, not on the bridge.**

- Store an ordered list of stable device IDs in `localStorage`, keyed by the
  configured public origin.
- Resolve IDs against the current project at render time and silently drop those
  that no longer exist, so a re-parsed or re-wired project degrades gracefully.
- Never send favourites to the bridge; they are presentation state, not device
  state, and must not enter the project cache or any API payload.

Rationale: it keeps per-device personalisation (a phone and a wall tablet want
different favourites), adds no server state to back up, migrate, or protect, and
avoids inventing an ownership model the unauthenticated profile cannot express.

Accepted limitations, which must be visible in `/settings`: favourites do not sync
between devices and are lost if browser storage is cleared. Server-side, synced
favourites are a natural addition once an authenticated profile exists, and would
supersede this decision rather than extend it.

## 6. Proposed repository layout

Add the bridge beside the Swift app:

```text
Tests/
  PHCRemoteControlTests/
    FixtureParityTests.swift
    CommandFixtureTests.swift
bridge/
  go.mod
  go.sum
  README.md
  cmd/
    phc-bridge/
      main.go
    stm-probe/
      main.go
  internal/
    config/
      config.go
    stm/
      transport.go
      xmlrpc.go
      client.go
      types.go
    project/
      download.go
      parser.go
      keywords.go
    domain/
      model.go
      identity.go
    controller/
      controller.go
      commands.go
      polling.go
      events.go
    cache/
      cache.go
    web/
      server.go
      pages.go
      api.go
      events.go
      security.go
      assets.go
      templates/
        base.html
        home.html
        floor.html
        settings.html
        acknowledgments.html
        device.html
      static/
        app.css
        app.js
        icons/
  testdata/
    transport/
    xmlrpc/
    project/
  packaging/
    phc-bridge.service
    bridge.env.example
protocol-fixtures/
  README.md
  project/
  commands/
```

`protocol-fixtures/` contains language-neutral synthetic cases consumed by both
Go tests and a real Swift unit-test target generated through `project.yml`. It
must never contain the real household project. Merely documenting fixtures
without running them in both suites does not satisfy the anti-drift goal.

Keep package boundaries practical. Do not add interfaces until there is a real
test or ownership boundary that benefits from one.

## 7. Go and dependency policy

Development and deployment currently use Go 1.26.5 on macOS and the Raspberry
Pi. Set the module version accordingly unless the installed Pi toolchain
changes.

Prefer the standard library:

- `net` and `net/http` for STM and browser transports;
- `encoding/xml` for XML-RPC;
- `archive/zip` for the project archive;
- `html/template` for pages;
- `embed` for assets;
- `encoding/json` for the API and cache;
- `log/slog` for logging.

Do not add a web framework, router, template framework, or JavaScript dependency
until the standard library implementation demonstrates a concrete limitation.

## 8. STM transport requirements

The STM listens on port 6680 and expects XML-RPC requests at `/`. The request is
a normal HTTP POST, but the response contains at least one malformed header line
that standard Go HTTP parsing may reject.

Before selecting the compatibility implementation:

1. Capture a direct, non-proxied raw STM response.
2. Compare it with the existing mitmproxy observation.
3. Store only a synthetic byte-for-byte equivalent in `testdata/transport`.
4. Test fragmented reads, valid responses, the known defect, oversized headers,
   unexpected malformed lines, premature EOF, and timeouts.

Evaluate two bounded approaches:

- a `net.Conn` wrapper that repairs only the exact known header defect before
  passing bytes to `http.ReadResponse`;
- a strict STM-specific response reader that accepts the status line, required
  framing headers, and exact known defect while rejecting unrelated syntax.

Choose the smaller implementation that is justified by raw hardware bytes.
Do not write a permissive general HTTP parser and do not normalize arbitrary
malformed input.

Set explicit connect, write, header, body, and whole-operation deadlines. Limit
header bytes, response bytes, XML depth/size, project chunks, decoded ZIP size,
entry count, and decompressed entry size.

## 9. Typed STM client

Expose typed operations rather than XML strings:

```go
type Client interface {
    WhoAreYou(ctx context.Context) (Identity, error)
    ReadFile(ctx context.Context, fileIndex, chunkIndex, mode int) (FileChunk, error)
    SendTelegram(ctx context.Context, stmIndex, moduleAddress, content int) ([]int, error)
    SimInputEvent(ctx context.Context, stmIndex, module, channel, eventType, keyType int) error
}
```

Only the XML-RPC package knows XML element details. The client validates response
types, required struct members, array lengths, integer ranges, and fault
responses before returning domain values.

All STM operations pass through one controller scheduler. User commands have
higher priority than polling and may wait for at most the currently in-flight
STM request. Do not allow concurrent polling and command calls to race on the
device.

## 10. Project download and parsing

Implement the confirmed startup sequence:

```text
service.stm.whoAreYou()
service.stm.readFile(0, chunkIndex, 1) until cur == total - 1
```

Base64-decode and concatenate chunks in order, then use `archive/zip` and its
central-directory handling. Never scan compressed payload bytes for ZIP data
descriptor signatures.

Require and parse `project.ppfx`. Parse optional `project.tpfx` only for the
selected automation actions already supported by Swift. The current Swift
parser defines expected ppfx and supported tpfx classification and fallback
behaviour.

Do not parse `project.cpfx` in the MVP and do not make it a parity target. The
current Swift app has no cpfx consumer, and inspected real cpfx data exposes
typeless `Undefined`/`PossibleObjects` structures rather than a proven device
model. Retain the archive entry only if useful for explicitly separate, bounded
research. The facility file is likewise not a parser-parity input.

Cross-language parser parity includes only ppfx and the supported tpfx subset:

- floor sort prefixes and display names;
- category text between `:` and `>`;
- light, outlet, pump, shutter, and motorized-window classification;
- EMD raise/lower channel pairing;
- scenes and selected central actions;
- panic and presence-simulation actions where supported;
- visible unknown EMD controls as fallback buttons;
- unfiltered fallback names so unsupported wiring remains discoverable.

Stable external IDs are a separate API contract. The current Swift model uses
fresh UUIDs during parsing and a hardware-derived `favouriteKey`, so identical
UUID output cannot honestly be asserted yet. Before freezing fixture outputs or
API v1, define one language-neutral stable-ID algorithm, update Swift to use or
export it, and then require both suites to produce the same IDs.

Apply conservative limits before allocation. Reject archives with excessive
compressed size, decompressed size, entry count, path traversal, duplicate
required entries, or malformed XML beyond the selected tolerance.

## 11. Command mapping

### Lights and outlets

```text
sendTelegram(0, 0x40 | moduleAddress, (channel << 5) | command)
```

Confirmed commands:

```text
2 = on
3 = off
6 = toggle
```

Use the module address class proven by the parsed hardware type. Do not infer a
new address class from the display label alone.

### Shutters and motorized windows

```text
simInputEvent(0, emdModule, channel, eventType, 4)
```

Confirmed sequence:

```text
lower: press(2), release(4), doublePress(5) on the lower channel
raise: press(2), release(4), doublePress(5) on the raise channel
stop:  press(2), longPress(3) on the lower channel
```

The app labels describe the physical outcome. The event names above describe
the PHC input simulation and must not be reinterpreted as gesture duration in
the browser.

### Jalousie slat tilt (experimental)

Devices the parser identifies as jalousies (venetian blinds) additionally expose
a slat-angle nudge, mirroring `ShutterCommand.tiltOpen` / `.tiltClose` and
`STMv3Client.tip` in the Swift reference:

```text
tilt open:  press(2), release(4) on the raise channel
tilt close: press(2), release(4) on the lower channel
```

A tilt is the short tap *without* the `doublePress(5)` that commits a full
travel. Momentary: one small step per request, no persistent state.

Unlike raise/lower/stop, this sequence is **not hardware-verified** — no jalousie
exists in the reference installation. The canonical jog is the JRM output
Tippbetrieb (`JRM_AUS` com 7 = raise, com 8 = lower), but JRM outputs are not
addressable visu channels, so both Swift and this bridge approximate the tip on
the rocker input. The target channel is correct; only the event sequence is
provisional.

Therefore the capability must be advertised and labelled as experimental in the
API and UI, and must not be offered for plain roller shutters. If a capture from
real jalousie hardware ever contradicts this, fix the Swift reference first, then
the shared fixtures, then this bridge.

### Fallback buttons

An unknown visible EMD control exposes two explicit actions using the same
channel:

```text
short press: press(2), release(4), doublePress(5)
long press:  press(2), longPress(3)
```

This is a compatibility fallback, not a claim that the device is a light or
shutter.

## 12. State and event delivery

Track the number of active SSE subscribers and make state polling
subscriber-aware:

1. On the transition from zero to one subscriber, mark cached output state
   stale and immediately sweep every pollable AMD module once.
2. While at least one subscriber is connected, poll each AMD module once per
   active interval, initially 2.5 seconds.
3. When the final subscriber disconnects, retain the active cadence only for a
   short grace period, initially 15 seconds, to absorb page navigation and iOS
   background/foreground churn.
4. After the grace period, stop output-state polling. A slow, separately
   configured connectivity probe may continue if needed for diagnostics.
5. A command targeting an AMD output may refresh that module after the command
   even with no subscriber, but it must not restart continuous polling.

The state read remains one call per AMD module:

```text
sendTelegram(0, moduleBusAddress, 1)
```

Decode the returned bitmask and update every mapped channel from that module.
Publish only changed device state, plus explicit connection/staleness events.

The SSE endpoint is:

```text
GET /api/v1/events
Content-Type: text/event-stream
```

Suggested events:

```text
event: snapshot
data: {"revision":12,"connection":"connected","devices":{...}}

event: state
data: {"revision":13,"deviceID":"...","power":"on"}

event: connection
data: {"revision":14,"status":"disconnected","stale":true}

event: project
data: {"revision":15,"reloadRequired":true}
```

Send heartbeat comments and ensure slow or disconnected browsers cannot block
polling or other clients. A reconnect may first receive a snapshot explicitly
marked stale; the immediate full sweep then supplies fresh state and clears the
marker. Never use SSE as proof that a shutter completed its travel.

## 13. HTTP API

Keep commands capability-oriented and versioned:

```text
GET  /api/v1/status
GET  /api/v1/project
GET  /api/v1/state
GET  /api/v1/events
POST /api/v1/project/reload
POST /api/v1/devices/{deviceID}/commands
```

Command bodies are JSON:

```json
{"action":"on"}
{"action":"off"}
{"action":"toggle"}
{"action":"raise"}
{"action":"lower"}
{"action":"stop"}
{"action":"tiltOpen"}
{"action":"tiltClose"}
{"action":"shortPress"}
{"action":"longPress"}
{"action":"activate"}
```

Reject actions not advertised by the device's capabilities. `tiltOpen` and
`tiltClose` are advertised only for devices classified as jalousies, and the
capability entry must carry an explicit experimental marker so the UI can label
it (see §11).

Return a generated command ID and distinguish these outcomes:

```text
202 Accepted       queued behind at most one in-flight STM operation
400 Bad Request    malformed JSON
404 Not Found      unknown stable device ID
409 Conflict       action unsupported or project revision changed
503 Unavailable    STM disconnected or scheduler stopped
504 Gateway Timeout STM call exceeded its deadline
```

Do not expose generic XML-RPC, raw telegram, arbitrary module/channel, filesystem,
or configurable proxy endpoints to the browser.

## 14. Unauthenticated local HTTP security boundary

The MVP has no user authentication. Plain HTTP provides no confidentiality and
no cryptographically meaningful way to distinguish an authorized user from any
other client that can reach the listening port. Do not describe a password,
cookie, URL capability, or bearer token sent over this connection as secure.

The bridge also lowers the practical barrier compared with direct STM access.
The STM is unauthenticated, but using it requires knowledge of its malformed
XML-RPC transport, methods, addresses, and command encoding. The bridge adds an
enumerable JSON API and a self-describing control website. Any permitted client
can discover the installation model, subscribe to readable state, and invoke
every exposed action, including shutters and security-sensitive actions.
Protocol obscurity was never a proper security control, but removing it still
increases the reachable attack surface and must be acknowledged as such.

Therefore, treat every device that can connect to the bridge as fully
authorized. A guest phone, compromised smart appliance, television, or other
host on the same permitted segment can call the API directly. `Host`, `Origin`,
JSON content-type, CORS, and browser confirmation checks do not stop such a
client; they protect only against browser-mediated cross-origin requests and
accidental interaction.

The actual authorization boundary for this profile is membership of the
trusted home LAN. The deployment deliberately accepts that every device on that
LAN can use the website and API. Guest access should follow whatever isolation
policy the home network already applies, but this plan does not require a new
VLAN, per-device allowlist, or separate controller network.

Still defend against unrelated websites using a browser to target the bridge:

1. Bind to the selected LAN address, not every interface by default.
2. Restrict the listening port to the home LAN subnet with the Pi firewall.
3. Configure one exact public origin and validate the `Host` header.
4. Require an exact matching `Origin` on every mutating request.
5. Require `Content-Type: application/json` and reject form submissions.
6. Send no `Access-Control-Allow-Origin` header.
7. Reject unexpected methods before reading large bodies.
8. Apply small request-body limits and strict JSON decoding.
9. Set `Content-Security-Policy` to permit resources and connections only from
   the same origin.
10. Set `X-Content-Type-Options: nosniff` and deny framing.
11. Never load scripts, fonts, analytics, or other active content from a CDN.

`Sec-Fetch-Site` can be checked as additional defence but must not replace
`Host`, `Origin`, method, and content-type validation.

Panic confirmation protects against accidental interaction in the legitimate
UI. It is not authorization and does not protect against a caller already able
to use the API. Rate limits and the serialized STM scheduler are resilience
controls, not authorization.

If remote access is added later, place publicly trusted HTTPS/VPN identity and
real authentication in front of the same loopback-bound Go server. That is a
new deployment profile with its own threat model, not an invisible extension
of this MVP.

## 15. Cache and local data

Store normalized project data under `/var/lib/phc-bridge`, keyed by a
non-sensitive STM identity hash. Write atomically using a temporary file,
`fsync` where appropriate, and rename. Include a schema version and reject
incompatible cache files.

The cache reveals room and device names if the Pi's SD card is removed. Document
that risk and support a no-cache mode if desired. Do not log full project XML,
facility identifiers, device names, addresses, command payloads, or archive
bytes at normal log levels.

Ignore the STM `readFile` response's `crc` value. Its meaning and covered bytes
are not established, and the Swift implementation does not use it. Decode and
discard the field if response-shape validation requires it; do not compare,
cache, or describe it as an integrity check. Rely on chunk order and totals,
strict size limits, valid ZIP structure, ZIP entry CRCs, and bounded XML parsing.

## 16. Configuration

Use flags with environment-variable fallbacks. An initial deployment does not
need YAML.

```text
PHC_STM_ADDRESS=192.168.1.50:6680
PHC_LISTEN_ADDRESS=192.168.1.20:8080
PHC_PUBLIC_ORIGIN=http://phc-bridge.local:8080
PHC_STATE_DIR=/var/lib/phc-bridge
PHC_ACTIVE_POLL_INTERVAL=2.5s
PHC_SUBSCRIBER_GRACE_PERIOD=15s
PHC_IDLE_HEALTH_INTERVAL=0
PHC_PROJECT_CACHE=true
PHC_LOG_LEVEL=info
```

These addresses are examples only. Never commit the real STM or Pi address.
Reject invalid addresses, ambiguous origins, unwritable state directories, and
poll or grace intervals outside conservative bounds. Setting the idle health
interval to zero disables that optional probe. The listen port must not collide
with other services already on the Pi — notably the CUPS print server on 631; see
§17.

## 17. Deployment

Build a static `linux/arm64` executable and install it as:

```text
/usr/local/bin/phc-bridge
```

Run it under a dedicated account with a systemd unit containing at least:

```ini
User=phc-bridge
Group=phc-bridge
StateDirectory=phc-bridge
EnvironmentFile=/etc/phc-bridge/bridge.env
ExecStart=/usr/local/bin/phc-bridge
Restart=on-failure
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
CapabilityBoundingSet=
```

The firewall should admit the website port only from the trusted home LAN
subnet.
The service must remain useful when started before the STM is reachable: serve
the interface, report disconnected state, retry with bounded backoff, and avoid
a restart loop.

### Coexistence with CUPS on the same Pi

The Pi also runs a CUPS print server, which owns TCP **631** (IPP, plus its own
web admin UI on that port). The bridge is an independent, unprivileged systemd
service and must not collide with it:

- bind the bridge to the Pi's LAN address on **8080** (`PHC_LISTEN_ADDRESS`);
  never 631, and leave 80/443 free;
- the bridge systemd unit has no `Requires`/`After` relationship with
  `cups.service`, and vice versa — either may start, stop, or restart alone;
- the firewall admits 8080 from the trusted LAN; CUPS's own 631 exposure is
  managed separately and is outside this project's scope;
- resource sharing is a non-issue in practice: subscriber-aware polling (§12)
  keeps the bridge near-idle when no browser is observing, so a print job and the
  bridge do not meaningfully compete for the Pi 3's CPU or I/O.

## 18. Testing

### Automated tests

- valid and malformed STM response fixtures;
- fragmented network reads and timeout paths;
- XML-RPC values, arrays, structs, base64, and faults;
- project chunk ordering and resource limits;
- ZIP central-directory extraction and hostile archive cases;
- a generated Swift unit-test target and Go tests consuming the same versioned
  parser and command fixtures;
- normalized parity output that omits no field except fields explicitly marked
  volatile by the fixture schema;
- deterministic IDs and cache migration/rejection;
- exact command sequences for every capability;
- command priority over polling;
- first-subscriber immediate sweep, active cadence, final-subscriber grace
  period, idle stop, and rapid reconnect behaviour;
- bitmask state decoding;
- SSE snapshot, change, heartbeat, slow-client, and reconnect behaviour;
- template rendering with long German and project-derived labels;
- rendered DOM-anchor snapshots plus a browser contract test that drives the
  real JavaScript updater against those anchors;
- strict `Host`, `Origin`, JSON, method, and body-size enforcement;
- command rejection for unsupported capabilities;
- graceful shutdown and cancellation.

Use only synthetic or aggressively redacted fixtures in git.

### Long-running stability

The bridge is a permanent daemon on constrained hardware, so resource leaks
matter more than peak throughput. Cover explicitly:

- repeated SSE connect/disconnect cycles leave no goroutines, timers, or file
  descriptors behind, and the subscriber registry returns to empty;
- repeated subscriber-presence transitions (active cadence, grace period, idle
  stop, immediate resume) do not accumulate pollers or duplicate sweeps;
- sustained polling with an unreachable STM neither grows memory nor tightens
  into a hot retry loop;
- one connection per STM call does not exhaust ephemeral ports or leave sockets
  in `CLOSE_WAIT` over long runs.

Run `go test -race ./...`, and run a multi-hour soak on the Raspberry Pi with a
browser attached and detached, recording goroutine count, heap, and descriptor
count at start and end. A soak run is a release gate, not part of ordinary
`go test ./...`.

### Continuous integration

Shared fixtures only prevent drift if both suites execute them automatically. CI
must, on every change:

- run `go build`, `go vet`, `go test ./...`, and `go test -race ./...`;
- run the Swift `PHCRemoteControlTests` target;
- fail when either suite skips a fixture present in `protocol-fixtures/`, so a
  fixture cannot be added to one language only;
- cross-compile the `linux/arm64` binary to catch build breakage before deploy.

A parity fixture that is not executed by both suites in CI is treated as a
failing test, not as documentation.

### Browser verification

Test the real served interface at iPhone and desktop widths. Verify:

- no overlapping or clipped controls;
- stable control dimensions during state changes;
- live updates reaching the intended stable `data-device-id`/`data-role`
  elements;
- floor/category navigation with JavaScript enabled and basic navigation
  without it;
- reconnect after backgrounding and foregrounding;
- German and English chrome;
- long project names;
- disconnected and stale-state displays;
- confirmation before panic/security commands;
- Home Screen launch through the stable local origin.

### Hardware smoke tests

1. `whoAreYou` succeeds.
2. Project download and parse match the Swift app's counts.
3. One light and one outlet switch and report state.
4. One shutter raises, stops midway, and lowers.
5. The motorized roof window raises, stops midway, and lowers.
6. One fallback button sends both supported sequences.
7. Panic/security confirmation prevents an accidental UI command.
8. The first SSE subscriber triggers a fresh state sweep, and browser state then
   follows physical light changes within the active polling interval.
9. Fast polling stops after the last subscriber and grace period.
10. Commands remain responsive while polling all modules.
11. The bridge recovers after STM and Pi restarts.

## 19. Implementation phases

### Phase 0: delivery spike

- Configure the Pi's reserved address and stable hostname.
- Serve a synthetic Go page with one JSON button and an SSE counter.
- Test Safari, Home Screen launch, background/foreground reconnect, and Pi
  restart using plain HTTP.
- Record the proven origin and network assumptions in `bridge/README.md`.

Acceptance: the actual iPhone can repeatedly launch and interact with the Go
service without installing a CA.

### Phase 1: scaffold and transport probe

- Create `bridge/go.mod`, package layout, test commands, and README.
- Add build output to `.gitignore`.
- Define a versioned language-neutral fixture schema for normalized parser
  output and input-to-command sequences.
- Add `PHCRemoteControlTests` to `project.yml`, include
  `protocol-fixtures/` as test resources, and make Swift tests consume the
  corpus. Refactor command planning into a pure Swift helper where necessary so
  exact event sequences can be asserted without networking.
- Add the Go fixture loader and schema-validation test before implementing Go
  parser behaviour against it.
- Capture one direct raw STM response.
- Implement, compare, and test the bounded transport candidates.
- Implement `stm-probe whoami`.

Acceptance: `go test ./...` passes on Mac and Pi, the Swift unit-test target runs
the shared corpus, and the probe identifies the real STM without permissive
parsing. A fixture is not accepted unless both test suites execute it.

### Phase 2: XML-RPC and project download

- Complete the typed XML-RPC codec and client methods.
- Implement bounded chunk download and ZIP extraction.
- Add typed errors and sanitized diagnostics.

Acceptance: the project downloads on hardware and required entries are found
without logging or persisting raw installation data.

### Phase 3: project model and parser

- Port ppfx parsing and current Swift fallback behaviour.
- Port only the selected tpfx actions exercised by the shared Swift fixtures.
- Leave cpfx out of the implementation and acceptance criteria; any future cpfx
  work starts as separately documented research with no claimed Swift parity.
- Define and implement the common stable-ID contract in Swift and Go before
  freezing API v1 fixture outputs.
- Add deterministic IDs, normalized cache, and shared fixtures.

Acceptance: both suites produce the same normalized output for every shared
ppfx/tpfx fixture, including stable IDs, and a redacted real-project summary
matches Swift floor, category, device, and capability counts.

### Phase 4: controller, commands, and polling

- Implement the single STM scheduler with command priority.
- Add exact command mappings and state polling.
- Publish snapshots and changes internally.
- Measure direct STM round trips on the Raspberry Pi for `whoAreYou`, one AMD
  state read, and each command class. Then measure bridge command latency while
  idle and during an active poll sweep.

Acceptance: hardware command sequences are exact, polling remains stable, and a
user command waits for no more than one active poll request. Record p50/p95
direct and bridged measurements, then set a regression threshold justified by
that baseline. Do not impose an unmeasured 500 ms target. Project download and
reload latency is reported separately and excluded from command-latency
acceptance; the scheduler must still yield between reload chunks so a command
does not wait for the complete download.

### Phase 5: website and API

**Implemented.** The bridge now ships the embedded server-rendered website,
versioned JSON API, SSE stream, exact-origin HTTP protections, English/German
chrome, browser-local favourites, and DOM-anchor/API/security tests described
below. Automated and real-STM HTTP/SSE checks pass. Interactive screenshot
verification remains a release check on a machine with an attached browser and
at the final Raspberry Pi origin.

- Implement templates, responsive CSS, JSON handlers, SSE, and small vanilla
  JavaScript enhancements.
- Use Go templates as the sole structural renderer and implement the stable DOM
  anchor contract for state-only JavaScript patches.
- Apply the local HTTP security checks before enabling mutation routes.
- Add English and German chrome.
- Verify screenshots and interactions at mobile and desktop sizes.

Acceptance: the browser can navigate the project, receive state, and execute
every supported action without a frontend build system or service worker. The
rendered-DOM/JavaScript contract test fails when a required anchor or selector
is deliberately changed.

### Phase 6: deployment and recovery

- Add systemd packaging, hardening, firewall instructions, and install/update
  documentation.
- Verify cache startup, explicit project reload, retries, graceful shutdown,
  Pi reboot, and STM outage recovery.

Acceptance: one binary starts at boot and serves the proven local origin with
the documented network boundary.

## 20. Definition of done

The website bridge MVP is complete when:

- one static Go binary serves the HTML, assets, JSON API, and SSE stream;
- no service worker, CA installation, reverse proxy, Node.js process, or
  frontend build pipeline is required;
- the selected STM response reader accepts only the proven defect;
- project parsing matches the supported Swift model;
- lights, outlets, shutters, the roof window, scenes, and fallback controls work
  on real hardware;
- jalousie tilt is offered only for jalousies and is labelled experimental in
  both the API capabilities and the UI;
- favourites persist per browser, resolve against the current project, and never
  reach the bridge or its cache;
- readable state reaches connected browsers and stale state is explicit;
- active polling follows SSE subscriber presence and stops after the configured
  final-subscriber grace period;
- Go templates and the JavaScript state updater pass their shared DOM-anchor
  contract test;
- panic/security actions require UI confirmation;
- mutation endpoints enforce the configured local origin and JSON-only policy;
- the Pi firewall limits access to the trusted home LAN subnet;
- no real project, capture, address, secret, or home-layout data is committed;
- CI runs both the Go and Swift suites on every change and fails when a shared
  fixture is executed by only one of them;
- a multi-hour soak on the Pi shows no goroutine, descriptor, or memory growth
  across browser connect/disconnect cycles;
- both Swift and Go execute the shared fixture corpus, and automated, browser,
  hardware, reboot, and outage tests pass.

## 21. Instructions for the implementing agent

Before editing bridge code:

1. Read the root `AGENTS.md`, this file, `PROTOCOL.md`, and `ARCHITECTURE.md`.
2. Inspect the current Swift client and parser because they may have evolved.
3. Preserve unrelated dirty worktree changes.
4. Keep `project/`, captures, facility data, addresses, and logs out of git.
5. Complete Phase 0 on the actual target iPhone before implementing the STM
   service.
6. Complete Phase 1 and prove raw response handling before building the parser
   or website.
7. Use synthetic fixtures for every hardware-derived protocol exception.
8. Stop and document evidence when observed hardware behaviour conflicts with
   an assumption; do not hide the conflict behind parser leniency.

The initial implementation should end each phase with working tests and a small
hardware-verifiable increment. Do not build the entire website before proving
the transport, and do not expose generic STM operations merely because they are
convenient during development.
