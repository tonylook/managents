# Serial protocol, version 1

The contract between the **helper** (computer) and the **display** (firmware). Machine-readable versions:
[`protocol/schema/`](../protocol/schema). Shared test cases: [`protocol/fixtures/`](../protocol/fixtures) — the helper
validates them against the schema and the firmware decodes them, so both sides are tested against the same files.

## Transport

- USB serial, **115200 8N1**. The helper keeps **DTR and RTS low**: on ESP32 boards they drive the auto-reset circuit.
- UTF-8 text, **one JSON object per line**, terminated by `\n` (a preceding `\r` is ignored).
- Maximum line length **4096 bytes**. Receivers discard longer lines and resynchronise at the next `\n`.
- Every message has `"v"` (protocol version) and `"t"` (type). Receivers ignore unknown types, unknown fields, and
  messages with another `v`.
- Anything that is not a valid message (boot log, noise, partial lines) is ignored silently.

## Host → device

### `state`

Sent every 2 seconds, and at the next poll (≤ 1 s) when anything visible changes.

```json
{
  "v": 1,
  "t": "state",
  "now": 1791560000,
  "tz": 7200,
  "agents": [
    {
      "id": "claude:48211",
      "kind": "claude",
      "name": "managents",
      "path": "/Users/ann/managents",
      "status": "working",
      "age": 42,
      "ctx": { "used": 88000, "limit": 200000 }
    }
  ],
  "more": 0
}
```

| Field | Type | Meaning |
|---|---|---|
| `now` | int | Host UNIX time in seconds. The display shows the clock from `now + tz` and advances it locally. |
| `tz` | int, optional | Host UTC offset in seconds, DST applied. Default 0. |
| `agents` | array, ≤ 24 | **Already filtered, named and ordered** by activity: working sessions first, then the most recent status change first. The display never sorts, renames or derives status; it shows them nine per page. |
| `more` | int, optional | Agents left out of `agents`. Shown as a `+N` badge. Default 0. |

Agent fields:

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Stable for the life of the session: `<kind>:<pid>`. |
| `kind` | string | `claude` or `opencode`. Other values get a generic logo. |
| `name` | string, ≤ 64 bytes | Display name, already unique (`name`, `parent/name`, `name #2`). The display shortens it with an ellipsis to fit. |
| `path` | string, optional | Working directory, for detail views. Dropped by the helper if the line would exceed 4096 bytes. |
| `status` | enum | `working`, `waiting`, `error`, `idle` (see below). |
| `age` | int ≥ 0 | Seconds in the current status. The display advances it locally and formats it (`42s`, `5m`, `1h 12m`). |
| `ctx` | object or null, optional | Context-window usage: `used` tokens and `limit` (null when unknown). The card shows a fill bar along its bottom edge when the limit is known, and nothing otherwise. |

Status meanings:

| Status | Meaning | Card |
|---|---|---|
| `working` | the agent is running a turn | green |
| `waiting` | blocked on the user, or finished and waiting for the next prompt | yellow |
| `error` | the last turn ended in an API/model error | red, blinking ~1 Hz |
| `idle` | open but no activity for 2 hours | dark, dimmed |

A frame is validated as a whole: if any part is invalid (wrong type, unknown status, negative age…) the display
ignores the entire frame and keeps the previous one.

### `hello`

```json
{ "v": 1, "t": "hello" }
```

A probe. The display answers with its own `hello`.

## Device → host

### `hello`

```json
{ "v": 1, "t": "hello", "device": "managents", "fw": "0.1.0", "board": "e32r40t", "w": 480, "h": 320, "proto": 1 }
```

Sent at boot and in reply to a probe. `w`/`h` are the screen size in the current rotation. The helper treats a port
as a display only after receiving this message (within 3 s, probing once per second).

## Behaviour

| Situation | Display | Helper |
|---|---|---|
| Normal operation | Draws the agents received, in order, nine per page. A tap anywhere shows the next page; after 30 s without a tap it returns to the first. | Sends `state` every 2 s and on change. |
| No agents | Shows "No agents open" and the clock. | Sends `state` with `"agents": []` — never silence. |
| No valid frame for 6 s | Shows "Waiting for computer…" with a QR code to the project page. | — |
| Display unplugged | — | Keeps running; rediscovers ports every 2 s. |
| Several displays | — | Sends to all of them. |
| A detector fails | — | Skips that source, logs once, keeps sending the rest. |

## Versioning

- Backwards-compatible additions (optional fields, new message types) keep `v: 1` and are documented here.
- A breaking change bumps `v`. The helper must keep speaking the version a display announces in `proto`.

Planned additions for touch and sound are listed in the [roadmap](roadmap.md).
