package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOpenLogFileRotatesPastTheLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managents.log")
	write := func(content string) {
		t.Helper()
		file, err := openLogFile(path, 10)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString(content); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	write("first\n")  // 6 bytes: created
	write("second\n") // 13 bytes after this: appended, still under the limit at open
	if got := read(path); got != "first\nsecond\n" {
		t.Errorf("log = %q, want both runs appended", got)
	}
	write("third\n") // over the limit at open: rotated first
	if got, old := read(path), read(path+".1"); got != "third\n" || old != "first\nsecond\n" {
		t.Errorf("log = %q, previous = %q; want the third run alone, the first two in .1", got, old)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("log permissions %v, want 0600", perm)
		}
	}
}
