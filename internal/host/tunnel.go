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

const tunnelGracePeriod = 10 * time.Second

// tunnelMgr binds desktop loopback ports and opens substreams back to the
// agent for each incoming connection.
type tunnelMgr struct {
	session *yamux.Session
	log     *slog.Logger

	mu        sync.Mutex
	listeners map[int]*tunnelListener
	done      chan struct{} // closed when all listeners released
}

type tunnelListener struct {
	port   int
	ln     net.Listener
	active int32 // current number of in-flight tunnel conns
	parent *tunnelMgr
	timer  *time.Timer
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
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err != nil {
			m.releaseAll()
			return p, err
		}
		tl := &tunnelListener{port: p, ln: ln, parent: m}
		m.mu.Lock()
		m.listeners[p] = tl
		m.mu.Unlock()
		go tl.acceptLoop()
		// Start the grace timer — if nothing connects, release after grace.
		tl.armGrace()
	}
	return 0, nil
}

func (m *tunnelMgr) releaseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, tl := range m.listeners {
		_ = tl.ln.Close()
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

func (tl *tunnelListener) acceptLoop() {
	for {
		c, err := tl.ln.Accept()
		if err != nil {
			return
		}
		tl.disarmGrace()
		atomic.AddInt32(&tl.active, 1)
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

	stream, err := tl.parent.session.OpenStream()
	if err != nil {
		tl.parent.log.Error("open tunnel stream", "err", err)
		return
	}
	defer stream.Close()

	if err := proto.WriteFrame(stream, proto.TunnelHeader{Kind: "tunnel", Port: tl.port}); err != nil {
		tl.parent.log.Error("write tunnel header", "err", err)
		return
	}

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(stream, c); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, stream); done <- struct{}{} }()
	<-done
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
	tl.timer = time.AfterFunc(tunnelGracePeriod, func() {
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
	delete(m.listeners, port)
	_ = tl.ln.Close()
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
