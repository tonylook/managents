package detect

import "io"

// ReadTail returns at most the last size bytes of the file at path, and
// whether that is the whole file. Detectors read only the end of a session
// log, however long the session. Only a regular file is read, and no more
// than size bytes of it even when the file grows while it is being read.
func ReadTail(path string, size int64) ([]byte, bool, error) {
	f, err := OpenRegular(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	offset := max(info.Size()-size, 0)
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, false, err
	}
	data, err := io.ReadAll(io.LimitReader(f, size))
	return data, offset == 0, err
}
