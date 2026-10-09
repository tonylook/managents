# managents enclosure

A desk wedge that holds the LCDWiki 4.0" ESP32-32E display (E32R40T) at **45°**, in landscape with the USB-C port
on the right. Two parts, no supports. Designed in OpenSCAD from the vendor's dimensioned drawing.

<p align="center">
  <img src="images/assembly.png" alt="Assembled enclosure showing a page of status cards (render)" width="560">
</p>

| Body | Bezel (print orientation) | USB-C side and back |
|---|---|---|
| <img src="images/body.png" width="260"> | <img src="images/bezel-print.png" width="260"> | <img src="images/assembly-side.png" width="260"> |

Overall size: **120 × 88 × 57 mm** (w × d × h) plus the 6 mm bezel.

## Print

| Part | File | Orientation | Notes |
|---|---|---|---|
| Fit test | [`stl/managents-fit_test.stl`](stl/managents-fit_test.stl) | flat | **Print this first** (≈10 min). Lay the board face down on it: the four pins must pass through the mounting holes (Ø2.9 pins in Ø3.2 holes check the positions to ±0.15 mm). Lift the board and look through the window: it must frame the picture. |
| Body | [`stl/managents-body.stl`](stl/managents-body.stl) | standing on its base, as exported | Nothing overhangs more than 45° from vertical except short bridges: the ceiling under the top flat (5.5 mm), the four feet recesses and the 2 mm grille holes. |
| Bezel | [`stl/managents-bezel.stl`](stl/managents-bezel.stl) | face down, as exported | The counterbore floors are short bridges. A textured PEI sheet gives a matte front. |

Suggested settings: PLA or PETG, 0.2 mm layers, 4 walls (the 2.4 mm walls then print solid), 15 % infill, no
supports.

**Filament.** The board's RGB LED is on the back of the PCB, facing into the case. In an opaque body you only see it
as a glow through the back grille onto whatever is behind the display. In white or natural (translucent) PETG some of
its light should come through the body; this is untested.

## Bill of materials

| Qty | Part |
|---|---|
| 1 | LCDWiki 4.0" ESP32-32E display, E32R40T (or E32N40T) |
| 4 | M3 × 10 socket or pan head screws |
| 4 | M3 heat-set inserts, Ø4 hole × 5.7 mm (or set `insert_d = 2.8` for self-tapping screws) |
| 4 | Self-adhesive rubber feet, Ø10 mm, at least 2 mm thick: **required**, without them a tap slides the case |
| 1 | USB-C data cable (see [Cable](#cable)) |

## Assemble

1. Prop the body so the face is level and press the four heat-set inserts into the corner supports with a soldering
   iron. Stop each one about 0.2 mm below the surface; the chamfer around the hole takes the plastic it pushes up.
2. Drop the board into the face, screen up, USB-C towards the side opening.
3. Put the bezel on and fasten the four screws. Tighten gently: the bezel's corner posts press on the PCB's mounting
   pads only, never on the glass, so the resistive touch panel stays free.
4. Stick the rubber feet into the recesses under the base.

Inside the closed case end up the **BOOT** and **RESET** buttons, which you never need (the USB bridge resets the
board and starts the download mode for flashing), the **microSD** slot (unused; leave it empty, it cannot be reached)
and the **RGB LED** (see Filament).

## Cable

The side opening is centred on the board's USB-C receptacle and fits plugs with overmolds up to about 13 × 8.5 mm,
which covers most cables. A right-angle plug keeps the cable flat on the desk.

## Stability

The feet sit at the corners of the base. By estimate (PLA, board included) a firm tap at the top of the screen tips
the case only above about 9 N, around ten times what a resistive touch needs; without the feet it slides at under 1 N.
Rendering the model echoes how far back the feet would have to reach for no tap to tip it.

## Customise

Everything is a parameter at the top of [`managents_case.scad`](managents_case.scad), also in OpenSCAD's Customizer:
`base_depth`, `lip_h`, wall thickness, clearances, screw and insert sizes, `usb_cutout`, `chamfer`, the grille and
the speaker mount. `face_angle` is one too, but only 45° prints without supports; any other angle prints a warning.
Board dimensions are in [`lib/e32r40t.scad`](lib/e32r40t.scad), taken from the vendor drawing (see
[docs/hardware.md](../docs/hardware.md#mechanical-data)).

```bash
openscad managents_case.scad                          # interactive, part = "assembly"
openscad -D 'part="section"' managents_case.scad      # assembly cut in half: check clearances
make enclosure                                        # re-render all STLs (from the repo root)
make enclosure-check                                  # check that each printed part is one solid, as CI does
make enclosure-images                                 # re-render the images above
```

`make enclosure` and `make enclosure-check` work with the stable OpenSCAD (2021.01) and with snapshots, where they
use the faster Manifold backend. `make enclosure-images` needs a snapshot: 2021.01 lacks its colour scheme.

`speaker_mount` adds a ring for a 28 mm speaker behind the grille, for the roadmap's sound feature. It is off: its
underside needs supports, and the plug in the board's side-entry SPEAKER socket will need a relief in the face shell.

## Status

v1.1 is designed from the drawing and checked in renders and with geometry checks (one solid per part, overhangs,
plug and board clearances), not yet printed. Measured corrections go into `lib/e32r40t.scad`; planned improvements
are in the [roadmap](../docs/roadmap.md).
