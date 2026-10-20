//go:build !windows

package process

import (
	"context"
	"io"
	"os"
	"os/exec"
	"testing"
)

// TestStartTimeOfAnUnreapedProcess covers the child that has exited but has
// not been collected yet: its pid and start time still record and answer as
// they did while it ran. Claude Code leaves session records behind in exactly
// this state (the IntelliJ plugin does, for hours), which used to keep a card
// on the display. Windows reports the pid the same way before it is reaped,
// and has no zombie state to filter, so this test runs on macOS and Linux.
func TestStartTimeOfAnUnreapedProcess(t *testing.T) {
	child := exec.Command(os.Args[0], "-test.run=^$") // this test binary, running no tests
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Wait() })

	// The pipe ends exactly when the child has: no polling needed.
	if _, err := io.Copy(io.Discard, stdout); err != nil {
		t.Fatal(err)
	}

	if _, running := (System{}).StartTime(context.Background(), child.Process.Pid); running {
		t.Errorf("pid %d of an unreaped process is reported as running", child.Process.Pid)
	}
}
