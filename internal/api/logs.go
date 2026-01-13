package api

import "sync"

// LogBuffer stores recent log lines in memory.
type LogBuffer struct {
	mu    sync.Mutex
	lines []string
	max   int
}

func NewLogBuffer(max int) *LogBuffer {
	if max <= 0 {
		max = 500
	}
	return &LogBuffer{max: max}
}

func (b *LogBuffer) Add(line string) {
	if line == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.lines) >= b.max {
		// Drop oldest line.
		copy(b.lines, b.lines[1:])
		b.lines[len(b.lines)-1] = line
		return
	}
	b.lines = append(b.lines, line)
}

func (b *LogBuffer) Lines(tail int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.lines) == 0 {
		return nil
	}
	if tail <= 0 || tail >= len(b.lines) {
		out := make([]string, len(b.lines))
		copy(out, b.lines)
		return out
	}
	out := make([]string, tail)
	copy(out, b.lines[len(b.lines)-tail:])
	return out
}

type AccessLogs struct {
	API     *LogBuffer
	UI      *LogBuffer
	Webhook *LogBuffer
}

func NewAccessLogs(max int) *AccessLogs {
	return &AccessLogs{
		API:     NewLogBuffer(max),
		UI:      NewLogBuffer(max),
		Webhook: NewLogBuffer(max),
	}
}

func (l *AccessLogs) Get(name string) *LogBuffer {
	if l == nil {
		return nil
	}
	switch name {
	case "api":
		return l.API
	case "ui":
		return l.UI
	case "webhook":
		return l.Webhook
	default:
		return nil
	}
}
