package claude

import (
	"bytes"
	"encoding/json"
	"io"
	"os"

	"github.com/tonylook/managents/helper/internal/agent"
)

// transcriptTail is how much of the end of a transcript is scanned. The last
// entry and the last model reply are virtually always within it.
const transcriptTail = 256 << 10

type transcriptSummary struct {
	endsInError bool
	context     *agent.ContextUsage
}

// entry declares only the metadata fields we use. Message content is never
// decoded into memory we keep, logged or sent anywhere.
type entry struct {
	Type              string `json:"type"`
	IsSidechain       bool   `json:"isSidechain"`
	IsAPIErrorMessage bool   `json:"isApiErrorMessage"`
	Message           *struct {
		Usage *struct {
			InputTokens         int `json:"input_tokens"`
			CacheCreationTokens int `json:"cache_creation_input_tokens"`
			CacheReadTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// summarizeTranscript reads the end of a session transcript (JSON lines) to
// find whether the last entry is an API error, and how many tokens the last
// main-thread model call had in its context window.
func summarizeTranscript(path string) (transcriptSummary, error) {
	tail, err := readTail(path, transcriptTail)
	if err != nil {
		return transcriptSummary{}, err
	}

	var summary transcriptSummary
	sawEntry := false
	lines := bytes.Split(tail, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		var e entry
		if len(bytes.TrimSpace(lines[i])) == 0 || json.Unmarshal(lines[i], &e) != nil {
			continue
		}
		if !sawEntry {
			sawEntry = true
			summary.endsInError = e.IsAPIErrorMessage
		}
		if e.Type == "assistant" && !e.IsSidechain && e.Message != nil && e.Message.Usage != nil {
			u := e.Message.Usage
			summary.context = &agent.ContextUsage{Used: u.InputTokens + u.CacheCreationTokens + u.CacheReadTokens}
			break
		}
	}
	if !sawEntry {
		return transcriptSummary{}, errNoEntries
	}
	return summary, nil
}

func readTail(path string, size int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	offset := info.Size() - size
	if offset < 0 {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}
