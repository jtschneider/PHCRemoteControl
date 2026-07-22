# Shared protocol fixtures

These synthetic fixtures are consumed by both the Swift and Go test suites.
They define normalized parser behaviour without containing any real household
project, address, device name, capture, or facility identity.

`project/*.ppfx` contains synthetic PHC project XML. A matching
`*.expected.json` file contains the language-neutral normalized result. A fixture
may also include a same-basename `*.tpfx`; both parsers must then consume it.
`commands/*.json` contains current input-event sequences shared by the native
and bridge command planners. Hardware verification status remains documented in
the protocol notes; the jalousie `tip` sequence is still experimental.

Stable IDs use this versioned contract:

```text
device:v1:<module-class>:<dip>:<channel>
floor:v1:<unpadded-base64url-UTF8-floor-name>
```

Parsed devices always have a primary hardware reference. The primary reference
is the identity even when a motor also has a secondary raise channel.
