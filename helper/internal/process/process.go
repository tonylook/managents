// Package process inspects running processes. Each detector declares the
// small interface it needs from System, so it can be tested without real
// processes.
package process

import (
	"context"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

// Info describes a running process.
type Info struct {
	PID       int
	Dir       string // working directory, empty if not readable
	StartedAt time.Time
}

// System inspects the processes of the local operating system.
type System struct{}

// StartTime returns when the process started, or false if it is not running.
func (System) StartTime(ctx context.Context, pid int) (time.Time, bool) {
	p, err := process.NewProcessWithContext(ctx, int32(pid))
	if err != nil {
		return time.Time{}, false
	}
	millis, err := p.CreateTimeWithContext(ctx)
	if err != nil {
		return time.Time{}, false
	}
	return time.UnixMilli(millis), true
}

// FindByName lists running processes whose executable name is exactly name.
func (System) FindByName(ctx context.Context, name string) ([]Info, error) {
	all, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, err
	}
	var found []Info
	for _, p := range all {
		pname, err := p.NameWithContext(ctx)
		if err != nil || pname != name {
			continue
		}
		info := Info{PID: int(p.Pid)}
		if dir, err := p.CwdWithContext(ctx); err == nil {
			info.Dir = dir
		}
		if millis, err := p.CreateTimeWithContext(ctx); err == nil {
			info.StartedAt = time.UnixMilli(millis)
		}
		found = append(found, info)
	}
	return found, nil
}
