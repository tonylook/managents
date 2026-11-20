# Changelog

All notable changes are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- **The status chime** (firmware and protocol, [ADR-0014](docs/adr/0014-sound-and-the-detail-view.md)). When a card
  starts waiting the display plays one short tone; when one hits an error, two — tellable apart without looking.
  Once per transition: an agent that stays waiting or failed is silent until something new happens, several agents
  changing in the same moment are one chime, and page flips, the idle decay and a dimmed screen (nobody is at the
  desk) make no sound. Off by default; `managents sound on|off` sends the new additive `settings` protocol frame,
  which the display stores in its NVS, so the choice survives reboots and the helper never repeats it. Older
  firmware ignores the frame. Needs a speaker in the enclosure's optional speaker mount.
- **`managents status --watch`.** The status table stays on screen and repaints itself every 2 s — each frame
  replacing the last one — until Ctrl-C, which exits cleanly. Output that is not a terminal (a pipe, a file) gets
  one report as before, and `--json` cannot be combined with `--watch`.
- **Qwen Code support.** Running `qwen` processes appear as cards, one per working directory: the folder's active
  conversation is the newest session file Qwen Code keeps under `~/.qwen/projects/<sanitized cwd>/chats/` (or
  `$QWEN_HOME`), read only at both ends — the head for the session id, start time and working directory, the tail
  for status and context. A prompt whose turn has not ended is working; an explicit `turn_result` record ends it:
  `completed` or `cancelled` waits, `error` shows the error, and both go idle after 2 h. Context size and limit
  come from the records' `usageMetadata` and `contextWindowSize`. Conversation content — the `message` field, the
  turn result texts, the title sidecars — is never decoded.
- **Gemini CLI support.** Running `gemini` processes appear as cards, one per working directory: the project comes
  from `~/.gemini/projects.json` (or the `.project_root` markers under `~/.gemini/tmp`), and the active conversation
  is the newest `session-*.jsonl` of its `chats/`, read only at both ends. Gemini CLI records no status, so the
  newest message record decides: a user prompt is working, a reply waits — unless its tool calls are still
  `executing` (working) or stuck at `awaiting_approval` (waiting, the closest a permission prompt gets) — and an
  `error` record shows the error; all of it goes idle after 2 h. Context size comes from the newest `tokens.total`
  (no limit: Gemini CLI stores none). Subagent sessions, nested under `chats/<parent session>/`, get no card of
  their own; a sandboxed run keeps its state in `~/.cache/gemini` and is not found. Conversation content — message
  content, display content, tool call arguments and results, thoughts, summaries — is never decoded.
- **pi support.** Running `pi` processes appear as cards, one per working directory: the active conversation is the
  newest session file of the folder pi names after the working directory under its sessions folder
  (`~/.pi/agent/sessions`, or `$PI_CODING_AGENT_DIR` / `$PI_CODING_AGENT_SESSION_DIR`), read only at both ends — the
  header for the session id, start time and working directory, the end for status and context. pi records no turn
  state, so the newest message entry decides: a user prompt or a tool result is working; an assistant message that
  stopped to call a tool with no tool result after it waits (the permission prompt); an assistant `error` stop
  shows the error, any other stop waits; all of it goes idle after 2 h. Context size comes from the newest
  assistant `usage.totalTokens` (no limit: pi stores none). Conversation content — message contents, compaction and
  branch summaries, tool results — is never decoded.
- **droid support.** Running `droid` processes appear as cards, one per working directory: the active conversation
  is the newest session file under `~/.factory/sessions/<project key>/` (or `$FACTORY_HOME_OVERRIDE`), of which
  only the first line (the `session_start` record), the `<session id>.settings.json` sidecar and the file's
  timestamps are read — droid records no status anywhere on disk, so the last write decides: written under 90 s ago,
  during this run, is working; quieter is waiting (a finished turn, a permission prompt and a write from a previous
  run look alike); untouched for 2 h is idle.
  Archived sessions (the sidecar's `archivedAt`) are hidden; the legacy flat and btw layouts are skipped; cards
  have no context bar, droid storing no token counts. The transcript after the first line is never decoded.
- **Crush support.** Running `crush` processes appear as cards, one per working directory: the project's data
  directory comes from the global `projects.json` registry (`$CRUSH_GLOBAL_DATA`, else `$XDG_DATA_HOME`, else
  `~/.local/share`), or from the nearest `.crush` directory at or above the working directory, and its `crush.db`
  is opened read-only for the active session — the newest one, without the title-generation and agent-tool
  sessions — and its newest message. Crush holds `crush.lock` (the pid of its process) over the data directory
  while it runs: a live lock and an unfinished newest message are working, from the prompt; a finished message
  waits; behind a stale lock — a run that crashed — nothing works. All of it goes idle after 2 h. Crush records
  no turn failure on disk, so an error cannot be told from a finished turn. Conversation content — message parts,
  file snapshots, todos, titles — is never decoded.
- **Goose support.** Running `goose` processes appear as cards, one per working directory: the folder's newest
  non-archived session is read from its `sessions.db` (macOS: `~/Library/Application Support/Block/goose`;
  elsewhere `$XDG_DATA_HOME/goose`, default `~/.local/share`; `$GOOSE_PATH_ROOT` moves the store to `<root>/data`),
  opened read-only and matched by the session's `working_dir` column. Goose records no status anywhere, so the
  newest message's role decides: a prompt without its reply is working, an assistant message waits, everything
  goes idle after 2 h; neither a turn failure nor a permission prompt is on disk. Context size comes from the
  session's `total_tokens` (no limit: Goose stores none). Conversation content — message contents, recipes,
  session names and descriptions — is never decoded.
- **Amp support (best effort).** Amp threads appear as cards, read from the local thread mirror
  (`$XDG_DATA_HOME/amp/threads`, default `~/.local/share`) — one JSON document per thread, rewritten in place,
  parsed with a filtered struct that reads only ids, creation times, message states, tool-run statuses, token
  counts and the thread's working directory. The statuses are explicit: a tool result `queued` or `in-progress`
  is working, `blocked-on-user` or `rejected-by-user` waits for as long as it takes, a failed run shows the
  error, a settled turn waits. A thread amp has not written to for 5 minutes is closed or abandoned and is not
  shown any more (nothing goes idle); a running thread is kept for 2 h of silence, a blocked one until answered.
  The mirror is undocumented and may be absent: threads it does not carry get no card, and so does a thread
  whose working directory it does not name (the key in its opaque `env` object is unconfirmed). Conversation
  content — text, thinking, tool calls and their results, titles — is never decoded.
- **Oh My Pi support (omp).** Running `omp` processes appear as cards, one per working directory. omp is
  pi-derived and writes the same session logs, so the same reader reads them at both ends — but the config
  tree is omp's own: the default agent dir and every named profile under it, each where omp keeps it
  (`~/.omp/agent/sessions`, `~/.omp/profiles/<name>/agent/sessions`, or their `$XDG_DATA_HOME/omp` locations
  once `omp config migrate` has moved them there, the `agent/` prefix flattened away), all scanned with the
  newest file across them deciding. The folder is named after the working directory relative to home or the
  OS temp root, pi's absolute encoding kept as a candidate for folders not yet migrated, and the file's
  header settles folders whose names collide. pi's status mapping carries over: a prompt or a tool result
  works, a tool call without its result waits (the permission prompt), an `error` stop shows the error,
  everything goes idle after 2 h; an entry written off the active branch does not decide. Context size comes
  from the newest assistant `usage.totalTokens` (no limit: omp stores none). Which profile a process runs
  under lives in its environment, which is not read, and sessions opened with `--session-dir` elsewhere are
  not found. Conversation content — message contents, title-slot titles, compaction and branch summaries,
  tool results — is never decoded.
- **Logos for the new agents.** Cards of the eight new kinds draw their own vector logo on the display
  instead of the generic mark — qwen a stylised Q, gemini a four-point spark, pi a lowercase pi, droid a
  robot head, crush a heart, goose a bird, amp a bolt, omp pi with a spark. Older firmware keeps the generic
  logo, so the protocol is unchanged. The mixed-four, showcase and twelve-plus-more fixtures now include the
  new kinds, and the golden screens are re-rendered.

## [0.5.0] - 2026-11-09

### Added

- **ZCode support.** Open ZCode sessions, from the terminal or the desktop app, appear as cards: one per open
  conversation, from the session processes (`zcode-cli`, or the desktop app's helpers, which it recognises by the
  process title the app rewrites to `zcode-cli`) and the conversations ZCode records in
  `~/.zcode/cli/db/db.sqlite`, read-only. A prompt whose turn has not ended is working; an ended turn waits, or
  shows the error it ended with. ZCode records no close, so a conversation it has not written to for five minutes
  stops being shown. Sessions ZCode runs for itself, under `~/.zcode`, get no card. ZCode stores no context-window
  size, so its cards have no context bar, and it records no pending approvals: a session blocked on one shows as
  working until it is answered.

## [0.4.2] - 2026-10-30

### Fixed

- **OpenCode: skip ACP agent processes.** `opencode acp` processes spawned by IDEs (e.g. JetBrains) are no longer shown as sessions; only real user sessions appear.

### Added

- **Enclosure: a light case beside the wedge.** A closed tray, 10.8 mm deep, holds the board and prints flat on its
  back; a stand of two side frames on four corner pads holds it at 45° and carries the rubber feet, the frames' edges
  in grooves in the tray's back. It uses the wedge's bezel, screws and feet, and the same board supports and pilot
  holes. Its body takes 33 g and 2 h 59 min instead of 72 g and 5 h 52 min (Cura estimate), the board stays covered
  on every side, and it tips at about 10 N. New STLs `managents-tray.stl` and `managents-stand.stl`; the wedge's
  STLs are unchanged ([ADR-0013](docs/adr/0013-light-tray-and-stand-case.md)). A first print with less play was hard to
  assemble; the version here is printed and fits.

## [0.4.1] - 2026-10-20

### Fixed

- **Closed sessions no longer keep a card.** The helper now counts a process whose exit path is underway (`P_WEXIT` on
  macOS, a zombie elsewhere) as gone; Claude Code can leave a session there for hours (the IntelliJ plugin does), with
  a pid and a start time that still match its registry record. Codex's IDE app server, launched as
  `codex -c ... app-server`, is no longer mistaken for a TUI session, and the non-interactive subcommands Codex 0.162
  has added since (review, doctor, queue, ...) no longer draw cards as they run, nor do `codex apply`,
  `codex --version` and `codex --help`.

## [0.4.0] - 2026-10-18

### Added

- **OpenCode: every open tab is its own card.** A TUI can keep several sessions open in tabs on one project; each one
  is now a card (`managents #1`, `managents #2`, ...) with its own status and age, instead of one card showing
  whichever tab wrote last. The tabs come from OpenCode's TUI state, read-only, session ids only: the tab titles are
  conversation text and are never decoded. Folders whose tabs are not known (OpenCode 1, the opt-in global tab scope,
  an unreadable state file) keep the single card they had.

## [0.3.0] - 2026-10-18

### Added

- **Codex CLI.** Every open `codex` session is a card with its status and context bar. Working and the end of a turn
  come from the thread's log, an error from a turn that failed, and an approval prompt from a tool call that waits
  with no command of the turn running (Codex does not record its prompts). `codex exec` runs, the background app server, and
  sessions of the IDE extension and the desktop app are not shown. Tested with the Codex 0.162 TUI and its 0.163 app server.
- **Codex logo.** Codex cards show the Codex mark, its blue-violet blob with the `>_` prompt, rasterized from
  the SVG by `firmware/scripts/codex_logo.py`. Earlier firmware shows them with the generic logo.

### Changed

- **Enclosure: the fit-test coupon is gone.** The printed case fits the board, so the coupon (`managents-fit_test.stl`,
  the `fit_test` part and its Makefile entry) is removed; the body and bezel are unchanged.
- **A short README, the rest in `docs/`.** The front page is one photo of the real display, what you need and three steps to get
  started, including how to tell your AI to do it. New pages: a build guide (what to buy, how to search for it, how to
  print and assemble the stand), install and flash, using the display and privacy.

## [0.2.0] - 2026-10-14

The first release: from a prototype you build from source to something you install and plug in.

### Added

- **One-line install.** `install.sh` (the release asset behind the `curl | sh` line in the README) installs the helper
  into `~/.local/bin`, checks its SHA-256, registers the service and can be run again to upgrade.
- **Background service.** `managents service install|uninstall|start|stop|status`: a LaunchAgent on macOS, a systemd
  user unit on Linux, started at login and restarted if it exits; logs to `~/Library/Logs/managents.log` on macOS and
  to the journal on Linux. Plugging in a display then just works: ports are scanned every 2 s.
- **Built-in flasher.** `managents flash` writes the firmware embedded in the helper over the ESP32's ROM bootloader,
  at 460800 baud with a fallback to 115200 and an MD5 check, stopping and restarting the service around the
  transfer. No PlatformIO or esptool needed.
- **Releases.** A `v*` tag builds the helper for macOS (universal), Linux (amd64, arm64) and Windows (amd64, not
  tested), the merged firmware image, `install.sh` and checksums; the version comes from the tag, for the helper and
  the firmware. `make dist`, `dist-check`, `check-version`, `release-notes`.
- **Firmware version check.** The helper warns, in its log and in `managents devices`, when a display's firmware is
  older than the one it needs, or when the display speaks a newer protocol; development builds count as unknown.
- **Command line.** `managents` alone prints help; commands have exit codes and `-h`; `run --log-file`; `devices`
  lists every USB serial port, shows firmware versions and tells a busy port from a board that is not a display.
- **Link screens.** Connecting, Setup needed (a QR code to the README's Get started section) and Reconnecting replace
  the single waiting screen.
- **Page dots coloured by attention**: they point at errors and waiting agents on other pages.
- **Backlight dimming** (`MANAGENTS_BACKLIGHT_DIM`) when nobody needs the screen: a minute of Reconnecting, or five
  minutes with no or only idle agents. A tap wakes it.
- **Host renderer** (`make screens`): the real UI painted into `docs/screens`, with a check that strip painting
  equals a full-frame paint; CI fails when the committed images are out of date ([ADR-0011](docs/adr/0011-host-renderer.md)).
- **Documentation.** A README for newcomers, `AGENTS.md` for AI coding agents, `docs/troubleshooting.md`,
  `SECURITY.md`, ADRs 0009 to 0012, third-party notices.
- **Enclosure v1.2.** No heat-set inserts: four ST2.9 × 9.5 self-tapping screws cut their own thread in Ø2.5 pilot
  holes in the corner supports (4.9 mm of thread, 1.5 mm of plastic below the hole; the supports are 1 mm taller).
  The bezel and the fit-test coupon are unchanged; only the body STL is regenerated.
- **Enclosure v1.1.** Bezel posts make the bezel one solid and clamp the board; the USB-C cut is centred on the
  receptacle and fits overmolds up to about 13 x 8.5 mm; an even 0.4 mm board clearance and a 0.5 mm glass gap;
  required rubber feet at the corners (tip threshold about 9.4 N instead of about 5 N); rounded corner supports with a
  0.4 mm insert lead-in; fit-test pins that reach the board, used face down; 1 mm chamfers; a speaker mount that is
  off by default; a warning when `face_angle` is not 45. Binary STLs (0.45 to 2.77 MB), `make enclosure-check` (each
  part is one solid) in CI, `make enclosure-images` for reproducible renders; `make enclosure` works on OpenSCAD 2021.01.
- CI: job timeouts, a weekly run, `govulncheck`, `shellcheck`, `go mod tidy` and sanitizer builds of the host tests;
  the `-Werror -Wshadow` build of `src/`.

### Changed

- **Frames no longer carry the working directory.** `path` is reserved and ignored; frames hold folder names,
  statuses, ages and context token counts only. Agent ids are limited to 48 bytes.
- Names are folded to ASCII (accents dropped, other characters shown as `?`), wrapped onto two lines, and shortened in
  the middle only as a last resort. Long ages read `1d 2h`. Card text is uniformly bold and the context bar is
  readable on every card colour.
- Error cards and the LED blink only during the first minute of an error.
- Detection: a Claude error counts only when it is the last main-thread message, and old errors turn idle; errors and waiting states turn idle after 2 hours; Claude's config directory follows
  `$CLAUDE_CONFIG_DIR` and OpenCode's database `$XDG_DATA_HOME`; the transcript reader scans the last 256 KiB, up to
  4 MiB when it finds no token usage there; macOS processes are listed with one `sysctl` (about 3.5 ms of CPU per poll instead of 25 ms); same-folder
  cards are numbered in start order; OpenCode turn ages count from the prompt.
- Discovery probes only USB serial ports behind ESP32 bridges (CH340, CP210x, FTDI, Espressif), retries a silent
  port 10 s and 70 s later and then leaves it alone, and never delays frames to connected displays.
- The display decoder is stricter: an empty `id` or a `ctx.limit` below 1 rejects the frame; integers above 32 bits
  saturate.
- The Go toolchain is pinned (`go1.26.9`); `go test -cover` works without the covdata error.
- `make flash` stops and restarts the service around the upload.

### Fixed

- A short write to a serial port with a full output buffer is retried instead of dropping part of a frame.
- A closed device manager stays closed.
- **OpenCode 2 is detected again.** OpenCode 2 writes its sessions to `session_v2` and `session_message` and no longer
  touches the `message` and `part` tables the helper read, so every card stayed idle. The helper now reads the new
  tables (turn end and outcome, interrupted and failed replies, compaction) and falls back to the old ones for
  OpenCode 1. Its background service (`opencode serve`) is no longer shown as a session in the home folder.
- OpenCode: errors left by a previous run are ignored; a reply of a dead run is not "working"; a store error no longer
  hides the sessions.
- Logos are drawn the same in every strip; auto-advance on boards without touch waits a full interval after a second
  page appears; a null string no longer reaches `memcpy`; the firmware reboots when its loop stalls (5 s watchdog).

### Removed

- The single "Waiting for computer" screen, `helper/init` (the hand-edited plist and unit templates the service
  replaces), `process.Table` and the `LGFX_USE_V1` defines. The firmware's hand-written version number: it comes from
  the tag now.

## [0.1.0] - 2026-10-09 (never tagged)

The minimum viable product. It was never tagged or published as a release; its contents ship with the first release.

### Added

- Firmware for the LCDWiki 4.0" ESP32-32E display (E32R40T): protocol v1, adaptive card grid with up to nine cards
  per page and a tap anywhere to turn the page, clock, overflow badge, context bar, blinking error cards,
  "waiting for computer" screen with QR code, RGB LED status summary, flicker-free strip rendering.
- Helper (`managents`): Claude Code and OpenCode detection, ordering by activity (working first, long-idle last),
  Claude context size and limit, serial auto-discovery with handshake, hot-plug and multiple displays; `run`,
  `status`, `devices`, `demo` commands.
- Protocol v1 JSON Schemas and shared fixtures tested by both the helper and the firmware.
- 45° desk enclosure in OpenSCAD with ready-to-print STLs and a fit-test coupon.
- Documentation: architecture, protocol, hardware notes with bring-up findings, ADRs, roadmap.
- CI: helper tests on Linux, macOS and Windows; lint; firmware host tests and build; enclosure renders.

[Unreleased]: https://github.com/tonylook/managents/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/tonylook/managents/compare/v0.4.2...v0.5.0
[0.4.2]: https://github.com/tonylook/managents/compare/v0.4.1...v0.4.2
[0.4.1]: https://github.com/tonylook/managents/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/tonylook/managents/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/tonylook/managents/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/tonylook/managents/commits/main
[0.1.0]: https://github.com/tonylook/managents/commit/b9edbd2
