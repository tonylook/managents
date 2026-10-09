# Contributing

Thanks for helping! Issues and pull requests are welcome. For anything bigger than a fix, open an issue first so we
can agree on the approach.

## Setup

- Go ≥ 1.26 (an older `go` downloads the right toolchain by itself)
- PlatformIO Core (`pipx install platformio`)
- Optional: OpenSCAD (enclosure), clang-format 23 (`pipx install clang-format`)

```bash
make test     # everything that runs without hardware
make lint     # gofmt, go vet, staticcheck, clang-format
```

## Ground rules

- **The protocol is the contract.** Change `docs/protocol.md`, `protocol/schema` and `protocol/fixtures` in the same
  pull request, keep it backwards compatible, and let both test suites prove it.
- **Behaviour goes in the core.** Firmware logic belongs in `firmware/lib/core` (no Arduino headers) with a native
  test; helper logic belongs in `internal/` packages behind interfaces, tested with fakes. Adapters stay thin.
- **No conversation content.** Detectors read session metadata only. Never log, store or send prompt or reply text.
- **Small, focused commits** with messages that say what and why. Conventional prefixes are welcome
  (`feat(helper): …`, `fix(firmware): …`, `docs: …`).
- **Record decisions.** If a change makes a design choice others will wonder about, add an ADR in `docs/adr/`.

## Adding an agent

1. Create `helper/internal/detect/<agent>/` implementing `detect.Source`.
2. Depend on `process.Table` (and a small store interface if the agent persists state), so tests can use fakes.
3. Map the agent's states onto `working` / `waiting` / `error` / `idle` and document the mapping in
   `helper/README.md`.
4. Register it in `defaultSources()` in `cmd/managents/commands.go`.
5. If it needs a new logo, add a `kind` and a vector logo in `firmware/src/ui/logos.cpp`; older firmware shows a
   generic logo, so this is not a breaking change.

## Hardware changes

Board pin maps live in `firmware/src/board/`; enclosure dimensions in `enclosure/lib/`. Note the source of every
dimension (drawing, datasheet, caliper) in a comment.
