// Package registry holds the host daemon's live state — per-box connection
// status and a bounded, in-memory log of recent open requests — and fans out
// change events to subscribed control clients (the status CLI and the GUI).
package registry

import (
	"sync"
	"time"
)

// BoxStatus is a box's connection state as exposed to control clients.
type BoxStatus struct {
	Name      string    `json:"name"`
	SSHHost   string    `json:"ssh_host"`
	State     string    `json:"state"`
	LastError string    `json:"last_error,omitempty"`
	Since     time.Time `json:"since"`
}

// RequestRecord is one URL the daemon was asked to open.
type RequestRecord struct {
	Box     string    `json:"box"`
	URL     string    `json:"url"`
	Time    time.Time `json:"time"`
	Outcome string    `json:"outcome"`
}

// Event is a single change pushed to subscribers.
type Event struct {
	Kind    string         `json:"kind"` // "status" or "request"
	Status  *BoxStatus     `json:"status,omitempty"`
	Request *RequestRecord `json:"request,omitempty"`
}

// Snapshot is the full current state, sent to a client on connect.
type Snapshot struct {
	Boxes    []BoxStatus     `json:"boxes"`
	Requests []RequestRecord `json:"requests"`
}

const subscriberBuffer = 64

// Registry is safe for concurrent use.
type Registry struct {
	maxRequests int

	mu       sync.Mutex
	boxes    map[string]BoxStatus
	order    []string
	requests []RequestRecord
	subs     map[int]chan Event
	nextSub  int
}

// New builds a Registry retaining at most maxRequests recent requests.
func New(maxRequests int) *Registry {
	if maxRequests <= 0 {
		maxRequests = 100
	}
	return &Registry{
		maxRequests: maxRequests,
		boxes:       make(map[string]BoxStatus),
		subs:        make(map[int]chan Event),
	}
}

// SetBoxStatus records the latest status for a box and notifies subscribers.
func (r *Registry) SetBoxStatus(st BoxStatus) {
	r.mu.Lock()
	if _, ok := r.boxes[st.Name]; !ok {
		r.order = append(r.order, st.Name)
	}
	r.boxes[st.Name] = st
	r.mu.Unlock()

	s := st
	r.publish(Event{Kind: "status", Status: &s})
}

// RecordRequest appends a request to the bounded log and notifies subscribers.
func (r *Registry) RecordRequest(rec RequestRecord) {
	if rec.Time.IsZero() {
		rec.Time = time.Now()
	}
	r.mu.Lock()
	r.requests = append(r.requests, rec)
	if len(r.requests) > r.maxRequests {
		r.requests = r.requests[len(r.requests)-r.maxRequests:]
	}
	r.mu.Unlock()

	rc := rec
	r.publish(Event{Kind: "request", Request: &rc})
}

// ResetBoxes clears all box statuses (used on config reload) while keeping the
// request log intact.
func (r *Registry) ResetBoxes() {
	r.mu.Lock()
	r.boxes = make(map[string]BoxStatus)
	r.order = nil
	r.mu.Unlock()
}

// Snapshot returns a copy of the current state.
func (r *Registry) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	boxes := make([]BoxStatus, 0, len(r.order))
	for _, name := range r.order {
		boxes = append(boxes, r.boxes[name])
	}
	reqs := make([]RequestRecord, len(r.requests))
	copy(reqs, r.requests)
	return Snapshot{Boxes: boxes, Requests: reqs}
}

// Subscribe returns a channel of future events and a cancel func. The channel
// is closed by cancel. Events are dropped for a subscriber whose buffer is
// full, so a slow client never blocks the daemon.
func (r *Registry) Subscribe() (<-chan Event, func()) {
	r.mu.Lock()
	id := r.nextSub
	r.nextSub++
	ch := make(chan Event, subscriberBuffer)
	r.subs[id] = ch
	r.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			r.mu.Lock()
			if c, ok := r.subs[id]; ok {
				delete(r.subs, id)
				close(c)
			}
			r.mu.Unlock()
		})
	}
	return ch, cancel
}

func (r *Registry) publish(ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ch := range r.subs {
		select {
		case ch <- ev:
		default:
			// Subscriber is behind; drop rather than block. It can re-snapshot.
		}
	}
}
