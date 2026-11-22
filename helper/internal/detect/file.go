package detect

import (
	"errors"
	"io"
	"io/fs"
	"os"
)

// MaxFileBytes bounds any one file a detector reads whole. The side files a
// poll reads — session records, project indexes, tab state, lock files — are
// kilobytes, and anything past this cap is hostile or corrupt: os.ReadFile
// allocates whatever the file's size says, so a multi-gigabyte file planted
// at a watched path would take the helper's memory with it. An over-cap file
// is an error, exactly like an unreadable one. The same order as amp's thread
// mirrors, the largest legitimate file any detector reads.
const MaxFileBytes int64 = 4 << 20

// Errors that refuse a file before any of it is read.
var (
	errNotRegular = errors.New("not a regular file")
	errTooLarge   = errors.New("larger than the read cap")
)

// OpenRegular opens the regular file at path for reading. Anything else is
// refused before it is opened — a named pipe planted at a watched path would
// block the open until a writer appears, and a device would stream forever —
// and the opened file is checked too, so a path swapped for a special file in
// between does not slip through. Every file a detector opens goes through
// here, through ReadFileBounded, or through ReadTail.
func OpenRegular(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, &fs.PathError{Op: "open", Path: path, Err: errNotRegular}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err = f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, &fs.PathError{Op: "open", Path: path, Err: errNotRegular}
	}
	return f, nil
}

// ReadFileBounded reads the regular file at path whole, but never more than
// MaxFileBytes of it. Callers that ignored an unreadable file keep ignoring
// this error: a side file that is hostile in size is as good as absent.
func ReadFileBounded(path string) ([]byte, error) {
	f, err := OpenRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxFileBytes {
		return nil, &fs.PathError{Op: "read", Path: path, Err: errTooLarge}
	}
	// The file may grow between the Stat and the read; the cap holds anyway.
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxFileBytes {
		return nil, &fs.PathError{Op: "read", Path: path, Err: errTooLarge}
	}
	return data, nil
}

// RegularFile reports whether the file at path is a regular file. The
// database-backed detectors check this before handing a path to the sqlite
// driver, which opens the file lazily at its first query and would block
// forever on a named pipe.
func RegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
