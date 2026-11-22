package detect

import (
	"os"
	"path/filepath"
	"testing"
)

// A special file planted at a watched path is refused before it is opened: a
// named pipe would block the open until a writer appears, and a directory has
// no content. A directory stands in for the pipe, which cannot be made
// portably and must never be opened in a test anyway.
func TestOpenRegularRefusesNonRegularFiles(t *testing.T) {
	if _, err := OpenRegular(t.TempDir()); err == nil {
		t.Error("err = nil, want a directory to be refused")
	}
	if RegularFile(t.TempDir()) {
		t.Error("RegularFile of a directory = true, want false")
	}

	path := filepath.Join(t.TempDir(), "record.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := OpenRegular(path)
	if err != nil {
		t.Fatalf("err = %v, want a regular file to open", err)
	}
	f.Close()
	if !RegularFile(path) {
		t.Error("RegularFile of a regular file = false, want true")
	}
}

func TestReadFileBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tabs.json")
	if err := os.WriteFile(path, []byte(`{"cwd":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := ReadFileBounded(path)
	if err != nil || string(data) != `{"cwd":{}}` {
		t.Errorf("ReadFileBounded() = %q, %v; want the file whole", data, err)
	}
	if _, err := ReadFileBounded(t.TempDir()); err == nil {
		t.Error("err = nil, want a directory to be refused")
	}
}

// os.ReadFile allocates whatever the file's size says, so a hostile
// multi-gigabyte file at a watched path must be refused, not read: a file
// past the cap is exactly like an unreadable one.
func TestReadFileBoundedRefusesAnOverCapFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge.json")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if _, err := ReadFileBounded(path); err == nil {
		t.Error("err = nil, want a file past the cap to be refused")
	}
}
