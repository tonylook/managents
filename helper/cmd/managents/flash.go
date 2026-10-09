package main

import "github.com/tonylook/managents/helper/internal/firmware"

// board is the only display board this helper has firmware for.
const board = "e32r40t"

// embeddedFirmware returns the firmware image built into this helper and its
// version. Both are empty if the helper was built without one.
func embeddedFirmware() (image []byte, version string) {
	image, version, _ = firmware.Embedded(board)
	return image, version
}
