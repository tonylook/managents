// Package pijsonl reads the session logs of pi-format agents: pi, and the
// agents derived from it (omp). A session is an append-only JSON-lines file
// whose header line carries the session id, start time and working directory,
// and whose message entries carry the role, the stop reason and the token
// counts of each model call. The agents record no turn state, so the end of
// the log decides: a prompt without its reply is a turn in progress, an
// assistant stop is a finished one, and an assistant message that stopped to
// call a tool with no tool result after it is a permission prompt.
//
// Only metadata is decoded: the message contents, the title slot omp writes
// ahead of the header, the compaction and branch summaries and the tool
// results are never kept, logged or sent anywhere. An entry written off the
// active branch (omp's activeLeafId, which its loader replays against the
// recorded leaf) does not decide where the turn stands; pi never writes the
// field.
package pijsonl

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/detect"
)

// Only the end of a session file is scanned: SessionTail bytes, growing
// fourfold up to MaxSessionTail when a huge entry (a large tool result) hides
// the last message or token count.
const (
	SessionTail    = 256 << 10
	MaxSessionTail = 4 << 20
)

// HeadBytes bounds how much of the start of a session file is read to find its
// session id, start time and working directory.
const HeadBytes = 64 << 10

// Head is what the header of a session file says about its session.
type Head struct {
	SessionID string
	Timestamp time.Time // when the session was opened
	Cwd       string
}

// ReadHead reads the start of a session file, whose header line is the first
// line that names a session (omp writes a fixed-width title slot ahead of it).
// ok is false when no line names a working directory, so the file cannot be
// tied to a folder.
func ReadHead(path string) (h Head, ok bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return Head{}, false, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, HeadBytes))
	if err != nil {
		return Head{}, false, err
	}
	for rest := data; len(rest) > 0 && (h.SessionID == "" || h.Timestamp.IsZero() || h.Cwd == ""); {
		i := bytes.IndexByte(rest, '\n')
		line := rest
		if i >= 0 {
			line, rest = rest[:i], rest[i+1:]
		} else {
			rest = nil
		}
		var r headRecord
		if len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &r) != nil {
			continue
		}
		if h.Timestamp.IsZero() {
			h.Timestamp = r.Timestamp
		}
		if h.SessionID == "" {
			h.SessionID = r.SessionID
		}
		if h.Cwd == "" {
			h.Cwd = r.Cwd
		}
	}
	return h, h.Cwd != "", nil
}

// headRecord declares only the header fields we use. The title slot's text and
// everything after the header are never decoded beyond the fields below.
type headRecord struct {
	SessionID string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Cwd       string    `json:"cwd"`
}

// TurnState is where the newest turn of a session is.
type TurnState int

const (
	NoTurn TurnState = iota
	TurnRunning
	TurnEnded
)

// Summary is what the end of a session file says about its conversation.
type Summary struct {
	Turn     TurnState
	At       time.Time // when the newest turn started (running) or ended (ended)
	PromptAt time.Time // when the newest user message was sent
	Failed   bool      // the newest turn ended in an error
	Context  *agent.ContextUsage
}

// Summarize reads the end of a session file (JSON lines).
func Summarize(path string) (Summary, error) {
	for size := int64(SessionTail); ; size *= 4 {
		tail, whole, err := detect.ReadTail(path, size)
		if err != nil {
			return Summary{}, err
		}
		summary, complete := SummarizeTail(tail)
		if complete || whole || size >= MaxSessionTail {
			return summary, nil
		}
	}
}

// SummarizeTail scans the lines of tail backwards. The newest message entry
// decides where the turn stands: a user message or a tool result is a turn in
// progress, an assistant message the model's last word — stopped to call a
// tool (its tool result will come once the user approves, so it is a
// permission prompt), stopped with an error, or stopped for good. The
// timestamp of the newest user message dates the running turn, and the newest
// token count gives the context.
func SummarizeTail(tail []byte) (Summary, bool) {
	var summary Summary
	decided := false
	for rest := tail; len(rest) > 0 && !(decided && summary.Context != nil && !summary.PromptAt.IsZero()); {
		i := bytes.LastIndexByte(rest, '\n')
		line := rest[i+1:]
		rest = rest[:max(i, 0)]

		var e entry
		if len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &e) != nil || e.Message == nil || e.ActiveLeafID != "" {
			continue
		}
		if summary.Context == nil && e.Message.Usage != nil && e.Message.Usage.TotalTokens > 0 {
			summary.Context = &agent.ContextUsage{Used: e.Message.Usage.TotalTokens}
		}
		if summary.PromptAt.IsZero() && e.Message.Role == "user" {
			summary.PromptAt = e.Timestamp
		}
		if decided {
			continue
		}
		switch e.Message.Role {
		case "user", "toolResult":
			decided = true
			summary.Turn, summary.At = TurnRunning, e.Timestamp
		case "assistant":
			decided = true
			summary.At = e.Timestamp
			switch e.Message.StopReason {
			case "pending":
				summary.Turn = TurnRunning // the model is still answering
			case "error":
				summary.Turn, summary.Failed = TurnEnded, true
			default: // stop, toolUse, aborted, length, deferred
				summary.Turn = TurnEnded
			}
		}
	}
	return summary, decided
}

// entry declares only the metadata fields we use. The message contents, the
// title slot, the compaction and branch summaries and tool results are never
// decoded into memory we keep, logged or sent anywhere.
type entry struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"` // session, title, message, model_change, usage, ...
	// ActiveLeafID is set on an entry written off the active branch (omp):
	// late usage, a background result routed back to the branch it started on.
	// Its loader replays such an entry against the recorded leaf, so it says
	// nothing about where the turn stands now.
	ActiveLeafID string `json:"activeLeafId"`
	Message      *struct {
		Role       string `json:"role"` // user, assistant, toolResult, system, developer
		StopReason string `json:"stopReason"`
		Usage      *struct {
			TotalTokens int `json:"totalTokens"`
		} `json:"usage"`
	} `json:"message"`
}

// StatusOf maps the newest turn of a session to a status and when it began.
// started is when the agent process started: an entry written before that
// belongs to a previous run, so it is neither still working nor this run's
// error, and the session waits since it was opened. A prompt whose reply has
// not arrived works only while it is young: one this old is a run that died
// mid-turn.
func StatusOf(summary Summary, started, now time.Time) (agent.Status, time.Time) {
	live := !summary.At.Before(started)
	since := summary.At
	if !live {
		since = started
	}
	switch {
	case summary.Turn == NoTurn:
		return agent.StatusWaiting, since
	case live && summary.Turn == TurnRunning:
		if !summary.PromptAt.IsZero() {
			since = summary.PromptAt
		}
		if now.Sub(since) < agent.DormantAfter {
			return agent.StatusWorking, since
		}
		return agent.StatusIdle, since
	case now.Sub(since) >= agent.DormantAfter:
		return agent.StatusIdle, since
	case live && summary.Failed:
		return agent.StatusError, since
	default:
		return agent.StatusWaiting, since
	}
}

// NewestSessionFile returns the newest session file in any of the named
// folders under sessionsRoot whose header names dir, with its header. The
// agents name a working directory's folder after the encoded path, and two
// paths can encode to the same name; the header's working directory settles
// those. The path is empty when there is none: a folder the agent has written
// no sessions to is no history, not an error.
func NewestSessionFile(sessionsRoot, dir string, folders []string) (string, Head, error) {
	var (
		best     string
		bestHead Head
		bestMod  time.Time
	)
	for _, folder := range folders {
		entries, err := os.ReadDir(filepath.Join(sessionsRoot, folder))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // the agent has written no sessions for this folder
			}
			return "", Head{}, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			path := filepath.Join(sessionsRoot, folder, e.Name())
			h, ok, err := ReadHead(path)
			if err != nil {
				return "", Head{}, err
			}
			if !ok || h.Cwd != dir {
				continue // another folder that encodes to the same name
			}
			if best == "" || info.ModTime().After(bestMod) {
				best, bestHead, bestMod = path, h, info.ModTime()
			}
		}
	}
	return best, bestHead, nil
}
