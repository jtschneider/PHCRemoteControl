# Go PHC Bridge Implementation Plan

> This is the earlier broad bridge plan. The current local-website handoff is
> [GO_WEBSITE_BRIDGE_PLAN.md](GO_WEBSITE_BRIDGE_PLAN.md). Keep this document as
> protocol and design background, but use the newer plan for implementation.

This document is the implementation handoff for a small Go service that runs
on a Raspberry Pi and exposes a same-origin browser UI and API for a
PEHA/Honeywell PHC installation. It is intended to be detailed enough that a
new agent can begin implementation without repeating the protocol research.

The bridge is needed because a browser cannot call the STM reliably: browsers
provide no raw TCP API, the STM response contains a malformed HTTP header, and
project loading requires XML-RPC plus ZIP processing. The browser talks only to
the bridge. The bridge talks to the STM over the trusted home LAN.

Protocol facts remain authoritative in [PROTOCOL.md](PROTOCOL.md). The current
Swift implementation in `Sources/Client/STMv3Client.swift` and
`Sources/Client/PHCProjectParser.swift` is the behavioural reference.

## 1. Goal

Build a long-running Go service with these responsibilities:

1. Connect to one configured STM v3 at TCP port 6680.
2. Accommodate the STM's one precisely known malformed HTTP response line.
3. Call the four XML-RPC methods already verified by the iOS app.
4. Download, validate, unzip, and parse the PHC project.
5. Present floors, categories, devices, capabilities, and current state as JSON.
6. Execute light, outlet, shutter, scene, and fallback-button commands.
7. Poll AMD module state once per module every 2.5 seconds.
8. Publish state changes to browser clients using Server-Sent Events (SSE).
9. Run as an unprivileged systemd service on a Raspberry Pi 3.
10. Serve a responsive Home Screen web app and API from one origin, using the
    delivery profile selected before implementation begins.

After the delivery spike, the first Go deliverable is a command-line
`whoAreYou` probe with the selected malformed-response handling and fixture
tests. Do not start the production web UI before the transport is proven.

## 2. Non-goals

The initial bridge will not:

- expose the STM directly to the internet;
- accept an STM host supplied by an API request;
- implement dimmers before real dimmer hardware is available;
- claim shutter position or movement feedback that the STM does not provide;
- support multiple STMs;
- reproduce every tool from the decompiled PHC configuration software;
- replace the existing iOS app;
- implement HomeKit, Matter, MQTT, or Home Assistant in the first version;
- use the PHC RS-485 protocol directly;
- write its own ZIP decompressor or full HTTP parser.

It will also not require users to install a private root CA on their phones.

## 2.1 Browser delivery is a precondition

The original plan incorrectly deferred browser-trusted TLS until late in the
project. That is too risky because delivery is the architectural reason for the
bridge. Select and test one of these profiles before implementing the STM
transport.

### Profile A: local HTTP Home Screen app (recommended MVP)

```text
iPhone -> http://phc-bridge.local:8080 -> Go bridge -> STM
```

Safari and WebKit allow ordinary HTTP or HTTPS websites to be added to the iOS
Home Screen. A service worker still requires a secure context, but this control
app does not need one: when the Pi or LAN is unavailable, an offline shell
cannot control the house anyway. Do not cache or queue physical commands for
later replay.

Consequences:

- no certificate, CA profile, reverse proxy, domain, or renewal process;
- the app can use a manifest/icon and open in standalone mode on iPhone;
- all HTML, JSON, and SSE come from the same HTTP origin;
- Android can use the responsive site or a Home Screen bookmark, but full PWA
  installability may require HTTPS;
- there is no transport confidentiality on the LAN, matching the STM's own
  unauthenticated HTTP trust model;
- reusable passwords or bearer tokens must not be presented as secure over
  this profile.

Bind explicitly to the Pi's LAN address and restrict the port to the home subnet
with the host firewall. Mutations must require JSON, reject cross-origin
requests, validate `Host` and `Origin`, and send no permissive CORS headers.
Give the Pi a reserved address and stable hostname through the FRITZ!Box or
configure mDNS/Avahi deliberately; `.local` is an example, not an assumed DNS
feature.

### Profile B: public-trust certificate on a private LAN

```text
iPhone -> https://phc.example.net -> private Pi address -> bridge -> STM
```

Use a real registrable domain and obtain a publicly trusted certificate through
an automated ACME DNS-01 challenge. DNS-01 works even when the web server is not
publicly reachable. Local or split-horizon DNS resolves the chosen hostname to
the Pi. This avoids installing a private CA on every device.

This profile requires:

- a domain and a DNS provider with an automation API;
- narrowly scoped DNS credentials or delegated `_acme-challenge` records;
- automatic renewal and expiry monitoring;
- a stable local DNS answer;
- testing for router DNS-rebinding protection;
- confirmation that iOS installation, service workers, SSE, and reconnection
  work through the exact hostname.

No router port forwarding is required for DNS-01. The private key and DNS API
credential remain sensitive operational state.

### Profile C: Tailscale HTTPS

Tailscale can issue a publicly trusted certificate for a `*.ts.net` MagicDNS
name and provide authenticated remote reachability. It avoids a private CA but
requires the iPhone and Pi to participate in the tailnet. Treat that dependency
as an explicit product choice, not as "local HTTPS for free".

### Rejected default: Caddy local CA or mkcert

A private CA can technically work after its root is installed and manually
enabled for full SSL trust on each iPhone. That enrollment and lifetime
management is disproportionate for this app and is not the default plan. More
importantly, compromising the Pi-held CA private key would let an attacker issue
certificates trusted by every enrolled device. Use it only for development or
after a deliberate household-device-management choice.

### Delivery spike acceptance

Before Phase 0, serve a tiny synthetic page with a button and an SSE counter on
the Pi. On the real target iPhone:

1. Open it through the candidate hostname and profile.
2. Add it to the Home Screen.
3. Confirm standalone launch after Safari has been closed.
4. Confirm a JSON mutation reaches the Pi.
5. Confirm SSE reconnects after backgrounding and foregrounding.
6. Reboot the Pi and confirm the saved icon still opens the service.
7. For HTTPS profiles, advance or simulate renewal and verify certificate
   replacement without reinstalling the Home Screen app.

Record the selected profile and result in `bridge/README.md`. Stop the project
if none of the profiles is acceptable.

Primary references for this decision:

- [WebKit: Safari 26 Home Screen web apps](https://webkit.org/blog/17333/webkit-features-in-safari-26-0/)
- [W3C Secure Contexts](https://www.w3.org/TR/secure-contexts/)
- [Apple: trusting manually installed root certificates](https://support.apple.com/en-ie/102390)
- [Let's Encrypt: DNS-01 challenges](https://letsencrypt.org/docs/challenge-types/)
- [Tailscale: HTTPS certificates](https://tailscale.com/docs/how-to/set-up-https-certificates)

## 3. Environment and version policy

Development and deployment currently use Go 1.26.5:

```text
Mac:          go1.26.5 darwin/arm64
Raspberry Pi: go1.26.5 linux/arm64
```

Use Go modules and declare Go 1.26 in `bridge/go.mod`. Keep the STM transport
free of cgo so a static Raspberry Pi binary can be cross-compiled on the Mac:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOARM64=v8.0 \
  go build -trimpath -o dist/phc-bridge-linux-arm64 ./cmd/phc-bridge
```

Native builds on the Pi are useful for diagnostics but are not required for
deployment. The checked-in source, not a binary built on either machine, is the
source of truth.

## 4. System architecture

```text
Home Screen web app in Safari
    |
    | same-origin HTML, JSON API, SSE
    v
phc-bridge (Go, systemd, unprivileged)
    |
    | XML-RPC over malformed HTTP/1.0 responses
    v
STM v3 :6680
    |
    | PHC bus managed by STM
    v
AMD / EMD / JRM modules
```

In Profile A, the Go service serves the UI and API directly from an explicitly
configured Pi LAN address. In Profiles B and C, it listens on
`127.0.0.1:8080` behind the selected TLS terminator. In every profile the UI and
API use one origin, so CORS is unnecessary.

The working native iOS app remains the direct-control fallback. The bridge adds
an always-on Pi to its own browser control path; it must not become a dependency
of the native app.

The code has four main boundaries:

```text
HTTP API -> controller/service -> project + live state -> STM client
                                                     -> XML-RPC transport
```

Only the STM client knows module addresses and XML-RPC details. API handlers
look up a device by stable ID and call a domain-level operation.

## 5. Proposed repository layout

Create a self-contained Go module under `bridge/`:

```text
protocol-fixtures/                # synthetic, language-neutral protocol cases
bridge/
  go.mod
  go.sum                         # only when external dependencies exist
  README.md                      # build, run, configure, deploy
  cmd/
    phc-bridge/
      main.go                    # production service
    stm-probe/
      main.go                    # diagnostic whoAreYou/protocol probe
  internal/
    config/
      config.go                  # flags/environment validation
    stm/
      client.go                  # typed STM operations
      transport.go               # configured net/http transport
      sanitize_conn.go           # one-line response compatibility layer
      transport_test.go
      testdata/                  # synthetic malformed response fixtures
    xmlrpc/
      encode.go
      decode.go
      value.go                   # typed XML-RPC value representation
      xmlrpc_test.go
    project/
      download.go                # readFile loop and ZIP limits
      archive.go                 # archive/zip extraction
      parser.go                  # ppfx parser
      tools.go                   # selected tpfx action parser
      keywords.go                # classification vocabulary
      parser_test.go
      testdata/                  # synthetic project files only
    phc/
      model.go                   # Project, Floor, Device, State, capabilities
      controller.go              # domain operations and STM command mapping
      poller.go                  # per-module state polling
      state.go                   # synchronized snapshot and subscriptions
      cache.go                   # atomic normalized-project cache
    api/
      server.go
      routes.go
      middleware.go              # auth, origin, limits, request IDs
      handlers.go
      events.go                  # SSE
      api_test.go
  deploy/
    phc-bridge.service
    phc-bridge.env.example
  dist/                          # gitignored build output
```

Use the module path:

```text
github.com/jtschneider/PHCRemoteControl/bridge
```

Keep dependencies minimal. The standard library is sufficient for HTTP, XML,
ZIP, JSON, SSE, logging, testing, and signals. Add an external package only if
it removes meaningful protocol risk and document why it is needed.

`protocol-fixtures/` is shared by Swift and Go tests. Store synthetic XML-RPC
responses, ppfx/tpfx inputs, expected normalized JSON, and command-sequence
vectors there. Never place a real project or unredacted capture in this corpus.
Both implementations must consume the same expected results so protocol and
classification changes cannot silently drift.

## 6. STM HTTP compatibility layer

### 6.1 Exact defect

The mitmproxy capture shows responses beginning like this:

```http
HTTP/1.0 200 OK
Thu, 11 Jun 2026 21: 56:00
Server: INFRATEC_CTM/3.0
Content-Type: text/xml
Content-Length: 303
Proxy-Connection: close
Pragma: no-cache

<methodResponse>...</methodResponse>
```

The second line is intended as a date but lacks the `Date:` field name. It also
contains colons in the time. Go's `net/http` parser therefore treats the text
before the first time colon as an invalid header name and rejects the response.

All 55 responses in the available capture have one such line immediately after
the status line. The other headers and body framing appear normal. However,
`Proxy-Connection` and other details may have been added or rewritten by the
proxy. Phase 1 must capture the raw response header from a direct TCP connection
before treating this shape as authoritative. Do not commit identity or project
data from that capture.

### 6.2 Candidate design and decision gate

Use a normal `http.Transport` with a custom `DialContext`. The dialer returns a
`net.Conn` wrapper that sanitizes the first response header block and delegates
everything else to the real connection. This remains the preferred candidate,
not an assumption that bypasses comparison.

Recommended transport settings:

```go
&http.Transport{
    Proxy:                  nil,
    DisableKeepAlives:      true,
    DisableCompression:     true,
    MaxResponseHeaderBytes: 16 << 10,
    DialContext:            dialSanitizedSTM,
}
```

Use one request per connection. This matches the STM's HTTP/1.0 close behaviour
and ensures each sanitizer instance sees exactly one response.

Also prototype a small STM-specific response reader against the same synthetic
fixtures. It may be selected instead if the raw capture confirms a fixed
HTTP/1.0, `Content-Length`, connection-close response and the purpose-built
reader is demonstrably simpler. Such a reader must remain strict and bounded;
it is not a generally permissive HTTP parser. Record the comparison in package
documentation before choosing either implementation.

### 6.3 Sanitizer algorithm

On the first `Read` from the wrapped connection:

1. Wrap the underlying connection in a `bufio.Reader`.
2. Read the status line and headers through the first empty line.
3. Reject a header block larger than 16 KiB or an individual line larger than
   a small documented limit such as 4 KiB.
4. Preserve the status line exactly.
5. Inspect only the first line after the status line.
6. Drop it only if it matches the known malformed STM date shape.
7. Preserve all remaining header lines and their order.
8. Replay the sanitized block to `net/http`, followed by all bytes already
   buffered from the body and then the underlying connection.
9. Forward all subsequent reads, writes, addresses, deadlines, and `Close` to
   the wrapped connection.

A suitable strict matcher is conceptually:

```text
^(Mon|Tue|Wed|Thu|Fri|Sat|Sun), [0-9]{1,2}
 (Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)
 [0-9]{4} [0-9]{2}: ?[0-9]{2}:[0-9]{2}$
```

Implement it as one anchored Go regular expression without line breaks. A
normal `Date: ...` header must pass through untouched. A different malformed
line must not be removed; let the standard parser reject it.

The wrapper must work when TCP delivers the response one byte at a time. Never
assume a single `Read` contains a complete line or header.

### 6.4 Do not do these things

- Do not patch or fork the Go standard library.
- Do not copy `net/http` internals.
- Do not discard every nonconforming header line.
- Do not search for the date pattern in the body.
- Do not weaken parsing on the incoming browser-facing server.
- Do not silently fall back to a raw permissive parser; any STM-specific reader
  must be an explicit, tested Phase 1 decision.

### 6.5 Transport timeouts and limits

Apply both HTTP-client and connection deadlines:

- dial timeout: 3 seconds;
- response-header timeout: 5 seconds;
- ordinary RPC timeout: 10 seconds;
- project chunk timeout: 20 seconds if hardware testing shows 10 is too short;
- maximum HTTP header: 16 KiB;
- maximum XML-RPC response body: 24 MiB.

Read bodies through `io.LimitedReader` with one extra byte so oversized bodies
are detected rather than silently truncated. Always close the response body.

Do not automatically retry mutation calls. A retry of `simInputEvent` can turn
one shutter or scene action into two physical actions.

## 7. XML-RPC implementation

Use `encoding/xml`. Do not port the Swift string-search response parser.

Only these methods are required initially:

| Method | Parameters | Result used by bridge |
| --- | --- | --- |
| `service.stm.whoAreYou` | none | identity struct |
| `service.stm.readFile` | `0, chunkIndex, 1` | `cur`, `total`, `crc`, `bin` |
| `service.stm.sendTelegram` | `0, busAddress, content` | integer array |
| `service.stm.simInputEvent` | `0, module, channel, event, 4` | success/fault |

Implement a small XML-RPC value decoder that supports:

- `<i4>` and `<int>`;
- `<string>` and implicit string values;
- `<base64>` with whitespace;
- `<array><data>...`;
- `<struct><member>...`;
- `<fault>`.

The STM declares ISO-8859-1 in responses. Configure `xml.Decoder.CharsetReader`
so umlauts are preserved. This can be a small bounded ISO-8859-1-to-UTF-8
reader; an external character-set package is not required.

Method names are constants, not user input. Encode integer parameters with
`encoding/xml` or a fixed encoder that still XML-escapes strings. Return typed
errors with operation context but never include complete XML bodies or project
data in logs.

## 8. Typed STM client

`internal/stm.Client` owns the endpoint, HTTP client, and an RPC serialization
gate. Expose domain-neutral methods such as:

```go
type Client interface {
    WhoAreYou(ctx context.Context) (Identity, error)
    ReadProjectChunk(ctx context.Context, index int) (ProjectChunk, error)
    SendTelegram(ctx context.Context, moduleAddress, content int) ([]int, error)
    SimInputEvent(ctx context.Context, module, channel, event int) error
}
```

Serialize calls to the STM. A simple mutex around each complete XML-RPC call is
adequate initially. Release it between project chunks so a control command does
not wait for the entire project download. Polling should wait behind a user
command rather than issuing concurrent requests.

Validate every integer before encoding it. In particular:

- module and channel ranges must be valid for the project model;
- content must fit the expected PHC command range;
- event must be one of `2`, `3`, `4`, or `5`;
- key type is always `4` and is not caller-selectable.

## 9. Project download and archive handling

Port the limits already used by the Swift app:

```text
maximum readFile chunks:       4096
maximum accumulated ZIP:       16 MiB
maximum extracted XML file:     8 MiB
maximum XML-RPC response:       24 MiB
maximum visible channels:       4096
maximum channel label:          512 characters
maximum selected tool actions:  256
```

For each `readFile(0, index, 1)` response:

1. Require `total` in `1...4096`.
2. Require `cur == requested index`.
3. Require `cur < total`.
4. Decode non-empty base64.
5. Check the accumulated ZIP limit before appending.
6. Stop only when `cur == total - 1`.

The response also contains `crc`, but its exact scope has not been established.
Parse it for fixture parity but do not claim to validate it until its algorithm
and covered bytes are proven. ZIP entry CRCs still provide archive-level
integrity checking.

Use the standard library `archive/zip` reader. It reads the central directory
and supports entries using data descriptors, which is necessary because the STM
sets ZIP general-purpose flag bit 3 and leaves local-header sizes at zero.

Extract only named files:

- `project.ppfx` is required;
- `project.tpfx` is optional initially but required for panic/presence tools;
- `project.cpfx` may be retained for later research but is not parsed initially.

Check `UncompressedSize64` before opening an entry, then enforce the same limit
while reading. Reject duplicate required entry names, encrypted entries, path
traversal names, and malformed archives. Do not extract the archive to disk.

## 10. Project parser parity

Port behaviour from `PHCProjectParser.swift`; do not redesign classification in
the first pass.

Parse XML as a stream with `encoding/xml.Decoder`. Collect only visible
channels and the surrounding module metadata required to build devices.

The expected channel convention is:

```text
N.FLOOR : CATEGORY > LABEL
```

The parser must:

1. Preserve the verbatim text between `:` and `>` as the UI category.
2. Use `N` to order floors, falling back to 99.
3. Use the text after `>` as the device label.
4. Parse visible AMD output channels as light/outlet devices.
5. Pair motor-like EMD input channels by removing only a final direction word.
6. Recognize both shutters and mechanically driven roof windows as motors.
7. Keep the down EMD reference as the primary reference and the up reference as
   the secondary reference.
8. Parse `EMD_VIR` channels as momentary scene actions unless already consumed
   as a paired motor.
9. Expose every remaining visible EMD input as a fallback button with separate
   short-press and long-press capabilities.
10. Parse supported panic and presence-simulation tools from `project.tpfx`.
11. Avoid duplicate actions by hardware reference.

Port the current German and English keyword lists from `PHCKeywords.swift` as
data in `keywords.go`. Preserve Unicode project names and labels. Add synthetic
fixtures for umlauts, roof windows, shutters, central actions, unknown EMD
channels, and malformed names.

Do not commit or derive golden fixtures from `project/`, packet captures, or a
real home's exported project. Those files are sensitive and gitignored.

## 11. Domain model and stable identities

The public JSON model should not reproduce Swift UUIDs. Use deterministic,
opaque string IDs derived from the primary hardware reference, for example:

```text
amd-3-10
emd-2-4
```

The server owns the mapping from ID to hardware references. The browser should
not submit raw module addresses.

Suggested internal model:

```go
type DeviceKind string

const (
    DeviceLight   DeviceKind = "light"
    DeviceOutlet  DeviceKind = "outlet"
    DeviceShutter DeviceKind = "shutter"
    DeviceScene   DeviceKind = "scene"
    DeviceButton  DeviceKind = "button"
)

type Device struct {
    ID           string
    Name         string
    Category     string
    Kind         DeviceKind
    PrimaryRef   ChannelRef
    SecondaryRef *ChannelRef
    Capabilities Capabilities
    State        DeviceState
}
```

Public JSON should expose capabilities instead of references:

```json
{
  "id": "emd-2-4",
  "name": "Flur",
  "category": "Lueftung Fenster",
  "kind": "shutter",
  "capabilities": ["raise", "stop", "lower"],
  "state": {
    "power": null,
    "movement": null
  }
}
```

Project-supplied display names remain untranslated. API field names and enum
values remain stable English identifiers. Browser UI localization belongs in
the browser app.

For shutters, do not expose a percentage. A command acknowledgement means only
that the sequence was sent successfully.

## 12. Command mapping

### Lights and outlets

```text
bus address = 0x40 | module DIP
content     = (channel << 5) | command
ON          = command 2
OFF         = command 3
```

Use explicit on/off commands rather than toggle for API requests.

### Shutters and motorized windows

`simInputEvent(0, module, channel, event, 4)` uses:

```text
2 = press
3 = longPress
4 = release
5 = doublePress
```

Verified command sequences:

```text
lower: press(2), release(4), doublePress(5) on down channel
raise: press(2), release(4), doublePress(5) on up channel
stop:  press(2), longPress(3) on down channel
```

Jalousie tilt remains experimental and must be marked as such in code and API
capabilities until tested on real hardware:

```text
tilt close: press(2), release(4) on down channel
tilt open:  press(2), release(4) on up channel
```

### Scenes and fallback buttons

```text
short press: press(2), release(4), doublePress(5)
long press:  press(2), longPress(3)
```

Panic/security actions require an explicit confirmation in the browser UI. The
API must keep these actions distinguishable through a `requiresConfirmation`
property, but must not trust a display label supplied by the browser.

## 13. State, polling, and event delivery

Keep one in-memory project snapshot guarded by a lock. Build the AMD poll plan
once from the loaded project:

```text
module bus address -> [(channel, device ID)]
```

Every 2.5 seconds, call `sendTelegram(0, busAddress, 1)` once for each AMD
module. The final integer in the response is the channel bitmask. Update only
devices whose state changed.

Polling rules:

- start only after a cached or downloaded project is registered;
- run immediately once, then wait 2.5 seconds;
- continue past individual module failures;
- expose degraded STM connectivity in health/status data;
- use bounded backoff when every module fails;
- stop cleanly when the root context is cancelled;
- never fabricate state for EMD actions or shutters.

Publish changes through SSE at `GET /api/v1/events`. Event types should include:

```text
snapshot          complete current state after connection
state             one or more changed device states
project           project was reloaded
connection        STM connectivity changed
```

Each subscriber gets a bounded channel. A slow subscriber must be disconnected
or sent a fresh snapshot; it must never block polling or commands.

SSE is preferred over WebSockets for the first version because updates are
server-to-client only and browser reconnection support is built in.

## 14. Cache

Cache the normalized project and last known pollable states as JSON under the
service state directory, normally:

```text
/var/lib/phc-bridge/project.json
```

Write atomically using a temporary file, `fsync`, rename, and mode `0600`.
Include a cache schema version and the configured STM identity/host key. Ignore
an incompatible or corrupt cache and reload from the STM.

Do not cache the raw ZIP unless a concrete diagnostic need arises. Never write
project XML or device names to ordinary logs. Treat the normalized cache as
sensitive home-layout data.

Mode `0600` protects against other processes, not physical removal of the Pi's
SD card. Document that residual risk. Offer a no-cache mode for deployments
where physical disclosure matters; full at-rest protection otherwise belongs
to disk/filesystem encryption and key management, not custom application
encryption.

Startup sequence:

1. Validate configuration.
2. Load a compatible cache if present.
3. Start the API with cached data marked stale.
4. Probe `whoAreYou`.
5. If no cache exists, download and parse the project before reporting ready.
6. Register pollable devices and start polling.
7. Keep manual project reload protected according to the selected delivery
   profile.

## 15. HTTP API v1

Use JSON with `Content-Type: application/json`. Reject unknown JSON fields and
limit request bodies to a few KiB. Return a stable error envelope:

```json
{
  "error": {
    "code": "device_not_found",
    "message": "Device was not found"
  }
}
```

Initial routes:

| Method | Route | Purpose |
| --- | --- | --- |
| `GET` | `/healthz` | process liveness only |
| `GET` | `/readyz` | project loaded and STM status |
| `GET` | `/api/v1/project` | floors, devices, capabilities, state |
| `GET` | `/api/v1/events` | SSE state stream |
| `POST` | `/api/v1/project/reload` | force download and parse |
| `POST` | `/api/v1/devices/{id}/power` | `{ "on": true }` |
| `POST` | `/api/v1/devices/{id}/shutter` | `{ "command": "raise|stop|lower" }` |
| `POST` | `/api/v1/devices/{id}/activate` | `{ "press": "short|long" }` |

Return `200 OK` with a small command acknowledgement only after all underlying
STM calls in the sequence succeeded. A successful shutter response must not
pretend that movement was observed.

Use method-specific handlers. Do not expose a generic endpoint accepting STM
method names, module addresses, command integers, or arbitrary XML.

## 16. Security boundary

The STM has no authentication and uses plain HTTP, so the bridge becomes the
browser-facing policy boundary. Common requirements in every profile:

- allow exactly one configured STM host and port;
- never implement an open HTTP proxy;
- use same-origin UI/API deployment and no wildcard CORS;
- validate `Host` and exact `Origin` on mutations;
- require `application/json` for mutations and reject missing/foreign origins
  from browser requests;
- rate-limit mutation requests;
- cap headers and request/response bodies;
- log command type and device ID, not labels, XML, or project content;
- run without root and with a read-only filesystem except the state directory;
- keep the native app available when the Pi is down.

Profile A deliberately inherits the STM's trusted-LAN threat model. Bind only
to the selected LAN address, firewall access to the home subnet, and do not
claim that a password or bearer token is protected over HTTP. Any untrusted or
compromised LAN client can already call the unauthenticated STM, but the bridge
also exposes the parsed home layout and therefore should not be placed on a
guest or shared network.

Profiles B and C must additionally:

- bind the Go backend to loopback;
- require HTTPS for UI and API;
- authenticate all `/api/` routes, including SSE;
- use a secure, `HttpOnly`, `SameSite=Strict` session cookie or Tailscale
  identity/ACLs;
- permit unauthenticated `/healthz` only on loopback;
- redirect or reject plain HTTP;
- verify certificate renewal before expiry.

Preferred authenticated order:

1. Tailscale identity/ACLs plus same-origin HTTPS for remote access.
2. Reverse-proxy authentication with a secure session cookie.

Do not put a permanent bearer token into checked-in web JavaScript. Do not add
token theatre to Profile A: a reusable secret sent over HTTP is observable on
the LAN and does not upgrade the transport to an authenticated channel.

UI confirmation for panic actions protects against accidental taps, not a
malicious caller. In Profiles B and C, authentication remains mandatory.

## 17. Configuration

Use flags with environment-variable fallbacks. Avoid a YAML dependency for the
initial service.

Suggested settings:

```text
PHC_STM_ADDRESS=192.168.1.50:6680
PHC_DELIVERY_PROFILE=local-http
PHC_LISTEN_ADDRESS=192.168.1.20:8080
PHC_PUBLIC_ORIGIN=http://phc-bridge.local:8080
PHC_STATE_DIR=/var/lib/phc-bridge
PHC_POLL_INTERVAL=2.5s
PHC_LOG_LEVEL=info
```

The example address above is documentation only. Do not commit the real STM
address or installation identity.

Reject empty hosts, invalid ports, non-IP hosts if DNS is not intentionally
supported, state directories that cannot be secured, and polling intervals
below a conservative minimum such as one second. Profiles B and C override the
listen address to loopback and add their authentication configuration.

## 18. Logging and diagnostics

Use `log/slog` with text output under systemd and optional JSON output for
development. Include operation, duration, request ID, and sanitized error class.

Never log:

- XML-RPC response bodies;
- base64 project chunks;
- project ZIP/XML;
- facility/device IDs at info level;
- device/floor names;
- API tokens or authorization headers.

The `stm-probe` command should support:

```text
stm-probe -stm 192.168.x.x:6680 whoami
```

It should print a redacted identity summary, response timing, and whether the
known malformed line was removed. A verbose mode may print header names but not
body contents.

## 19. Testing strategy

### Unit tests

Header sanitizer tests are mandatory before hardware testing:

- exact captured malformed line is removed;
- valid standard response is unchanged;
- valid `Date:` is unchanged;
- unrelated malformed line is not removed;
- malformed date in any later header position is not removed;
- header split one byte at a time succeeds;
- body bytes buffered with the header are preserved exactly;
- LF/CRLF behaviour is explicitly tested;
- oversized line/header fails;
- early EOF fails;
- deadlines and close operations propagate.

XML-RPC tests:

- identity struct;
- sendTelegram integer array;
- readFile struct and whitespace-containing base64;
- XML-RPC fault;
- malformed XML;
- ISO-8859-1 umlauts;
- oversized body.

Project tests:

- central-directory ZIP with data descriptors;
- missing/duplicate/oversized entries;
- channel parsing and category preservation;
- natural floor ordering;
- AMD light/outlet classification;
- shutter and roof-window pairing;
- unpaired EMD fallback buttons;
- panic and presence tool extraction;
- deterministic IDs;
- all parser limits.

Controller/API tests:

- exact telegram content for on/off;
- exact shutter and button event order;
- no automatic retry of momentary commands;
- unknown IDs and unsupported capabilities;
- delivery-profile policy, origin rejection, and authenticated-profile access;
- strict JSON decoding and size limits;
- SSE initial snapshot and slow-client handling;
- cache corruption and schema mismatch.

Use `net.Pipe` or a local `net.Listener` for fragmented transport fixtures and
`httptest` for the API. Run:

```sh
go test ./...
go test -race ./...
go vet ./...
```

The race detector runs on the Mac build, not the cross-compiled Pi binary.

### Real-hardware smoke tests

Run these manually in order:

1. `whoAreYou` succeeds through the sanitizer.
2. One `readFile` chunk decodes.
3. Full project downloads and parses without logging project data.
4. Poll one AMD module and compare with a known light state.
5. Turn one safe test light on and off.
6. Raise, stop midway, and lower one shutter.
7. Exercise a non-security central action.
8. Confirm the panic action is visible but do not trigger it casually.

Hardware tests must require explicit flags and must never run as part of
ordinary `go test ./...`.

## 20. Deployment

Install the binary outside the source tree, for example:

```text
/usr/local/bin/phc-bridge
```

Use a dedicated service account and a systemd unit with at least:

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

Confirm the service can write only to its state directory and can reach only
the LAN services required by the deployment. Keep secrets in files mode `0600`.

Profile A serves the static web app and API directly. Its systemd environment
must bind the chosen LAN address, and the host firewall must restrict port 8080
to the home subnet.

Profiles B and C bind the Go service to loopback. Their reverse proxy should:

- terminate HTTPS;
- serve the web app and proxy `/api/` on one origin;
- enforce authentication if it owns that responsibility;
- disable public directory listings;
- set appropriate security headers;
- proxy SSE without response buffering;
- never proxy arbitrary destinations from a request parameter.

## 21. Implementation phases

### Phase -1: Delivery viability spike

- Build the tiny synthetic Home Screen/SSE page described in Section 2.1.
- Test Profile A on the actual iPhone first.
- Test Profile B or C only if HTTPS, Android PWA installation, or remote access
  is an actual requirement.
- Record the selected profile and its operational dependencies.

Acceptance: the Home Screen launch and reconnect behaviour is acceptable
without installing a private CA. Otherwise stop and reconsider the browser
route.

### Phase 0: Scaffold

- Create `bridge/go.mod`, package layout, lint/test commands, and bridge README.
- Add build output to `.gitignore`.
- Add no third-party web framework.

Acceptance: `go test ./...` and native Mac build succeed with an empty service.

### Phase 1: Transport probe

- Obtain and inspect one direct, non-proxied raw STM response header.
- Implement and compare the bounded `net.Conn` sanitizer and the strict
  STM-specific response-reader candidate.
- Add malformed/valid/fragmented fixtures.
- Configure `http.Transport` and timeouts.
- Implement `stm-probe whoami` with a minimal XML-RPC decoder.

Acceptance: the selected transport is justified against raw bytes, all transport
tests pass, and `whoAreYou` succeeds on the real STM.

### Phase 2: XML-RPC and project download

- Complete the typed XML-RPC codec.
- Implement all four STM methods.
- Implement the bounded readFile loop and in-memory ZIP extraction.

Acceptance: the real project ZIP downloads, required entries are found, and no
sensitive data is written to logs or disk.

### Phase 3: Project model and parser

- Port ppfx channel parsing, keywords, motor pairing, scenes, and fallback EMD
  buttons.
- Port selected tpfx panic/presence actions as a separately estimated parser
  task with nesting and candidate limits, not as incidental glue.
- Add normalized cache and stable IDs.
- Make Swift and Go consume the shared language-neutral fixtures.

Acceptance: synthetic fixtures pass and a redacted summary of the real project
matches the iOS app's floors, categories, and device counts.

### Phase 4: Controller and polling

- Implement command mapping and serialized STM calls.
- Build per-module poll plans and synchronized state.
- Add cancellation, reconnect status, and changed-state publication.
- Let user commands preempt the poll batch between module calls.
- Measure command latency on the Pi.

Acceptance: test light, shutter start/stop, fallback button, and AMD polling all
work on hardware with exact expected event sequences. A user command waits for
at most one in-flight poll RPC, and end-to-end LAN command acknowledgement has a
provisional p95 target below 500 ms.

### Phase 5: JSON API, SSE, and selected policy

- Implement strict handlers, delivery-profile policy, status routes, and SSE.
- Add authentication middleware only for Profiles B and C.
- Add API tests and stable versioned JSON schemas.

Acceptance: a browser or `curl` can load the project, receive state changes,
and execute safe commands through the bridge only.

### Phase 6: systemd and selected delivery profile

- Add service hardening, state directory, secret handling, and deployment docs.
- Configure direct LAN serving for Profile A or the chosen TLS/authentication
  approach for Profiles B/C.
- Verify Home Screen launch, SSE, and service restart recovery through the
  actual production origin.

Acceptance: the service starts at boot, survives STM unavailability, exposes
only the interface intended by the selected profile, and passes that profile's
deployment security checklist.

### Phase 7: Home Screen web app

- Build the native-feeling responsive app against `/api/v1`.
- Parallel the app's floor lists, categories, names, favourites, and action
  confirmations without copying the SwiftUI layout one-to-one.
- In Profile A, do not add a service worker. In Profiles B/C, add one only if a
  measured user benefit justifies its lifecycle complexity.
- Never cache or queue physical control commands for later replay.

Acceptance: the installed Home Screen app works through the selected origin on
iPhone and cannot accidentally replay a stale command after reconnecting.

## 22. Explicitly deferred decisions

The delivery profile is not deferred; Phase -1 must select it. Remaining
decisions are:

1. Whether the Go binary embeds the built web app or a selected reverse proxy
   serves it.
2. Where user-specific favourites and renamed labels live. Settle their stable
   identity contract before freezing API v1.
3. Whether remote access is allowed at all. If yes, require Profile B or C and
   revisit authentication explicitly.
4. Whether at-rest disclosure of the normalized cache warrants no-cache mode or
   filesystem encryption.

## 23. Definition of done for the bridge MVP

The bridge MVP is complete when:

- it runs from one static `linux/arm64` binary on the Raspberry Pi 3;
- its selected STM response reader accepts only the known malformed header
  defect and rejects unrelated malformed responses;
- it downloads and parses the project with the documented limits;
- its device list matches the proven Swift parser for supported classes;
- lights/outlets, shutters, scenes, and fallback buttons work on hardware;
- AMD state polling reaches browser clients over SSE;
- cached startup and explicit reload work;
- Profile A mutations are same-origin, JSON-only, and LAN-firewall restricted,
  or Profiles B/C mutations are HTTPS-authenticated;
- panic actions are marked for confirmation;
- no real project, capture, address, secret, or home-layout data is committed;
- transport, parser, controller, API, and cache tests pass;
- systemd restart and graceful shutdown are verified.

## 24. Instructions for the implementing agent

Before editing code:

1. Read the root `AGENTS.md`, this file, `PROTOCOL.md`, and `ARCHITECTURE.md`.
2. Inspect the current Swift parser/client because they may have evolved after
   this plan was written.
3. Preserve unrelated dirty worktree changes.
4. Keep real `project/` exports and captures out of tests and commits.
5. Start with Phase -1. Do not implement the bridge until the actual iPhone
   delivery path has passed the spike.
6. Then complete Phase 0 and Phase 1 only; prove the raw malformed response
   handling before building the rest of the bridge.

When protocol behaviour in this plan conflicts with a new hardware capture,
record the capture-derived finding in `PROTOCOL.md`, add a synthetic regression
fixture, and update this plan. Do not broaden parser leniency without a precise,
documented wire example.
