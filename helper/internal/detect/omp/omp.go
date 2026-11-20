// Package omp detects Oh My Pi sessions.
//
// Oh My Pi (omp) is a coding agent derived from pi: it writes pi-format
// session logs, read here through the shared reader (detect/pijsonl), in a
// config tree of its own. Every running `omp` process is a session for its
// working directory, and the folder's active conversation is the newest
// session file the installation keeps for it. The sessions can sit in the
// default agent dir or in any named profile's agent dir, each of them either
// where the config tree puts it or, once omp has been migrated there, under
// $XDG_DATA_HOME/omp — so every existing sessions root is scanned, and the
// newest file across them decides. Which profile a process runs under lives
// in its environment, which is not read: a working directory's cards do not
// depend on it, and every root is looked at for every process.
package omp

import (
	"cmp"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/detect/pijsonl"
	"github.com/tonylook/managents/helper/internal/process"
)

// xdgPlatform is where omp honors $XDG_DATA_HOME: Linux and macOS, whatever
// the platform's conventions say — Windows never does.
const xdgPlatform = runtime.GOOS == "linux" || runtime.GOOS == "darwin"

// ProcessFinder lists running processes; process.System is the real one.
type ProcessFinder interface {
	// FindByName lists running processes whose executable name is exactly name.
	FindByName(ctx context.Context, name string) ([]process.Info, error)
}

// Source detects running Oh My Pi sessions.
type Source struct {
	Processes ProcessFinder
	// SessionsRoots are the installation's sessions directories: the default
	// agent dir's, and every named profile's.
	SessionsRoots []string
	// Home and TempRoot anchor the session-folder names: omp names a working
	// directory's folder after its path relative to one of them. Both are
	// canonical (symlinks resolved), like the paths omp names folders after.
	Home     string
	TempRoot string
}

// NewSource returns a Source for the Oh My Pi installation of the user whose
// home directory is home, as omp lays it out at construction time: the
// sessions folders of the default agent dir ($PI_CODING_AGENT_DIR, default
// <$PI_CONFIG_DIR or ~/.omp>/agent) and of every profile under it.
func NewSource(home string, processes ProcessFinder) *Source {
	return &Source{
		Processes:     processes,
		SessionsRoots: resolveSessionsRoots(home),
		Home:          canonical(home),
		TempRoot:      canonical(os.TempDir()),
	}
}

// resolveSessionsRoots returns where omp keeps its sessions: the default
// agent dir's sessions folder, then every profile's. omp resolves the config
// root from $PI_CONFIG_DIR (a name under home, default .omp), the agent dir
// from $PI_CODING_AGENT_DIR, and the sessions folder from both through its
// directory resolver.
func resolveSessionsRoots(home string) []string {
	root := filepath.Join(home, configDirName())
	defaultAgent := filepath.Join(root, "agent")
	agentDir := defaultAgent
	if dir := os.Getenv("PI_CODING_AGENT_DIR"); dir != "" {
		agentDir = filepath.Clean(dir)
	}
	roots := []string{sessionsRoot(agentDir, agentDir == defaultAgent, "")}
	profiles, err := os.ReadDir(filepath.Join(root, "profiles"))
	if err != nil {
		return roots // no profiles: the default agent dir is all there is
	}
	for _, entry := range profiles {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		profileAgent := filepath.Join(root, "profiles", name, "agent")
		roots = append(roots, sessionsRoot(profileAgent, true, name))
	}
	return roots
}

// configDirName is the name of omp's config root under home: $PI_CONFIG_DIR,
// else .omp.
func configDirName() string {
	if dir := os.Getenv("PI_CONFIG_DIR"); dir != "" {
		return dir
	}
	return ".omp"
}

// sessionsRoot is the sessions folder of one agent dir, resolved the way omp's
// directory resolver resolves its data-category paths: on Linux and macOS,
// for the default agent dir, an $XDG_DATA_HOME whose omp folder exists takes
// over — and flattens the agent/ prefix (a named profile's under the
// profile's folder). omp trusts the folder's existence as the sign that its
// `omp config migrate` has run, and keeps the agent dir otherwise.
func sessionsRoot(agentDir string, isDefault bool, profile string) string {
	if !isDefault || !xdgPlatform {
		return filepath.Join(agentDir, "sessions")
	}
	if data := os.Getenv("XDG_DATA_HOME"); data != "" {
		base := filepath.Join(data, "omp")
		if profile != "" {
			base = filepath.Join(base, "profiles", profile)
		}
		if _, err := os.Stat(base); err == nil {
			return filepath.Join(base, "sessions")
		}
	}
	return filepath.Join(agentDir, "sessions")
}

// canonical resolves a path's symlinks: omp names session folders after the
// real paths of home, the temp root and the working directory. A path that
// cannot be resolved stays as it is.
func canonical(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// Name implements detect.Source.
func (s *Source) Name() string { return string(agent.KindOmp) }

// Sessions implements detect.Source. A folder whose session files cannot be
// read is still shown, as waiting, and the errors are returned.
func (s *Source) Sessions(ctx context.Context, now time.Time) ([]agent.Session, error) {
	procs, err := s.Processes.FindByName(ctx, "omp")
	if err != nil {
		return nil, err
	}
	// The oldest process in a folder owns its card, as for Codex and pi.
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

// toSession builds the card of a folder: the newest session file the
// installation has for it, or a waiting card when omp has written none there
// yet.
func (s *Source) toSession(p process.Info, now time.Time) (agent.Session, error) {
	session := agent.Session{
		ID:        string(agent.KindOmp) + ":" + strconv.Itoa(p.PID),
		Kind:      agent.KindOmp,
		Dir:       p.Dir,
		Status:    agent.StatusWaiting,
		Since:     p.StartedAt,
		StartedAt: p.StartedAt,
	}
	path, h, err := s.activeSession(p.Dir)
	if err != nil {
		return session, err
	}
	if path == "" {
		return session, nil // omp has written no session file for this folder yet
	}
	summary, err := pijsonl.Summarize(path)
	if err != nil {
		return session, err
	}
	if h.SessionID != "" {
		session.ID = string(agent.KindOmp) + ":" + h.SessionID
	} else {
		session.ID = string(agent.KindOmp) + ":" + strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}
	if !h.Timestamp.IsZero() {
		session.StartedAt = h.Timestamp
	}
	session.Context = summary.Context
	session.Status, session.Since = pijsonl.StatusOf(summary, p.StartedAt, now)
	return session, nil
}

// activeSession returns the folder's active session file: the newest one in
// any sessions root whose folder is named after the working directory, and
// whose header carries it (under either of its spellings). The path is empty
// when there is none.
func (s *Source) activeSession(dir string) (string, pijsonl.Head, error) {
	folders := sessionFolders(dir, s.Home, s.TempRoot)
	var (
		best     string
		bestHead pijsonl.Head
		bestMod  time.Time
	)
	for _, root := range s.SessionsRoots {
		path, h, err := pijsonl.NewestSessionFile(root, headerDirs(dir), folders)
		if err != nil {
			return "", pijsonl.Head{}, err
		}
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", pijsonl.Head{}, err
		}
		if best == "" || info.ModTime().After(bestMod) {
			best, bestHead, bestMod = path, h, info.ModTime()
		}
	}
	return best, bestHead, nil
}

// headerDirs are the spellings a session header may carry for the working
// directory: the process's, and — on macOS, where omp writes the /private
// prefix of the real path out of session headers — the stripped one. A
// session file is only ever matched inside the folder named after the
// directory, so the alias cannot pull in another directory's session.
func headerDirs(dir string) []string {
	if runtime.GOOS == "darwin" && strings.HasPrefix(dir, "/private/") {
		return []string{dir, dir[len("/private"):]}
	}
	return []string{dir}
}

// sessionFolders returns the folder names a working directory's sessions live
// in. omp names the folder after the working directory relative to the OS
// temp root — checked first, wherever it nests inside home — or relative to
// home, and keeps pi's absolute encoding for directories outside both.
// Sessions written before omp moved its folders sit under the absolute name,
// so it stays a candidate; the header match keeps two folders that encode
// alike apart.
func sessionFolders(cwd, home, tempRoot string) []string {
	var name string
	switch {
	case within(tempRoot, cwd):
		name = encodedRelative("-tmp", tempRoot, cwd)
	case within(home, cwd):
		name = encodedRelative("-", home, cwd)
	default:
		return []string{encodedAbsolute(cwd)}
	}
	if legacy := encodedAbsolute(cwd); legacy != name {
		return []string{name, legacy}
	}
	return []string{name}
}

// encodedRelative is omp's name for a directory under one of its roots: the
// path relative to the root with / \ : turned into '-', behind the scope
// prefix ("-" for home, "-tmp" for the temp root).
func encodedRelative(prefix, root, cwd string) string {
	rel, err := filepath.Rel(root, cwd)
	if err != nil {
		return encodedAbsolute(cwd) // different drives on Windows: omp's absolute name
	}
	if rel == "." {
		rel = "" // the root itself
	}
	encoded := pathSeparatorReplacer.Replace(rel)
	if encoded == "" {
		return prefix
	}
	if !strings.HasSuffix(prefix, "-") {
		prefix += "-"
	}
	return prefix + encoded
}

// encodedAbsolute is pi's folder name, which omp keeps for directories
// outside home and the temp root, and which the runs before omp moved its
// folders used everywhere: the path with its leading slash stripped and
// / \ : turned into '-', wrapped in double dashes.
func encodedAbsolute(cwd string) string {
	if len(cwd) > 0 && (cwd[0] == '/' || cwd[0] == '\\') {
		cwd = cwd[1:]
	}
	return "--" + pathSeparatorReplacer.Replace(cwd) + "--"
}

var pathSeparatorReplacer = strings.NewReplacer("/", "-", "\\", "-", ":", "-")

// within reports whether cwd sits at or inside root, the check omp sorts its
// roots by (temp root before home).
func within(root, cwd string) bool {
	rel, err := filepath.Rel(root, cwd)
	return err == nil && rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
