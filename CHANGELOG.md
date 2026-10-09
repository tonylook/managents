# Changelog

All notable changes are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.1.0] - 2026-10-09

First release: the minimum viable product.

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

[Unreleased]: https://github.com/tonylook/managents/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/tonylook/managents/releases/tag/v0.1.0
