# 14. Sound and the detail view

Date: 2026-11-20 · Status: Accepted · Amends the touch part of [0009](0009-display-ui-rules.md)

## Context

The roadmap's "Sound" and "Touch beyond paging" items both reach past the display's passive rules. A chime should
mark the moment an agent needs attention — and nothing else, or it becomes noise. The detail view needs to know
where the finger pressed, which the display has never needed: the resistive panel varies per unit, so raw touch
coordinates need calibration — and [ADR-0009](0009-display-ui-rules.md) made "a tap anywhere shows the next page"
the one interaction users know, with no hit-testing and no calibration at all.

The display has no configuration of its own ([ADR-0002](0002-dumb-display-smart-helper.md)), and the chime only
makes sense with the optional speaker mounted behind the enclosure's back grille.

## Decision

**The chime.** When a card *enters* `waiting` or `error` — a change between two consecutive `state` frames, matched
by the agent's id — the display plays a short chime: one tone for waiting, two for error, so they are tellable apart
by ear. Everything else is silent: a card seen for the first time, one that leaves the screen, one that keeps its
status, one that decays to idle, page flips, link screens. Several cards changing in the same frame are one chime;
an error outranks a waiting agent. The first frame after boot or a reconnect never chimes by itself.

A dimmed screen suppresses the chime. The dim means nobody needs the screen ([ADR-0009](0009-display-ui-rules.md)),
and after five quiet minutes nobody is at the desk to hear it; the screen still wakes when the attention changes, so
the state is there when its person returns. Chosen over "the chime still plays" because a chime heard by an empty
room spends the one thing it has — meaning that something now needs you *now*.

The chime is **off by default** and is enabled from the computer: a new additive `settings` frame
(`{"v":1,"t":"settings","sound":true|false}`, still protocol v1), sent by `managents sound on|off`. The display
stores it in NVS and keeps it across reboots, so the helper sends it only when the choice changes — never at the
handshake, never on a schedule. Older firmware ignores the frame like any unknown message type. The sound lives
behind a `SoundOutput` port (the board's DAC and its amp-enable pin, free before the optional speaker mount holds a
speaker; driving nothing is harmless).

**The detail view.** A **long press** — a touch held for **600 ms** (`kLongPressMs`) — on a card opens that card's
detail view: the agent's logo, its folder name as on the card, the status and its age, the clock time the status
started, and the context bar when the helper knows the limit. Any later tap or long press dismisses it; a plain
short tap is unchanged. Because the same press can no longer page the instant it lands, a tap is recognised when
the finger lifts within the threshold: the rule "a tap anywhere shows the next page" keeps its meaning, its timing
moves by the width of a finger lift.

Hit-testing is against the card rectangles the scene was laid out with (`cardAt`): only cards answer a long press.
The header, the page dots, the `+N` badge, the gaps and the margins are not cards — a long press there does
nothing, and never pages. A card is matched at the moment the long press fires, and the view follows the agent by
id afterwards, so a reordering frame cannot move the target under a held finger. A detail view closes by itself
when its agent leaves the frame or the link drops.

**Calibration, lazy.** The raw panel coordinates are mapped by a two-point calibration: two touches on shown
targets (near opposite corners) give a scale and an offset per axis, enough to hit a card of ten. Axis pairing is
decided by which raw axis moved the way the targets did, so the flow absorbs the panel's orientation, mirror and
per-unit drift alike. It is stored in NVS with the screen size it was taken in; another layout or a corrupted store
counts as uncalibrated.

Nothing calibrates at boot. The flow starts only when a long press lands while the display is uncalibrated: a
screen that says *touch the circle* (then *and this one*). A tap is a sample; a hold past the long-press threshold
leaves the flow, and a hold-and-a-half (1500 ms) starts it again anywhere, calibrated or not — no accuracy needed
to escape or redo, so the flow is always skippable and never a dead end. Out of the box the display is fully usable
with no calibration at all: taps page, and the first long press simply offers the calibration. If the two samples
are too close together to trust, the result is discarded and the display stays uncalibrated.

## Rejected alternatives

- **A settings screen on the display, or the chime on by default.** ADR-0002 keeps configuration on the computer;
  a speakerless display chiming by default would be noise out of the box.
- **Re-sending settings at every handshake.** The display persists them; a re-send would need the helper to know
  what the display last chose, and would break the moment two computers share a display.
- **LovyanGFX's `calibrateTouch`.** It draws its own four-corner UI, blocks until every corner is touched, and
  cannot be skipped or themed; two custom targets in the display's own scene system keep the flow short and
  testable on the host.
- **A single calibration target** (offset only). It corrects where the panel's zero sits but not its scale, and
  the far corner — where the `+N` badge and the page dots live — drifts past a card's edge.
- **Firing the long press on release.** Holding 600 ms and then waiting for the lift feels laggy; the view opening
  under the finger is the feedback that the hold was long enough.
- **New protocol fields for the session start time.** The detail view shows when the *status* started, derived
  from `age` and the host clock — the protocol already carries all of it, and the status is what the card is
  about.

## Consequences

- `docs/protocol.md` documents `settings`; the schema, the fixtures and both implementations carry it, and
  `managents sound` stops the service for its one frame, as `managents flash` does for the image.
- Taps page on release rather than on landing — imperceptible at the 120 ms debounce, and the only way one press
  can mean either page or detail.
- The detail view is a scene kind, painted by the same strip-safe painter, rendered by the host renderer like every
  other screen, and tested natively: the touch state machine, the hit tests, the calibration flow and its skip, and
  the out-of-the-box behaviour. The context bar of the detail view follows rule 4: a bar, never numbers.
- The chime blocks the loop for about a quarter second, once per transition — well under the task watchdog, and
  frames poll at a 2 s cadence. Its loudness is a first guess until a speaker is in a mount to listen to.
- A display whose owner never long-presses never calibrates, and nothing reminds them to: the detail view is a
  discoverable extra, not a step of setup.
