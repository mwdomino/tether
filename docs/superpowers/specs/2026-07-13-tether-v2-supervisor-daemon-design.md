# tether v2 — self-healing supervisor + status daemon (spec/plan)

## Context

Tether v1 forwards URLs from a headless Linux box (**agent**) to a browser on a
GUI Mac (**host**) over an SSH `RemoteForward`. It works, but it failed in a way
that's hard to trust for a demo: a login link appeared on the box and no browser
opened on the Mac, with no visible error.

Root cause (confirmed by reading the code):

1. The "tunnel" is the user's *own interactive* SSH `RemoteForward`. Tether
   doesn't own it. When the laptop sleeps / SSH reconnects / a stale remote
   socket blocks the bind, the forward silently dies.
2. The agent shim (`internal/shim/shim.go:137`) runs `nohup tether open … >>log
   2>&1 & exit 0` — it backgrounds tether, sends *all* output to a log file, and
   returns success. So the CLI printed its own fallback URL (the "link that
   popped up") and tether's `host unreachable` error went only to
   `~/.cache/tether/open.log`.
3. Restarting `tether host` on the Mac can't help — the break was the SSH
   forward, not the host daemon.

**Goal:** treat v1 as a POC and rebuild the host side into a long-lived,
self-healing daemon that (a) *owns* its SSH forwards (keepalive + auto-reconnect,
fails loudly), (b) tracks per-box status, (c) keeps an in-memory log of requests,
and (d) exposes a local IPC socket so a Mac menubar+window GUI (and a
`tether status` CLI) can show green/red status, alert when a box is down, and
list the URLs that were sent. Drop Windows entirely.

Decisions locked with the user:
- **Transport:** tether owns a dedicated managed `ssh -N -R` per box (Option 2),
  reusing `~/.ssh/config`/keys. No relay server, no overlay network.
- **Platforms:** macOS host + Linux agent only. Windows removed.
- **GUI:** macOS-only, **menubar + separate window**, built as a *thin client*
  over the Go daemon. History is **in-memory only**.
- **Split:** all logic in the Go daemon (buildable/testable on Linux now); the
  Mac GUI is a later phase wired to the daemon's IPC.

## Architecture

```
[ Mac host: tether host daemon (service) ]        [ Linux box: agent ]

  supervisor ── ssh -N -R 9999:box.sock box ─────▶ sshd listens 127.0.0.1:9999
      │  (keepalive, ExitOnForwardFailure,               ▲
      │   auto-reconnect, per-box state)                 │ tether open dials it
      │                                                  │ (shim, unchanged)
  per-box unix listener  ◀── tunneled bytes ────────────┘
      │  (existing yamux relay: host.go + tunnel.go, unchanged)
      ▼
  registry (per-box status + request ring buffer)
      │
  control socket (proto framing) ──▶ `tether status` CLI  and  Mac GUI (thin client)
```

The **relay core is unchanged** (`internal/host/host.go`, `internal/host/tunnel.go`,
`internal/agent`, `internal/proto`). We add a supervisor, a registry, and a
control/IPC channel around it; we replace the "user hand-edits RemoteForward"
setup with tether managing the SSH process.

Why this fixes the failure: `ssh -o ExitOnForwardFailure=yes` exits immediately
if the forward can't bind (instead of silently connecting), the supervisor sees
the exit and reconnects, and the box's status goes **red** in the menubar with
the ssh error — the failure becomes visible instead of silent. Forwarding to a
remote TCP port (not a remote unix socket) also removes the stale-socket gotcha.

## Phase 1 — Go daemon (Linux-buildable, this plan's implementation scope)

### 1. Box configuration — `internal/config` (new)
- Config file at `$XDG_CONFIG_HOME/tether/config.toml` (fallback
  `~/.config/tether/config.toml`); macOS uses the same home-relative path.
- Schema: a list of boxes, each `{ name, ssh_host (alias from ~/.ssh/config),
  remote_port (default 9999) }`, plus daemon settings (control socket path,
  optional auth_token).
- Functions: `Load() (*Config, error)`, `Save(*Config) error`, `AddBox`,
  `RemoveBox`. Use `encoding/json` or a tiny TOML dep — prefer JSON to avoid a
  new dependency unless the user wants TOML.

### 2. SSH supervisor — `internal/supervisor` (new)
- One supervised process per box:
  `ssh -N -T -o BatchMode=yes -o ExitOnForwardFailure=yes
   -o ServerAliveInterval=15 -o ServerAliveCountMax=3
   -R <remote_port>:<per-box-host-socket> <ssh_host>`.
  - `BatchMode=yes` enforces the key-based requirement (never hangs on a prompt).
  - `ExitOnForwardFailure=yes` makes bind failures loud.
  - `ServerAlive*` detects laptop-sleep/dead links (~45s).
- Per-box state machine: `Disconnected → Connecting → Connected → Backoff → …`.
  Treat "process alive > ~3s" as `Connected`; process exit → `Disconnected` with
  captured stderr as `lastError`. Exponential backoff (1s→30s cap), reset after a
  stable connection.
- **Testability:** inject the command runner (`type Runner func(ctx, name,
  args…) *exec.Cmd`, default wraps `exec.CommandContext`) so tests drive a fake
  `ssh` script and assert state transitions without real SSH.
- Known v1 limitation (document it): status = ssh-process liveness + keepalives,
  not a full end-to-end probe. An agent-side heartbeat could harden this later.

### 3. Status registry — `internal/registry` (new)
- Holds per-box status (from supervisor) and a bounded ring buffer of recent
  requests `{ box, url, timestamp, outcome }`.
- Subscribe/notify hub: emits events (`box_status_changed`, `request`) to
  connected control clients. Guard with a mutex; fan out over channels.
- Request→box attribution via **per-box listeners** (below): the listener that
  accepted the connection identifies the box.

### 4. Host daemon refactor — `internal/host` + `cmd/tether/cmd_host.go`
- `tether host` becomes: load config → start supervisor → bind **one unix
  listener per box** (`$XDG_RUNTIME_DIR/tether/<box>.sock`, each the `-R` target)
  → start the control socket → serve until signal.
- Reuse existing `Host`/`handleConn`/`tunnelMgr` per listener unchanged. Thread a
  box identity + a hook into `handleConn` so "request received" / "browser
  launched" / errors are reported to the registry (the explore map lists the
  exact log points at `host.go:126/145/156` and `tunnel.go:146/196`). Prefer an
  explicit event callback over scraping slog.
- Add a config-reload path (control-socket `reload` command and/or `SIGHUP`) so
  adding/removing a box restarts only the affected supervised ssh + listener.

### 5. Control/IPC protocol — `internal/control` (new) + `internal/proto`
- New listener on the control socket. Reuse `proto.WriteFrame`/`ReadFrame`.
- New message types in `internal/proto/messages.go`: `StatusSnapshot`,
  `Event` (status change / request), and client commands (`Subscribe`,
  `Reload`, `AddBox`, `RemoveBox`). Keep the agent↔host `Request`/`Response`/
  `TunnelHeader` types untouched.
- Client flow: connect → receive snapshot → stream events.

### 6. CLI surface — `cmd/tether`
- `tether status [--watch]`: connect to control socket, print per-box status +
  recent requests. **This is the primary Linux-side verification tool.**
- `tether box add <name> --ssh-host <alias> [--remote-port N]`,
  `tether box list`, `tether box rm <name>`: edit config + signal reload. Gives
  the GUI's management a testable CLI equivalent.
- `tether install` (host): install the daemon service only. **Remove the
  `RemoteForward` printing** — tether owns the forward now. This makes setup
  simpler than v1.
- Agent commands (`open`, `run`, `install-shim`, `source`) unchanged.

### 7. Drop Windows
Remove Windows branches and simplify to Linux+macOS at the exact locations the
explore map found:
- `internal/install/install.go` — `UnitPath` (~50-72), `renderUnitFor`
  (~115-165), `enable`/`disable` (~213-251), drop `.cmd`/`taskkill`/
  `joinWindowsArgs`/`quoteWindowsArg`.
- `internal/shim/shim.go` — drop `.cmd` rendering, PowerShell `SourceScript`
  branch, `LOCALAPPDATA` paths (~63-142).
- `internal/host/browser.go` — drop the `windows` case (~11-15).
- `cmd/tether/cmd_host.go` (~37-43), `cmd/tether/cmd_install.go` (~46-48),
  `cmd/tether/cmd_install_shim.go:16`, `internal/run/run.go:42`.
- Remove Windows skips / PowerShell mock branches in `*_test.go`
  (`install_test.go`, `shim_test.go`, `run_test.go`, `host_test.go`,
  `agent_test.go`).
- Update `README.md` and `.goreleaser.yaml` (drop windows targets).

### 8. Service install for the daemon
Existing systemd-user / launchd patterns in `internal/install/install.go` already
fit a long-lived daemon (`KeepAlive`/`Restart`). Keep those; just ensure the unit
starts `tether host` (which now reads config and supervises SSH). No Windows.

## Phase 2 — macOS GUI (deferred; not buildable on this Linux box)

Thin **Wails** app (Go backend shares this codebase; HTML/CSS window) — or Swift
if preferred later. It connects to the daemon's control socket:
- **Menubar:** aggregate status icon (all-green / degraded / down) + per-box list;
  notification/alert when a box goes down.
- **Window:** request history table (box, URL, time, outcome) + per-box status +
  add/remove box (writes config via the `AddBox`/`RemoveBox` control commands).
- Built and bundled (`.app`, signing) on the Mac. Spec'd here; implemented in a
  separate plan once Phase 1 lands.

## Out of scope (v1)
- Persisted history (in-memory only, per user).
- Non-SSH transports / relay server / Tailscale.
- Agent-side `tether doctor` and shim pre-flight visible-error (nice follow-up;
  the host GUI status is the primary fix for the silent-failure problem).
- Backward compatibility with the hand-edited `RemoteForward` mode.

## Verification (all on the Linux dev box)
- **Unit tests:** supervisor state machine with a fake `ssh` runner (connect,
  forward-failure exit, backoff, reconnect); config load/save/add/remove; control
  protocol round-trip; registry subscribe/notify + ring-buffer bounds.
- **Existing tests:** `go test ./...` — the untouched relay tests must still pass
  after Windows removal.
- **End-to-end on localhost:** add a box whose `ssh_host` is `localhost`
  (`tether box add local --ssh-host localhost`), run `tether host`, then in
  another shell `BROWSER=<shim> tether open 'http://localhost:PORT/...'` (or
  `tether run -- …`) with a fake browser; confirm `tether status --watch` shows
  the box `Connected` and the request appearing; kill the ssh and confirm status
  flips to `Disconnected` then reconnects.
- **Build:** `go build ./cmd/tether` and `go vet ./...` clean; confirm no
  remaining `runtime.GOOS == "windows"` references.

## Suggested implementation order
1. Windows removal + green `go test ./...` (clean base).
2. `internal/config` + `tether box` CLI.
3. `internal/supervisor` (+ fake-runner tests).
4. `internal/registry` + wire event hooks into `host.handleConn`/`tunnel`.
5. Per-box listeners + supervisor in `tether host`; config reload.
6. `internal/control` + `proto` messages + `tether status` CLI.
7. Update `tether install` (drop RemoteForward printing) + README/goreleaser.
8. End-to-end localhost verification.
9. (Phase 2, on Mac) Wails thin-client GUI.
