# managents firmware

Firmware for the LCDWiki 4.0" ESP32-32E display (E32R40T / E32N40T). It listens on the USB serial port, decodes
protocol v1 frames and draws one card per agent. No Wi-Fi, no Bluetooth, no settings.

## Build, test, flash

Requires [PlatformIO Core](https://docs.platformio.org/en/latest/core/installation/index.html).

```bash
pio test -e native                         # unit + contract tests of lib/core on this computer
pio run -e e32r40t                         # build
pio run -e e32r40t -t upload               # flash (add --upload-port /dev/cu.usbserial-XXXX if needed)
pio device monitor                         # see the hello message; type {"v":1,"t":"hello"} to probe
```

The USB-C port has auto-reset: no buttons needed to flash.

Build-time options (add to `build_flags` in `platformio.ini`):

| Flag | Default | Meaning |
|---|---|---|
| `MANAGENTS_ROTATION` | `1` | `1` = landscape with USB-C on the right (the enclosure's orientation), `3` = flipped, `0`/`2` = portrait. The layout adapts. |
| `MANAGENTS_BACKLIGHT` | `200` | Backlight level 0–255. |
| `MANAGENTS_BACKLIGHT_DIM` | `40` | Backlight level after a minute of "Reconnecting" or five minutes of no or only idle agents; a tap wakes it. |
| `MANAGENTS_LED_LEVEL` | `40` | RGB LED brightness 0–255. |
| `MANAGENTS_TOUCH` | `1` | `1`: a tap anywhere turns the page. `0` (E32N40T, no touch): pages turn by themselves. |
| `MANAGENTS_PAGE_INTERVAL_MS` | `10000` | Page interval without touch. |

## Structure

```
lib/core/            hardware-independent core (no Arduino) — all behaviour, unit-tested on the host
  application        bytes in → scene + LED + backlight out; link screens, timeouts, blink, dimming, redraw-on-change
  pager              nine cards per page, tap to turn, back to page one after 30 s; tap debouncing
  protocol           decode/validate host messages, encode hello
  scene              pure function: host state + time → everything the screen shows
  grid_layout        card grid for any count, screen size and orientation
  line_assembler     newline framing with oversize-line recovery
  format             ages, clock, percentages, UTF-8 → ASCII folding of names
  text_fit           fits a name into a box (font, line breaks, middle ellipsis) behind a text-measuring port
  ports              Display, StatusIndicator, HostLink interfaces
src/
  board/e32r40t.hpp  pin map and LovyanGFX device (ST7796S, XPT2046, backlight)
  ui/                ScenePainter (draws a Scene on any canvas), theme, vector logos, fonts
  adapters/          LgfxDisplay (strip renderer), RgbLedIndicator, SerialHostLink, TouchSensor
  main.cpp           composition root and loop
test/                Unity tests for lib/core, including the shared protocol fixtures
```

Design notes: [architecture](../docs/architecture.md#firmware), [strip rendering](../docs/adr/0006-strip-rendering.md),
[hardware findings](../docs/hardware.md#bring-up-findings-2026-10-09-on-a-real-e32r40t).
