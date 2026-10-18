package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Tabs is the sessions OpenCode has open in its TUI tabs, by working directory.
type Tabs interface {
	// OpenTabs returns the session ids of the tabs open in dir, in tab order.
	// An empty result means the folder is not open in a tab.
	OpenTabs(ctx context.Context, dir string) ([]string, error)
}

// StateTabs reads OpenCode's TUI state file, where OpenCode 2 records an entry
// per working directory with the tabs kept open there. The tabs outlive the TUI
// (they are still listed for a folder whose process is gone), so the caller
// still has to find a running process to know that a folder is open at all.
type StateTabs struct {
	Path string
}

// DefaultTabsPath is where OpenCode keeps the TUI state for the user whose home
// directory is home. It follows the XDG layout: $XDG_STATE_HOME when it is set,
// otherwise ~/.local/state, a different root from the database's.
func DefaultTabsPath(home string) string {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "opencode", "latest", "tui", "tabs.json")
}

// tabEntry is one open tab. Only its session id is decoded: the title next to
// it is conversation text, and the type has no field for it, so it never enters
// this process.
type tabEntry struct {
	SessionID string `json:"sessionID"`
}

// folderTabs is the tab state of one working directory.
type folderTabs struct {
	Tabs []tabEntry `json:"tabs"`
}

// tabsFile mirrors the part of the state file this package needs. The state of
// the opt-in global tab scope is under "global" instead of "cwd" and is
// ignored, like the rest of the file.
type tabsFile struct {
	Cwd map[string]folderTabs `json:"cwd"`
}

// OpenTabs implements Tabs.
func (s StateTabs) OpenTabs(_ context.Context, dir string) ([]string, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil // OpenCode 1, or a TUI that has not written a tab yet
	}
	if err != nil {
		return nil, err
	}
	var file tabsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("tab state %s: %w", s.Path, err)
	}
	var ids []string
	seen := make(map[string]bool)
	for _, tab := range tabState(file, dir).Tabs {
		if tab.SessionID == "" || seen[tab.SessionID] {
			continue
		}
		seen[tab.SessionID] = true
		ids = append(ids, tab.SessionID)
	}
	return ids, nil
}

// tabState finds the entry of dir. The path OpenCode wrote and the path of the
// process are the same string in practice; a trailing separator is compared
// away so a stray slash cannot hide every tab.
func tabState(file tabsFile, dir string) folderTabs {
	if state, ok := file.Cwd[dir]; ok {
		return state
	}
	for cwd, state := range file.Cwd {
		if filepath.Clean(cwd) == filepath.Clean(dir) {
			return state
		}
	}
	return folderTabs{}
}
