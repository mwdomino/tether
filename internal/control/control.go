// Package control is the local IPC channel between the host daemon and its
// clients (the `tether status` CLI and the macOS GUI). It reuses the proto
// length-prefixed JSON framing over a unix socket: a client sends one
// ClientRequest, and the server replies with one or a stream of ServerMessage
// frames depending on the command.
package control

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/mwdomino/tether/internal/proto"
	"github.com/mwdomino/tether/internal/registry"
)

// Commands a client may send.
const (
	CmdSnapshot  = "snapshot"
	CmdSubscribe = "subscribe"
	CmdReload    = "reload"
)

// ClientRequest is the single frame a client sends after connecting.
type ClientRequest struct {
	Command string `json:"command"`
}

// ServerMessage is a frame the server sends back. Exactly one payload field is
// set per frame.
type ServerMessage struct {
	Snapshot *registry.Snapshot `json:"snapshot,omitempty"`
	Event    *registry.Event    `json:"event,omitempty"`
	OK       bool               `json:"ok,omitempty"`
	Error    string             `json:"error,omitempty"`
}

// Server answers control connections against a registry.
type Server struct {
	reg      *registry.Registry
	onReload func() error
	log      *slog.Logger
}

// NewServer builds a control Server. onReload may be nil.
func NewServer(reg *registry.Registry, onReload func() error, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{reg: reg, onReload: onReload, log: log}
}

// Serve accepts connections until ctx is canceled or ln fails.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.handle(ctx, conn)
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	var req ClientRequest
	if err := proto.ReadFrame(conn, &req); err != nil {
		return
	}
	switch req.Command {
	case CmdSnapshot:
		snap := s.reg.Snapshot()
		_ = proto.WriteFrame(conn, ServerMessage{Snapshot: &snap})
	case CmdSubscribe:
		s.stream(ctx, conn)
	case CmdReload:
		msg := ServerMessage{OK: true}
		if s.onReload != nil {
			if err := s.onReload(); err != nil {
				msg = ServerMessage{Error: err.Error()}
			}
		}
		_ = proto.WriteFrame(conn, msg)
	default:
		_ = proto.WriteFrame(conn, ServerMessage{Error: "unknown command: " + req.Command})
	}
}

func (s *Server) stream(ctx context.Context, conn net.Conn) {
	snap := s.reg.Snapshot()
	if err := proto.WriteFrame(conn, ServerMessage{Snapshot: &snap}); err != nil {
		return
	}
	events, cancel := s.reg.Subscribe()
	defer cancel()

	// Detect client disconnect: the client sends nothing more, so a read
	// unblocks only on EOF/close.
	closed := make(chan struct{})
	go func() {
		var b [1]byte
		_, _ = conn.Read(b[:])
		close(closed)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-closed:
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			e := ev
			if err := proto.WriteFrame(conn, ServerMessage{Event: &e}); err != nil {
				return
			}
		}
	}
}

func dial(network, addr string) (net.Conn, error) {
	d := net.Dialer{Timeout: 3 * time.Second}
	return d.Dial(network, addr)
}

// Snapshot fetches the daemon's current state.
func Snapshot(network, addr string) (registry.Snapshot, error) {
	conn, err := dial(network, addr)
	if err != nil {
		return registry.Snapshot{}, err
	}
	defer conn.Close()
	if err := proto.WriteFrame(conn, ClientRequest{Command: CmdSnapshot}); err != nil {
		return registry.Snapshot{}, err
	}
	var msg ServerMessage
	if err := proto.ReadFrame(conn, &msg); err != nil {
		return registry.Snapshot{}, err
	}
	if msg.Error != "" {
		return registry.Snapshot{}, errors.New(msg.Error)
	}
	if msg.Snapshot == nil {
		return registry.Snapshot{}, errors.New("control: empty snapshot response")
	}
	return *msg.Snapshot, nil
}

// Reload asks the daemon to re-read its config and reconcile boxes.
func Reload(network, addr string) error {
	conn, err := dial(network, addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := proto.WriteFrame(conn, ClientRequest{Command: CmdReload}); err != nil {
		return err
	}
	var msg ServerMessage
	if err := proto.ReadFrame(conn, &msg); err != nil {
		return err
	}
	if msg.Error != "" {
		return errors.New(msg.Error)
	}
	return nil
}

// Stream subscribes to live events. It returns the initial snapshot and a
// channel of subsequent events; the channel closes when ctx is canceled or the
// connection ends.
func Stream(ctx context.Context, network, addr string) (registry.Snapshot, <-chan registry.Event, error) {
	conn, err := dial(network, addr)
	if err != nil {
		return registry.Snapshot{}, nil, err
	}
	if err := proto.WriteFrame(conn, ClientRequest{Command: CmdSubscribe}); err != nil {
		conn.Close()
		return registry.Snapshot{}, nil, err
	}
	var first ServerMessage
	if err := proto.ReadFrame(conn, &first); err != nil {
		conn.Close()
		return registry.Snapshot{}, nil, err
	}
	if first.Snapshot == nil {
		conn.Close()
		return registry.Snapshot{}, nil, errors.New("control: expected initial snapshot")
	}

	out := make(chan registry.Event)
	go func() {
		defer close(out)
		defer conn.Close()
		go func() {
			<-ctx.Done()
			_ = conn.Close()
		}()
		for {
			var msg ServerMessage
			if err := proto.ReadFrame(conn, &msg); err != nil {
				return
			}
			if msg.Event == nil {
				continue
			}
			select {
			case out <- *msg.Event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return *first.Snapshot, out, nil
}
