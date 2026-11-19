//go:build darwin

package droid

import (
	"os"
	"syscall"
	"time"
)

// birthTime is when the file was created, from the stat macOS keeps.
func birthTime(info os.FileInfo) time.Time {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}
	}
	return time.Unix(st.Birthtimespec.Sec, st.Birthtimespec.Nsec)
}
