// Package uistate is the UI-agnostic core of the macOS status app. It keeps a
// live View of the daemon's state by streaming the control socket (with
// automatic reconnect) and computes an aggregate Health for the menubar icon.
// It has no GUI dependency so it can be unit-tested on any platform.
package uistate

import (
	"context"
	"sync"
	"time"

	"github.com/mwdomino/tether/internal/control"
	"github.com/mwdomino/tether/internal/registry"
)

// Health is the aggregate state shown by the menubar icon.
type Health int

const (
	// HealthDaemonDown means the control socket is unreachable.
	HealthDaemonDown Health = iota
	// HealthEmpty means the daemon is up but no boxes are configured.
	HealthEmpty
	// HealthOK means every box is connected.
	HealthOK
	// HealthDegraded means at least one box is still connecting (none down).
	HealthDegraded
	// HealthDown means at least one box is disconnected.
	HealthDown
)

func (h Health) String() string {
	switch h {
	case HealthDaemonDown:
		return "daemon not running"
	case HealthEmpty:
		return "no boxes configured"
	case HealthOK:
		return "all connected"
	case HealthDegraded:
		return "connecting"
	case HealthDown:
		return "a box is down"
	default:
		return "unknown"
	}
}

// View is an immutable snapshot handed to the UI on each update.
type View struct {
	Connected bool // is the control socket reachable?
	Boxes     []registry.BoxStatus
	Requests  []registry.RequestRecord // most recent last
}

// Aggregate reduces a View to a single Health for the icon.
func Aggregate(v View) Health {
	if !v.Connected {
		return HealthDaemonDown
	}
	if len(v.Boxes) == 0 {
		return HealthEmpty
	}
	allConnected := true
	for _, b := range v.Boxes {
		if b.State == "disconnected" {
			return HealthDown
		}
		if b.State != "connected" {
			allConnected = false
		}
	}
	if allConnected {
		return HealthOK
	}
	return HealthDegraded
}

// Options configure a Model.
type Options struct {
	Network     string // "unix"
	Addr        string // control socket path
	Retry       time.Duration
	MaxRequests int
	OnUpdate    func(View)
}

// Model streams the daemon's state and pushes Views to OnUpdate.
type Model struct {
	opts Options

	mu   sync.Mutex
	view View
}

// NewModel builds a Model, applying defaults.
func NewModel(opts Options) *Model {
	if opts.Network == "" {
		opts.Network = "unix"
	}
	if opts.Retry <= 0 {
		opts.Retry = 2 * time.Second
	}
	if opts.MaxRequests <= 0 {
		opts.MaxRequests = 50
	}
	return &Model{opts: opts}
}

// View returns the current view.
func (m *Model) View() View {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.view
}

// Reload asks the daemon to re-read its config.
func (m *Model) Reload() error {
	return control.Reload(m.opts.Network, m.opts.Addr)
}

// Run streams state until ctx is canceled, reconnecting on failure.
func (m *Model) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		snap, events, err := control.Stream(ctx, m.opts.Network, m.opts.Addr)
		if err != nil {
			m.setDown()
			if !sleepCtx(ctx, m.opts.Retry) {
				return
			}
			continue
		}
		m.applySnapshot(snap)
		for ev := range events {
			m.applyEvent(ev)
		}
		// Stream ended → daemon gone or ctx canceled.
		m.setDown()
		if !sleepCtx(ctx, m.opts.Retry) {
			return
		}
	}
}

func (m *Model) applySnapshot(snap registry.Snapshot) {
	m.mu.Lock()
	m.view.Connected = true
	m.view.Boxes = append([]registry.BoxStatus(nil), snap.Boxes...)
	m.view.Requests = capRequests(snap.Requests, m.opts.MaxRequests)
	v := m.view
	m.mu.Unlock()
	m.emit(v)
}

func (m *Model) applyEvent(ev registry.Event) {
	m.mu.Lock()
	switch {
	case ev.Status != nil:
		m.view.Boxes = upsertBox(m.view.Boxes, *ev.Status)
	case ev.Request != nil:
		m.view.Requests = capRequests(append(m.view.Requests, *ev.Request), m.opts.MaxRequests)
	}
	v := m.view
	m.mu.Unlock()
	m.emit(v)
}

func (m *Model) setDown() {
	m.mu.Lock()
	if !m.view.Connected && m.view.Boxes == nil {
		m.mu.Unlock()
		return // already down; avoid redundant emits
	}
	m.view.Connected = false
	m.view.Boxes = nil
	v := m.view
	m.mu.Unlock()
	m.emit(v)
}

func (m *Model) emit(v View) {
	if m.opts.OnUpdate != nil {
		m.opts.OnUpdate(v)
	}
}

// upsertBox returns a new slice with st inserted or replacing the same-named
// box. It never mutates the input slice, so previously emitted Views (which
// share the old backing array) are not raced.
func upsertBox(boxes []registry.BoxStatus, st registry.BoxStatus) []registry.BoxStatus {
	out := make([]registry.BoxStatus, len(boxes), len(boxes)+1)
	copy(out, boxes)
	for i := range out {
		if out[i].Name == st.Name {
			out[i] = st
			return out
		}
	}
	return append(out, st)
}

func capRequests(reqs []registry.RequestRecord, max int) []registry.RequestRecord {
	if len(reqs) <= max {
		// Copy to avoid aliasing the registry's slice.
		out := make([]registry.RequestRecord, len(reqs))
		copy(out, reqs)
		return out
	}
	out := make([]registry.RequestRecord, max)
	copy(out, reqs[len(reqs)-max:])
	return out
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
