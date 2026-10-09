package protocol

import (
	"slices"
	"strconv"
	"strings"
)

// MinFirmware is the oldest display firmware this helper fully supports. Raise
// it only when the helper starts to rely on something newer firmware does: an
// older display keeps working as far as it can, and the helper warns that it
// needs an update.
const MinFirmware = "0.1.0"

// CompareVersions compares two X.Y.Z versions and returns -1, 0 or +1. A
// leading "v" and any suffix after "-" or "+" (pre-release, build metadata,
// git describe) are ignored. A version that does not parse is older than any
// version that does.
func CompareVersions(a, b string) int {
	return slices.Compare(versionNumbers(a), versionNumbers(b))
}

// versionNumbers returns X, Y and Z, or nil (which sorts first) if version
// is not of that form.
func versionNumbers(version string) []int {
	version = strings.TrimPrefix(version, "v")
	if i := strings.IndexAny(version, "-+"); i >= 0 {
		version = version[:i]
	}
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return nil
	}
	numbers := make([]int, len(parts))
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil
		}
		numbers[i] = n
	}
	return numbers
}
