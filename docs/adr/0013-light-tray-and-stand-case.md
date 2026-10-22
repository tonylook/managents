# 13. A light case beside the wedge: a flat tray and a stand

Date: 2026-10-22 · Status: Accepted

## Context

[ADR-0007](0007-parametric-45-degree-enclosure.md) made the case a one-piece wedge, printed standing on its base. It
is printed and works. In Cura (Ender 3 profile, 0.2 mm, 4 walls, 15 %) its body takes 72 g and 5 h 52 min, 43 % of it
for its first 4 mm, mostly a solid floor. The owner wants a cheaper and lighter option without replacing the wedge,
and with the board still covered on every side. Any closed case needs a plate about the size of the board behind it;
what costs time is how many tall layers it takes to print around it.

## Decision

- The wedge stays the case. Beside it, a **light case** replaces the body with two parts. A **tray**, a shallow box
  10.8 mm deep with a 1.2 mm back, holds the board and prints flat on its back. A **stand**, two side frames on four
  corner pads joined by a front and a rear bar, holds the tray at 45° and carries the rubber feet.
- The tray rests in the stand by its weight: its back on the frames, its lower edge on the front pads, and the
  frames' edges in two grooves in its back, which keep it from shifting. No screws or clips besides the four that
  clamp the board.
- Both cases share everything that holds the board: the 45° angle, the opening the PCB sits in, the corner supports
  with Ø2.5 pilot holes for 4 ST2.9 × 9.5 self-tapping screws, the USB-C opening and the bezel. They live in one
  OpenSCAD file, so a change to those parts reaches both.
- Every part prints without supports.

## Consequences

- The light body takes 33 g and 2 h 59 min in the same profile (tray 23 g and 1 h 58 min, stand 10 g and
  1 h 01 min). Its pilot holes print upright, the most accurate direction.
- Three parts to print instead of two, and four STLs to keep. Lifting the tray takes it out of the stand, so the
  light case is carried by the stand.
- The tray leaves no room behind the board for a speaker; the speaker mount stays a wedge feature.
- A first print with 0.2 mm of play and 1.2 mm deep grooves was hard to assemble. The play (`stand_fit`) is 0.4 mm on
  every side and the grooves are 2 mm deep with flared mouths; printed that way, the tray fits.
