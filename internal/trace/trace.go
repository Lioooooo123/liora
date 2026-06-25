package trace

import (
	"sync"
	"time"
)

type Status string

const (
	StatusOK    Status = "ok"
	StatusError Status = "error"
)

type Event struct {
	Time   time.Time `json:"time"`
	Tool   string    `json:"tool"`
	Input  string    `json:"input"`
	Output string    `json:"output"`
	Status Status    `json:"status"`
}

type Recorder interface {
	Record(event Event)
}

type MemoryRecorder struct {
	mu     sync.Mutex
	events []Event
}

func NewMemoryRecorder() *MemoryRecorder {
	return &MemoryRecorder{}
}

func (r *MemoryRecorder) Record(event Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if event.Time.IsZero() {
		event.Time = time.Now()
	}
	r.events = append(r.events, event)
}

func (r *MemoryRecorder) Events() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	events := make([]Event, len(r.events))
	copy(events, r.events)
	return events
}
