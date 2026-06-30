package host

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/mwdomino/tether/internal/proto"
)

// TunnelGracePeriod is the idle window after the last tunnel connection
// closes before the host-side listener is torn down. Exported as a var
// (not a const) so cross-package tests can shorten it. Production callers
// should not modify it.
var TunnelGracePeriod = 60 * time.Second

// tunnelMgr binds host loopback ports and opens substreams back to the agent
// for each incoming connection.
type tunnelMgr struct {
	session *yamux.Session
	log     *slog.Logger

	mu        sync.Mutex
	listeners map[int]*tunnelListener
	done      chan struct{} // closed when all listeners released
}

type tunnelListener struct {
	port      int
	listeners []net.Listener
	active    int32 // current number of in-flight tunnel conns
	parent    *tunnelMgr
	timer     *time.Timer
}

func newTunnelMgr(session *yamux.Session, log *slog.Logger) *tunnelMgr {
	return &tunnelMgr{
		session:   session,
		log:       log,
		listeners: map[int]*tunnelListener{},
		done:      make(chan struct{}),
	}
}

// bind attempts to listen on every port. On any failure all already-bound
// listeners are released and the offending port is returned.
func (m *tunnelMgr) bind(ports []int) (failedPort int, err error) {
	for _, p := range ports {
		listeners, err := listenLoopbackPort(p)
		if err != nil {
			m.releaseAll()
			return p, err
		}
		tl := &tunnelListener{port: p, listeners: listeners, parent: m}
		m.mu.Lock()
		m.listeners[p] = tl
		m.mu.Unlock()
		for _, ln := range listeners {
			go tl.acceptLoop(ln)
		}
		// Do NOT arm the grace timer here. The grace period is a tail buffer
		// for follow-on requests (favicons, IdP "you can close this tab" pages)
		// AFTER the first callback completes — not a kill switch for the
		// initial wait. Slow SSO flows (AWS SSO with MFA, etc.) routinely take
		// longer than the grace period to send the user back to the redirect.
		// Until the first connection arrives we rely on the agent's overall
		// timeout (default 5min) and session-close detection to tear things
		// down.
	}
	return 0, nil
}

func (m *tunnelMgr) releaseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, tl := range m.listeners {
		for _, ln := range tl.listeners {
			_ = ln.Close()
		}
		if tl.timer != nil {
			tl.timer.Stop()
		}
	}
	m.listeners = map[int]*tunnelListener{}
	select {
	case <-m.done:
	default:
		close(m.done)
	}
}

// wait blocks until all listeners are released, ctx is canceled, or the
// session dies.
func (m *tunnelMgr) wait(ctx context.Context) {
	select {
	case <-m.done:
	case <-ctx.Done():
	case <-m.session.CloseChan():
	}
	m.releaseAll()
}

func listenLoopbackPort(port int) ([]net.Listener, error) {
	ipv4, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}
	listeners := []net.Listener{ipv4}

	ipv6, err := net.Listen("tcp", fmt.Sprintf("[::1]:%d", port))
	if err == nil {
		listeners = append(listeners, ipv6)
	}
	return listeners, nil
}

func (tl *tunnelListener) acceptLoop(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		// Bump active BEFORE disarmGrace so a racing timer that grabs the mutex
		// first observes active>0 and re-arms instead of tearing the listener
		// down with an in-flight connection.
		atomic.AddInt32(&tl.active, 1)
		tl.disarmGrace()
		go tl.handleConn(c)
	}
}

func (tl *tunnelListener) handleConn(c net.Conn) {
	defer c.Close()
	defer func() {
		if atomic.AddInt32(&tl.active, -1) == 0 {
			tl.armGrace()
		}
	}()

	tl.parent.log.Info("tunnel: browser connected", "port", tl.port, "remote", c.RemoteAddr())

	stream, err := tl.parent.session.OpenStream()
	if err != nil {
		tl.parent.log.Error("tunnel: open substream", "port", tl.port, "err", err)
		return
	}
	defer stream.Close()

	if err := proto.WriteFrame(stream, proto.TunnelHeader{Kind: "tunnel", Port: tl.port}); err != nil {
		tl.parent.log.Error("tunnel: write header", "port", tl.port, "err", err)
		return
	}

	var upN, downN atomic.Int64
	upDone := make(chan struct{})
	downDone := make(chan struct{})
	go func() {
		n, _ := io.Copy(stream, c)
		upN.Store(n)
		close(upDone)
	}()
	go func() {
		n, _ := io.Copy(c, stream)
		downN.Store(n)
		close(downDone)
	}()

	// Wait for either direction to finish. The first finisher does NOT close
	// the connections — we give the other side a bounded window to drain.
	// Without this, when the browser closes its conn (Firefox times out a
	// stalled localhost response at ~5s), we'd otherwise tear the yamux
	// substream down and cut the agent's read from AWS CLI mid-response.
	select {
	case <-upDone:
		// Browser→agent done. Give agent→browser up to 30s to deliver the
		// response, then force-close.
		select {
		case <-downDone:
		case <-time.After(30 * time.Second):
		}
	case <-downDone:
		// Agent→browser done. The response has been delivered (or AWS CLI
		// closed). Browser may still hold a keep-alive read, but the OAuth
		// flow is complete; give the other direction a few seconds and bail.
		select {
		case <-upDone:
		case <-time.After(3 * time.Second):
		}
	}
	tl.parent.log.Info("tunnel: pipe ended", "port", tl.port, "bytes_to_agent", upN.Load(), "bytes_from_agent", downN.Load())
}

func (tl *tunnelListener) armGrace() {
	tl.parent.mu.Lock()
	defer tl.parent.mu.Unlock()
	if _, ok := tl.parent.listeners[tl.port]; !ok {
		return
	}
	if tl.timer != nil {
		tl.timer.Stop()
	}
	tl.timer = time.AfterFunc(TunnelGracePeriod, func() {
		tl.parent.removeListener(tl.port)
	})
}

func (tl *tunnelListener) disarmGrace() {
	tl.parent.mu.Lock()
	defer tl.parent.mu.Unlock()
	if tl.timer != nil {
		tl.timer.Stop()
		tl.timer = nil
	}
}

func (m *tunnelMgr) removeListener(port int) {
	m.mu.Lock()
	tl, ok := m.listeners[port]
	if !ok {
		m.mu.Unlock()
		return
	}
	// If a connection is in flight (acceptLoop bumped active before disarmGrace
	// raced this timer), re-arm grace and bail. handleConn's defer will call
	// armGrace once active drops back to zero.
	if atomic.LoadInt32(&tl.active) > 0 {
		tl.timer = time.AfterFunc(TunnelGracePeriod, func() {
			tl.parent.removeListener(tl.port)
		})
		m.mu.Unlock()
		return
	}
	delete(m.listeners, port)
	for _, ln := range tl.listeners {
		_ = ln.Close()
	}
	empty := len(m.listeners) == 0
	m.mu.Unlock()
	if empty {
		select {
		case <-m.done:
		default:
			close(m.done)
		}
	}
}
