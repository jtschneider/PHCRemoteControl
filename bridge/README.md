# phc-bridge

A Go service that bridges a browser to a PEHA/Honeywell PHC **STM v3** over the
LAN. Implements [docs/GO_WEBSITE_BRIDGE_PLAN.md](../docs/GO_WEBSITE_BRIDGE_PLAN.md).

The **Swift app is the authoritative, hardware-verified reference** for every
command and classification (see the plan's "Reference authority"). The one part
with no Swift counterpart is the STM's malformed HTTP response header, which
`URLSession` tolerates for free but Go's `net/http` rejects — handled by the
sanitizer in `internal/stm/transport.go`.

## Status: Phases 1–6 implemented

Implemented so far:

- `internal/stm/transport.go` — malformed-header sanitizer (`net.Conn` wrapper).
- `internal/stm/xmlrpc.go` — minimal XML-RPC codec (i4/string/base64/array/struct/fault, ISO-8859-1).
- `internal/stm/client.go` — typed STM client, including `WhoAreYou` and
  `ReadFile`.
- `internal/project/download.go` — bounded project download and central-directory
  ZIP extraction, verified against the real STM archive.
- `internal/project/parser.go` — bounded PPFX parser for AMD lights/outlets,
  paired EMD shutters and motorized windows, virtual scenes, and complete
  visible-input fallback buttons.
- `internal/project/tools.go` — bounded TPFX subset for panic and presence-
  simulation actions, including pushbutton candidate preference and PPFX
  deduplication.
- `../protocol-fixtures/` — synthetic parser contract executed by both Go tests
  and the iOS unit-test target, including stable IDs, secondary motor refs,
  Unicode labels, natural ordering, scenes, tools, and fallback controls.
- `internal/controller/` — serialized command-priority scheduler, exact
  capability-checked commands, revisioned snapshots/events, post-command
  refresh, and subscriber-aware AMD polling with a disconnect grace period.
- `internal/web/` — embedded semantic HTML templates, responsive light/dark CSS,
  bilingual English/German chrome, a small vanilla-JavaScript command/SSE layer,
  browser-local favourites with persistent drag/button reordering, strict
  local-origin checks, and the versioned JSON and SSE API.
- `internal/cache/` — atomic, STM-keyed on-disk project cache (0600) for
  instant, STM-independent startup.
- `cmd/phc-bridge` — the runnable website bridge with resilient cache-backed
  startup and background project loading, plus chunk-scheduled reloads that yield
  to user commands between STM `readFile` calls.
- `cmd/stm-probe` — redacted transport, download, and parser diagnostics.
- `packaging/` — hardened `systemd` unit and example environment file.

## Build & test

```sh
cd bridge
go test ./...          # unit tests, no hardware needed
go vet ./...
go build ./...
```

## Run the website bridge

Choose one stable LAN URL and use that exact origin in both the command and the
browser. The values below are examples, not installation defaults:

```sh
go run ./cmd/phc-bridge \
  -stm 192.168.x.x:6680 \
  -listen 192.168.x.y:8080 \
  -origin http://phc-bridge.local:8080
```

All flags have `PHC_*` environment equivalents (see
[packaging/bridge.env.example](packaging/bridge.env.example) for the full set,
including `PHC_STATE_DIR`, `PHC_PROJECT_CACHE`, `PHC_IDLE_HEALTH_INTERVAL`, and
`PHC_LOG_LEVEL`). The bridge deliberately has no authentication: every device
able to reach the listen port on the trusted home LAN can inspect and control
the exposed PHC project. Restricting that port to the trusted subnet is a
deployment responsibility (see below).

**Resilient startup.** The bridge serves immediately — from the on-disk project
cache when one exists (`PHC_STATE_DIR`, default `/var/lib/phc-bridge`), otherwise
from an empty shell — and then loads a fresh project from the STM in the
background with bounded backoff. It never exits merely because the STM is
unreachable, so it is safe under `Restart=on-failure` and survives booting before
the STM. If the state directory is not writable it logs a warning and runs
without a cache rather than failing.

Website routes are `/`, `/floors/{floorID}`, `/settings`, and
`/acknowledgments`. The API lives below `/api/v1`; mutation requests require the
exact configured `Origin` and `Content-Type: application/json`.

## Probe a real STM

```sh
go run ./cmd/stm-probe -stm 192.168.x.x:6680 whoami
```

Prints a **redacted** identity, the round-trip time, and whether the known
malformed date line had to be removed. The synthetic transport fixture has been
reconciled with a direct raw capture from the real STM; the fixture body remains
synthetic so installation identity is never committed.

Download, extract, and parse a project while printing counts only:

```sh
go run ./cmd/stm-probe -stm 192.168.x.x:6680 project
```

The probe parses both `project.ppfx` and optional `project.tpfx`. It never prints
project, floor, category, or device names.

Exercise the controller's first-subscriber state sweep without changing any
outputs:

```sh
go run ./cmd/stm-probe -stm 192.168.x.x:6680 -timeout 45s -samples 10 state
```

The macOS development baseline against the real STM is 21 ms p50/p95 per direct
AMD read and 211 ms p50/p95 per scheduled ten-module sweep. Repeat the command
on the Raspberry Pi before setting deployment latency thresholds. Live command
latency still requires an explicitly selected harmless target; automated tests
verify every command byte without actuating the home.

## Deploy on the Raspberry Pi

Cross-compile a static `linux/arm64` binary on the Mac:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
  -o dist/phc-bridge-linux-arm64 ./cmd/phc-bridge
```

Install it and the packaging under [packaging/](packaging/):

```sh
sudo install -m 0755 dist/phc-bridge-linux-arm64 /usr/local/bin/phc-bridge
sudo useradd --system --no-create-home phc-bridge
sudo install -D -m 0640 packaging/bridge.env.example /etc/phc-bridge/bridge.env
sudo $EDITOR /etc/phc-bridge/bridge.env            # set your real STM/Pi addresses
sudo install -m 0644 packaging/phc-bridge.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now phc-bridge
```

The unit ([packaging/phc-bridge.service](packaging/phc-bridge.service)) runs as an
unprivileged user with a read-only filesystem except its `StateDirectory`
(`/var/lib/phc-bridge`, where the cache lives), and shares the Pi with CUPS
without conflict (the bridge is on 8080, CUPS on 631). **Restrict the listen port
to the trusted LAN with the Pi firewall** — that network boundary is the only
access control in this profile. It coexists with a CUPS print server on the same
Pi; keep the bridge off 631 and 80/443.
