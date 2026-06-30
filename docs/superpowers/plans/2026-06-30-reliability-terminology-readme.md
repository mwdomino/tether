# Reliability, Terminology, and README Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix IPv6 loopback reliability, safer Unix socket startup, race-safe tunnel counters, robust service escaping, and refresh docs around host/agent terminology.

**Architecture:** Keep the existing host/agent/yamux architecture. Add focused helpers in the existing packages and cover each behavior with regression tests before implementation. Documentation changes are terminology-only plus README quick-start cleanup.

**Tech Stack:** Go 1.26.2, cobra, yamux, standard library net/os/encoding/xml, Markdown.

## Global Constraints

- Use `host` for the GUI machine running `tether host` and browser.
- Use `agent` for the headless machine running `tether open`, `tether run`, or the shim.
- Do not add runtime dependencies.
- Preserve existing CLI flags and current successful daily workflow.
- Run `gofmt`, `go test ./...`, `go vet ./...`, and `go test -race ./...` before completion.

---

### Task 1: IPv6 loopback listeners

**Files:**
- Modify: `internal/host/tunnel.go`
- Test: `internal/agent/agent_test.go`

**Interfaces:**
- Produces: host tunnel manager binds callback ports on IPv4 and IPv6 loopback when available.

- [ ] Add a failing end-to-end test using a URL with `redirect_uri=http://[::1]:PORT/cb` and browser callback to `[::1]:PORT`.
- [ ] Run `go test ./internal/agent -run TestAgentLoopbackIPv6EndToEnd -count=1`; expect connection failure before implementation.
- [ ] Refactor `tunnelListener` to hold multiple `net.Listener`s and bind `127.0.0.1:PORT` plus `[::1]:PORT`; require at least one listener.
- [ ] Run `go test ./internal/agent ./internal/host -count=1`; expect pass.

### Task 2: Safe Unix socket startup

**Files:**
- Modify: `internal/host/host.go`
- Test: `internal/host/host_test.go`

**Interfaces:**
- Produces: `Serve` removes only stale Unix sockets and rejects non-socket path collisions.

- [ ] Add failing test that creates a regular file and starts host with `Network: "unix"` at that path; assert file remains and error mentions non-socket.
- [ ] Run targeted test; expect failure because file is removed today.
- [ ] Add `prepareUnixSocketPath(path string) error` and call it from `Serve`.
- [ ] Run `go test ./internal/host -count=1`; expect pass.

### Task 3: Race-safe tunnel counters

**Files:**
- Modify: `internal/host/tunnel.go`
- Modify: `internal/agent/agent.go`

**Interfaces:**
- Produces: no unsynchronized access to copy byte counters.

- [ ] Run `go test -race ./...` as baseline.
- [ ] Replace shared `int64` counters with `atomic.Int64` or count channels in host and agent pipe code.
- [ ] Run `go test -race ./...`; expect pass.

### Task 4: Robust service escaping

**Files:**
- Modify: `internal/install/install.go`
- Test: `internal/install/install_test.go`

**Interfaces:**
- Produces: generated Linux, macOS, and Windows service files safely quote binary paths and extra args.

- [ ] Add tests for Linux `ExecStart` with spaces/quotes, macOS plist XML escaping for `&`/`<`, and Windows extra-arg quoting.
- [ ] Run `go test ./internal/install -count=1`; expect failures.
- [ ] Implement systemd quoting, XML escaping via `encoding/xml`, and Windows argument quoting.
- [ ] Run `go test ./internal/install -count=1`; expect pass.

### Task 5: Terminology and README quick start

**Files:**
- Modify: `README.md`
- Modify: command help strings in `cmd/tether/*.go` as needed
- Modify: comments/docs in `internal/**/*.go` where user-facing terminology appears

**Interfaces:**
- Produces: docs consistently define host/agent and README starts with a quick-start path.

- [ ] Search for `host`, `host`, `agent`, `host`, and `agent`.
- [ ] Update prose to host/agent terminology while preserving technical clarity.
- [ ] Rewrite README intro/setup into an obvious quick start.
- [ ] Run `rg -n "host|host|agent|agent" README.md cmd internal` and verify no stale phrasing remains.

### Final verification

- [ ] Run `gofmt -w` on modified Go files.
- [ ] Run `go test ./...`.
- [ ] Run `go vet ./...`.
- [ ] Run `go test -race ./...`.
