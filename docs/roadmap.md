# Roadmap

Each milestone is shippable on its own. Items are written so they can become GitHub issues as they are. Protocol
changes stay backwards compatible ([protocol.md § Versioning](protocol.md#versioning)).

## v0.1 — MVP ✅

- [x] Firmware: protocol v1 decoder, adaptive card grid (up to 9 cards per page, any orientation), tap anywhere for
      the next page, clock, `+N` badge, context bar, blinking error cards, "waiting for computer" screen with QR
      code, RGB LED summary.
- [x] Helper: Claude Code and OpenCode detection (ported from the agent-lights POC), activity ordering (what you
      work with on page one), Claude context size and limit, hot-plug discovery with `hello` handshake, multiple
      displays, `run` / `status` / `devices` / `demo`.
- [x] Shared contract: JSON Schemas and fixtures tested on both sides.
- [x] Enclosure v1: 45° wedge, support-free parts, fit-test coupon.
- [x] CI: tests, lint, firmware build, STL render.

## v0.2 — Everyday use and distribution

Goal: someone else can install it in five minutes.

- [ ] `managents service install|uninstall`: start at login with launchd (macOS), a systemd user unit (Linux) and a
      scheduled task (Windows). Templates already in [`helper/init/`](../helper/init).
- [ ] Release workflow on tags: helper binaries for macOS (arm64, amd64), Linux (amd64, arm64) and Windows (amd64)
      with checksums; firmware `.bin` (merged image) attached to the release.
- [ ] Web flasher on GitHub Pages ([ESP Web Tools](https://esphome.github.io/esp-web-tools/)): flash from Chrome
      without PlatformIO. The QR code on the "waiting" screen points to the setup guide.
- [ ] Homebrew tap (`brew install tonylook/tap/managents`).
- [ ] Windows: verify process detection, COM port discovery and the CH340 driver story end to end.
- [ ] Firmware version check: the helper warns when the display's `fw` is older than the one it was released with.

## v0.3 — Touch

Goal: the resistive panel becomes useful without making it necessary. Paging by tapping anywhere already works in
v0.1 (it needs no position, so no calibration); everything below needs to know *where* the tap was.

Firmware design (keeps the core testable):

- `TouchInput` port; the adapter reads the XPT2046 through LovyanGFX (`getTouchRaw`) and maps raw values with the
  stored calibration.
- `GestureRecognizer` in `lib/core`: debounces raw samples into tap, long-press and swipe. Pure, unit-tested with
  recorded sample sequences.
- `Navigator` state machine in `lib/core`: `Grid → Detail(id) → Grid`, `Grid → Settings`, `Calibration`. The scene
  builder renders the current screen; hit-testing uses the card rectangles already in `Scene`.

Features:

- [ ] Calibration: 4-point target screen on first boot (or on `managents calibrate`), stored in NVS. Pressure
      threshold to reject light touches through the bezel.
- [ ] Tap a card → detail view: full path, status and since when, context bar with numbers, session kind.
      Auto-returns to the grid after 15 s.
- [ ] Long-press → settings: brightness, LED on/off, sound on/off (persisted in NVS).
- [ ] Protocol 1.1, device → host: `{"v":1,"t":"event","event":"select","id":"claude:48211"}`. The helper logs it in
      v0.3; acting on it (bringing the session's terminal or app to the front) is a stretch goal, per OS.
- [ ] E32N40T (no touch) keeps working: touch features compile out with a board flag.

## v0.4 — Sound and ambient

- [ ] Short chime when an agent changes to `waiting` or `error` (DAC on GPIO 26, amplifier enabled on GPIO 4 only while
      playing). Tones synthesised on the device, no audio files. Rate-limited, muted at night.
- [ ] The enclosure already has a grille and a ring for a 28 mm speaker on the back.
- [ ] LED patterns per state (breathing while working, pulse while waiting) and an off switch.
- [ ] Auto-dim the backlight when every agent is idle; night hours derived from the host clock.
- [ ] Protocol 1.1, host → device: `{"v":1,"t":"config","brightness":180,"sound":true,"led":true}` so
      `managents config` can set preferences from the computer.

## v0.5 — More agents, richer status

- [ ] New `detect.Source` implementations: OpenAI Codex CLI, Gemini CLI, Aider, Cursor's agent CLI (one package each,
      same tests-with-fakes pattern).
- [ ] Session names: use Claude Code's user-set session name when present (the registry has `name`), folder name
      otherwise.
- [ ] Context limit per model (e.g. 200k vs 1M windows) so Claude cards show a bar instead of a number.
- [ ] Subagent activity: a small badge when a session runs background agents.
- [ ] `managents status --watch` for a terminal view of the same data.

## v0.6 — Developer experience

- [ ] Desktop simulator: LovyanGFX's SDL backend runs the same `ScenePainter` on macOS/Linux and reads protocol
      lines from stdin. Feed it `managents status --json` or the fixtures.
- [ ] Golden-image tests: render every fixture in CI and compare PNGs, so UI changes show up in review.
- [ ] Fuzz the firmware decoder (libFuzzer on the native build) and the helper's transcript reader (`go test -fuzz`).

## Enclosure v2

After printing v1 and the fit test:

- [ ] Fold measured corrections back into `lib/e32r40t.scad` (USB-C height, button positions, real hole spacing).
- [ ] Snap-fit bezel as an option to the screws.
- [ ] Cable strain relief / channel for the USB-C cable.
- [ ] Optional Li-Po bay and switch (the board has a charger and a battery connector) for a cable-free desk.
- [ ] Angle presets (30°, 45°, 60°) and a portrait variant for the TN panel's best viewing direction.
- [ ] Print profile notes from real prints (material, layer height, insert temperature).

## Hardware beyond the E32R40T

- [ ] Board abstraction: one directory per board under `firmware/src/board/`, one PlatformIO environment each,
      selected at build time.
- [ ] Freenove ESP32-S3 5" (800 × 480, capacitive, PSRAM): the original target of the agent-lights plan; with PSRAM
      the strip renderer can switch to a full frame buffer.
- [ ] ESP32-2432S028 ("Cheap Yellow Display", 2.8") as a low-cost option.
