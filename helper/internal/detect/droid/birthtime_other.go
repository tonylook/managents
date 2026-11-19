//go:build !darwin

package droid

import (
	"os"
	"time"
)

// birthTime falls back to the last write: the other platforms keep no
// creation time Go can read, and the write dates a session's first line
// close enough.
func birthTime(info os.FileInfo) time.Time {
	return info.ModTime()
}
