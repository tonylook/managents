// Package droid detects Factory droid sessions.
//
// Every running `droid` process is a session for its working directory, and
// the folder's active conversation is the newest session file Factory keeps
// for its project: <FACTORY_HOME_OVERRIDE or ~/.factory>/sessions/<project
// key>/<session id>.jsonl. droid records no status anywhere on disk, so the
// detector reads only the first line of the file (the session_start record
// with its id and working directory), the <session id>.settings.json sidecar
// (which hides archived sessions) and the file's timestamps — the messages,
// tool results and titles that follow the first line are never decoded. A
// recently written file is a turn in progress; a quiet one is a finished
// turn, and a permission prompt looks no different from one. The legacy flat
// sessions and the btw sessions are skipped.
package droid

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/detect"
	"github.com/tonylook/managents/helper/internal/process"
)

// workingFor is how long after a session file's last write the session still
// counts as working: a recently written file is the only tell of a turn in
// progress droid leaves on disk. After it, the session waits — a permission
// prompt is indistinguishable from a finished turn.
const workingFor = 90 * time.Second

// startBytes bounds how much of the start of a session file is read for its
// first line, the session_start record. Nothing past that line is parsed.
const startBytes = 64 << 10

// ProcessFinder lists running processes; process.System is the real one.
type ProcessFinder interface {
	// FindByName lists running processes whose executable name is exactly name.
	FindByName(ctx context.Context, name string) ([]process.Info, error)
}

// Source detects running droid sessions.
type Source struct {
	Processes ProcessFinder
	// SessionsDir is where Factory keeps its sessions: <factory home>/sessions.
	SessionsDir string
}

// NewSource returns a Source for the droid installation of the user whose
// home directory is home: $FACTORY_HOME_OVERRIDE when it is set, otherwise
// ~/.factory.
func NewSource(home string, processes ProcessFinder) *Source {
	root := os.Getenv("FACTORY_HOME_OVERRIDE")
	if root == "" {
		root = filepath.Join(home, ".factory")
	}
	return &Source{Processes: processes, SessionsDir: filepath.Join(root, "sessions")}
}

// Name implements detect.Source.
func (s *Source) Name() string { return string(agent.KindDroid) }

// Sessions implements detect.Source. A folder whose session files cannot be
// read is still shown, as waiting, and the errors are returned.
func (s *Source) Sessions(ctx context.Context, now time.Time) ([]agent.Session, error) {
	procs, err := s.Processes.FindByName(ctx, "droid")
	if err != nil {
		return nil, err
	}
	// The oldest process in a folder owns its card, as for Codex.
	slices.SortFunc(procs, func(a, b process.Info) int {
		return cmp.Or(a.StartedAt.Compare(b.StartedAt), cmp.Compare(a.PID, b.PID))
	})
	var sessions []agent.Session
	var errs []error
	seen := make(map[string]bool)
	for _, p := range procs {
		if p.Dir == "" || seen[p.Dir] {
			continue // one look per folder
		}
		seen[p.Dir] = true
		session, err := s.toSession(p, now)
		sessions = append(sessions, session)
		errs = append(errs, err)
	}
	return sessions, errors.Join(errs...)
}

// toSession builds the card of a folder: the newest session file Factory has
// for it, or a waiting card when droid has written none there yet.
func (s *Source) toSession(p process.Info, now time.Time) (agent.Session, error) {
	session := agent.Session{
		ID:        string(agent.KindDroid) + ":" + strconv.Itoa(p.PID),
		Kind:      agent.KindDroid,
		Dir:       p.Dir,
		Status:    agent.StatusWaiting,
		Since:     p.StartedAt,
		StartedAt: p.StartedAt,
	}
	dir := resolved(p.Dir)
	file, err := s.activeSession(dir, p.Dir)
	if err != nil {
		return session, err
	}
	if file.Path == "" {
		return session, nil // droid writes the session file with the first prompt
	}
	id := file.Start.ID
	if id == "" {
		id = strings.TrimSuffix(filepath.Base(file.Path), ".jsonl")
	}
	started := file.Born
	if started.IsZero() {
		started = file.Mod
	}
	session.ID = string(agent.KindDroid) + ":" + id
	session.StartedAt = started
	session.Status, session.Since = statusOf(file.Mod, p.StartedAt, now)
	return session, nil
}

// sessionFile is a session file and what its timestamps and first line say.
type sessionFile struct {
	Path  string
	Mod   time.Time // the last write: the status droid does not record comes from it
	Born  time.Time // when the file was created
	Start sessionStart
}

// activeSession returns the folder's active session file: the newest one in
// the project folder droid names after the working directory, whose first
// line carries that directory, and that is not archived. The folder is
// matched by name, not by a pattern: a project key keeps the punctuation of
// the path, which a glob would read as wildcards. The path is empty when
// there is none.
func (s *Source) activeSession(dir, processDir string) (sessionFile, error) {
	entries, err := os.ReadDir(filepath.Join(s.SessionsDir, projectKey(dir)))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return sessionFile{}, nil // droid has written no sessions for this folder
		}
		return sessionFile{}, err
	}
	var best sessionFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue // the settings sidecars are not sessions, nor are the btw ones
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(s.SessionsDir, projectKey(dir), entry.Name())
		start, ok, err := readSessionStart(path)
		if err != nil {
			return sessionFile{}, err
		}
		if !ok || (start.Cwd != dir && start.Cwd != processDir) {
			continue // another folder whose project key collides
		}
		archived, err := readArchived(strings.TrimSuffix(path, ".jsonl") + ".settings.json")
		if err != nil {
			return sessionFile{}, err
		}
		if archived {
			continue // an archived session keeps its data but is not open
		}
		if best.Path == "" || info.ModTime().After(best.Mod) {
			best = sessionFile{Path: path, Mod: info.ModTime(), Born: birthTime(info), Start: start}
		}
	}
	return best, nil
}

// projectKey is what droid names a project's session folder: a dash, then the
// working directory without its leading slash, every slash a dash. Unix paths
// map exactly as droid spells them. Windows spellings (backslash separators,
// a drive colon) fold to dashes too: the key has to be a legal directory name
// where the helper runs, and droid's own Windows spelling is unverified —
// Windows support is roadmap work.
func projectKey(dir string) string {
	if windowsPath(dir) {
		dir = strings.Map(func(r rune) rune {
			if r == '\\' || r == ':' {
				return '-'
			}
			return r
		}, dir)
	}
	return "-" + strings.ReplaceAll(strings.TrimPrefix(dir, "/"), "/", "-")
}

// windowsPath reports whether dir is spelled the Windows way: backslash
// separators or a leading drive prefix such as "C:".
func windowsPath(dir string) bool {
	if strings.ContainsRune(dir, '\\') {
		return true
	}
	if len(dir) < 2 || dir[1] != ':' {
		return false
	}
	drive := dir[0]
	return drive >= 'A' && drive <= 'Z' || drive >= 'a' && drive <= 'z'
}

// resolved is the working directory with symlinks resolved: droid derives the
// project key from the real path.
func resolved(dir string) string {
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		return real
	}
	return dir
}

// sessionStart is the first line of a session file, the record droid writes
// when it opens the session. Only these fields are read: the title and owner
// are not, and the transcript that follows the line — messages, tool results
// — is never decoded into memory we keep, logged or sent anywhere.
type sessionStart struct {
	Type string `json:"type"` // session_start
	ID   string `json:"id"`
	Cwd  string `json:"cwd"`
}

// readSessionStart reads the first line of a session file. ok is false when
// the file does not open with a session_start record, so it cannot be tied to
// a folder.
func readSessionStart(path string) (start sessionStart, ok bool, err error) {
	f, err := detect.OpenRegular(path)
	if err != nil {
		return sessionStart{}, false, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, startBytes))
	if err != nil {
		return sessionStart{}, false, err
	}
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		data = data[:i] // the transcript after the first line is never read
	}
	if json.Unmarshal(data, &start) != nil || start.Cwd == "" {
		return sessionStart{}, false, nil
	}
	return start, true, nil
}

// settings is the sidecar droid keeps next to a session file. Only the
// archive marker is read; the model and any other settings are not.
type settings struct {
	ArchivedAt json.RawMessage `json:"archivedAt"`
}

// readArchived reports the sidecar's archivedAt. Any value but an absent
// sidecar, null, zero, an empty string or false means the session is
// archived: hidden, its data kept.
func readArchived(path string) (bool, error) {
	data, err := detect.ReadFileBounded(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil // no sidecar: droid writes it with the first setting
	}
	if err != nil {
		return false, err
	}
	var s settings
	if err := json.Unmarshal(data, &s); err != nil {
		return false, err
	}
	switch value := strings.Trim(string(s.ArchivedAt), `" `); value {
	case "", "null", "0", "false":
		return false, nil
	default:
		return true, nil
	}
}

// statusOf maps a session file's last write to a status and when it began:
// droid records no turn state, so the last write is both the only tell of a
// turn in progress and the best guess at when the current status began.
// started is when the droid process started: a file last written before that
// belongs to a previous run, so it is not a turn in progress, and the session
// waits since it was opened.
func statusOf(written, started, now time.Time) (agent.Status, time.Time) {
	live := !written.Before(started)
	since := written
	if !live {
		since = started
	}
	switch {
	case live && now.Sub(written) < workingFor:
		return agent.StatusWorking, since
	case now.Sub(since) >= agent.DormantAfter:
		return agent.StatusIdle, since
	default:
		return agent.StatusWaiting, since
	}
}
