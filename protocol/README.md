# Protocol contract

The machine-readable half of [docs/protocol.md](../docs/protocol.md).

| Path | Content | Checked by |
|---|---|---|
| `schema/host-message.schema.json` | JSON Schema of every helper → display line | helper tests |
| `schema/device-message.schema.json` | JSON Schema of the display's `hello` | helper tests |
| `fixtures/valid/*.jsonl` | Lines the display must accept: every status, empty, 12 + `more`, long and non-ASCII names, unknown kinds and fields | helper (schema) and firmware (decoder) |
| `fixtures/invalid/*.jsonl` | Lines the display must ignore: broken JSON, missing fields, wrong types, unknown status, negative age | helper (schema) and firmware (decoder) |

Adding a case here tests both implementations at once. Change the schema and the fixtures together with
`docs/protocol.md`.
