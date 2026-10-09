# 7. Parametric OpenSCAD enclosure, inclined 45°

Date: 2026-10-09 · Status: Accepted

## Context

The display sits on a desk and should be readable from a seated position and reachable for touch. The vendor
publishes a dimensioned outline drawing (and a STEP model), so the case can be designed from real numbers.

## Decision

- OpenSCAD, text-based and diff-able; every board dimension comes from the vendor drawing and lives in one file,
  `enclosure/lib/e32r40t.scad`.
- A wedge with the screen face at 45° and a deep base, so that pressing the screen does not tip it.
- Two parts that print without supports: the body standing on its base (all overhangs are 45° or steeper) and the
  bezel face down. 4 × M3 screws into heat-set inserts clamp the board; the bezel never touches the resistive panel's
  active area.
- A small fit-test coupon to verify hole positions on the real board before the full print.
- STLs are committed so that people without OpenSCAD can print; CI re-renders them.

## Consequences

- Other boards or angles are parameter changes.
- Dimensions not in the drawing (exact USB-C height, button actuators) are covered by generous clearances until a
  test print confirms them.
