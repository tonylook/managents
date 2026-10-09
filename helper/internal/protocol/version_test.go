package protocol

import (
	"os"
	"regexp"
	"testing"
)

// The firmware and the helper are released together under one version, so
// the "## [X.Y.Z]" headings of the changelog are the firmware versions that
// exist. The firmware started at firstFirmware, which therefore counts even
// while the changelog lists no release.
const (
	changelog     = "../../../CHANGELOG.md"
	firstFirmware = "0.1.0"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.1.0", 0},
		{"0.1.0", "0.2.0", -1},
		{"0.1.1", "0.1.0", 1},
		{"1.0.0", "0.99.99", 1},
		{"0.10.0", "0.9.0", 1}, // numbers, not text
		{"v0.2.0", "0.2.0", 0},
		{"0.2.0-3-gabc1234", "0.2.0", 0}, // git describe
		{"0.2.0-rc.1", "0.2.0", 0},
		{"0.2.0+dirty", "v0.2.0", 0},
		{"", "0.0.0", -1},
		{"dev", "0.1.0", -1},
		{"0.1", "0.1.0", -1},
		{"0.1.0.1", "0.1.0", -1},
		{"0.1.x", "0.1.0", -1},
		{"0.1.0", "unknown", 1},
		{"dev", "unknown", 0},
	}
	for _, tt := range tests {
		if got := CompareVersions(tt.a, tt.b); got != tt.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestMinFirmwareHasBeenReleased(t *testing.T) {
	text, err := os.ReadFile(changelog)
	if err != nil {
		t.Fatal(err)
	}
	newest := firstFirmware
	for _, heading := range regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\]`).FindAllSubmatch(text, -1) {
		if version := string(heading[1]); CompareVersions(version, newest) > 0 {
			newest = version
		}
	}

	if versionNumbers(MinFirmware) == nil {
		t.Fatalf("MinFirmware %q is not an X.Y.Z version", MinFirmware)
	}
	if CompareVersions(MinFirmware, newest) > 0 {
		t.Errorf("MinFirmware %s is newer than the newest firmware in CHANGELOG.md (%s): no display could meet it",
			MinFirmware, newest)
	}
}
