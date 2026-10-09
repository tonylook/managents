# managents helper

The program that runs on your computer: it finds open AI agent sessions and streams their status to every connected
managents display. One static binary, no runtime, no configuration.

## Build and run

```bash
go build -o ../bin/managents ./cmd/managents     # or `make helper` from the repository root
../bin/managents                                  # same as `managents run`
```

| Command | What it does |
|---|---|
| `managents run [--port P] [--log-level L] [--claude-context N]` | Stream to displays until Ctrl-C. Without `--port`, USB serial ports are scanned every 2 s and each candidate is identified with a `hello` handshake. |
| `managents status [--json] [--claude-context N]` | Print the detected sessions, or the exact frame that would be sent. Needs no display. |
| `managents devices` | List USB serial ports and which of them are managents displays. |
| `managents demo [--port P] [--hold 6s]` | Cycle through demo screens: every layout, status and edge case. |
| `managents version` | Print the version. |

## What is detected

| Agent | Source | Status |
|---|---|---|
| Claude Code (terminal and desktop app) | `~/.claude/sessions/<pid>.json`, the live session registry. A record counts only if its pid is running **and** started when the record says (stale files from crashed sessions are ignored). | `busy` → working; a pending permission prompt or dialog → waiting; idle at the prompt → waiting, idle after 2 h; last transcript entry an API error → error. Context size from the last main-thread model call in the transcript; the limit is 200k, or 1M once a session goes past 200k (force it with `--claude-context`). |
| OpenCode | Running `opencode` processes (one card per working directory) and `~/.local/share/opencode/opencode.db`. | Unfinished assistant reply or a prompt sent < 90 s ago → working; a tool frozen at `running` for > 6 s → waiting (a permission prompt); assistant error → error; idle after 2 h. |

Cards are ordered by activity: working sessions first (in start order, so they don't swap places), then everything
else with the most recent status change first. Agents left waiting or idle for a long time sink to the later pages.
Names are the folder name, `parent/name` when two folders share a name, and `name #1`, `name #2` for several
sessions in the same folder.

**Privacy.** Only metadata is read: status fields, working directories, timestamps, token counts. The transcript
reader decodes nothing but those fields, and OpenCode's JSON is queried inside SQLite. Nothing is logged or sent
except what you see on the display.

## Start at login

| OS | How |
|---|---|
| macOS | [`init/com.github.tonylook.managents.plist`](init/com.github.tonylook.managents.plist) → `~/Library/LaunchAgents/`, then `launchctl load` it. |
| Linux | [`init/managents.service`](init/managents.service) → `~/.config/systemd/user/`, then `systemctl --user enable --now managents`. Your user must be allowed to open serial ports (group `dialout` or `uucp`). |
| Windows | Task Scheduler → "At log on" → `managents.exe run`. |

A `managents service install` command is planned ([roadmap](../docs/roadmap.md)).

## Layout

See [docs/architecture.md](../docs/architecture.md#helper). Tests: `go test -race ./...` — no agents, ports or
displays needed; the protocol tests check the shared fixtures in [`../protocol`](../protocol) against the schema.
