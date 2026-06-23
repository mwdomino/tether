# Tether — design

## Overview

Tether lets a headless server delegate "open this URL in a browser" to a
desktop machine over an existing SSH connection. Unlike
[lemonade](https://github.com/lemonade-command/lemonade), it transparently
solves the OAuth/SSO localhost-callback case: when a tool on the headless
side starts an HTTP listener on `localhost:PORT` and expects a redirect
back to it, tether tunnels the callback over the same SSH-forwarded
channel so the SSO flow completes correctly.

## Goals

- Open URLs requested on a headless server in a browser on a desktop
  machine the user is sitting at, over an existing SSH session.
- Support SSO / OAuth flows that use `localhost` / `127.0.0.1` /
  `[::1]` callbacks — the desktop browser's hit to that loopback URL
  must reach the original tool on the headless server.
- Cross-platform desktop side: Linux, macOS, Windows (amd64 + arm64).
- Single Go binary released via goreleaser.

## Non-goals (v1)

- Clipboard copy/paste (lemonade-style). The protocol leaves room for
  it; the v1 binary does not ship it.
- File-open (remote `xdg-open /path/to.pdf` opening on the desktop).
- URL allow-listing.
- Code signing / macOS notarization.
- Any non-SSH transport (raw TCP across networks, mTLS, Tailscale-aware
  modes, etc.).

## Trust model

SSH is the only network surface. The host daemon listens on:

- A unix socket at `$XDG_RUNTIME_DIR/tether.sock` (mode `0600`) on
  Linux/macOS, falling back to `~/.local/share/tether/tether.sock`
  when `XDG_RUNTIME_DIR` is unset (notably common on macOS).
- `127.0.0.1:9999` on Windows (no usable unix-socket parity for SSH
  `RemoteForward` on Windows OpenSSH at time of writing).

The headless server reaches the daemon **solely** via a `RemoteForward`
in the user's SSH config. Tether does not open any port reachable from
the network, and does not add an auth layer of its own by default. A
shared `--auth-token` flag exists as an opt-in escape hatch.

## Architecture

One Go binary, two subcommands:

- **`tether host`** — long-lived daemon on the desktop. Started by the
  user's service manager (systemd user unit / launchd agent / Windows
  service). Owns the local listener, dispatches incoming requests, and
  shells out to the OS default URL handler (`open` on macOS,
  `xdg-open` on Linux, `rundll32 url.dll,FileProtocolHandler` on
  Windows) to launch URLs.
- **`tether open <url>`** — short-lived agent on the headless server.
  Invoked per-call as `$BROWSER` (typically via a small wrapper script
  or symlink that the user installs as `xdg-open` in their `PATH`).
  Connects to the SSH-forwarded port, sends one open-URL request, then
  either exits immediately or stays alive long enough to relay an SSO
  callback.

### Wiring

The user adds one line per relevant host to `~/.ssh/config` on the
desktop:

```
Host headless-box
  RemoteForward 9999 /home/<user>/.local/share/tether/tether.sock
```

(On Windows, `RemoteForward 9999 127.0.0.1:9999`.)

This exposes the desktop's local listener inside the headless box on
port 9999. The agent connects to `127.0.0.1:9999` on the headless side.

## Wire protocol

Each agent invocation opens **one** TCP connection (over the SSH
forward) to the host. The connection is treated as a multiplexed
channel using
[hashicorp/yamux](https://github.com/hashicorp/yamux). The agent is the
yamux client; the host is the yamux server.

### Control stream

The agent opens the first substream as a control channel. One
request/one response, each a length-prefixed (uint32 big-endian) JSON
object.

Request:

```json
{
  "url": "https://accounts.google.com/...?redirect_uri=http%3A%2F%2Flocalhost%3A8085%2Fcallback",
  "loopback_ports": [8085],
  "auth_token": "<optional>"
}
```

`loopback_ports` is the deduplicated list of distinct ports the agent
parsed out of the URL. The agent does a single pass of URL-decoding on
the whole URL string and then scans the decoded string (case-insensitive)
for occurrences of `localhost`, `127.0.0.1`, or `[::1]` followed by
`:<port>`. This catches `redirect_uri=...`, `return_to=...`, custom
param names, and hosts inside the fragment, without depending on
specific param names.

Response:

```json
{ "ok": true }
```

or

```json
{ "ok": false, "error": "port 8085 already in use on desktop" }
```

The host attempts to bind each loopback port on `127.0.0.1` on the
desktop **before** responding `ok: true`. If any bind fails the host
releases the ones it already bound, returns `ok: false`, and the agent
exits non-zero.

After `ok: true` the host launches the browser. If the launch fails
the host returns `ok: false` with `error: "browser launch failed: ..."`.

### Tunnel substreams

For each port the host bound, it listens on `127.0.0.1:PORT` on the
desktop. Each incoming TCP connection to that listener triggers the
host to open a new yamux substream toward the agent, prefixed with a
length-prefixed JSON header:

```json
{ "kind": "tunnel", "port": 8085 }
```

After the header, the substream carries raw bytes bidirectionally. The
agent reads the header, dials `127.0.0.1:PORT` on the headless side
(where the original SSO-flow tool is listening), and copies bytes in
both directions until either side closes. Closing either direction
half-closes the corresponding side of the dialed TCP connection.

### Lifecycle

- If `loopback_ports` is empty: agent exits as soon as the control
  response arrives and the browser has been launched.
- If `loopback_ports` is non-empty: agent stays alive while the control
  stream is open. The host keeps the control stream open while any of
  its desktop listeners are still bound. The host releases a listener
  (and tears it down) 10 seconds after the last tunnel TCP connection
  on that port closes — grace period for follow-on requests (favicons,
  redirects to a "you can close this tab" page, etc.). When all
  listeners are released, the host closes the control stream and the
  agent exits 0.
- Agent enforces an overall `--timeout` (default 5 minutes). When it
  fires, the agent closes the yamux session (which releases the host's
  listeners) and exits non-zero.

## Configuration

CLI flags / env vars:

### `tether host`

| Flag             | Default                                                  | Notes                                            |
|------------------|----------------------------------------------------------|--------------------------------------------------|
| `--socket`       | `$XDG_RUNTIME_DIR/tether.sock` (Linux/macOS)             | Listen on a unix socket.                         |
| `--listen`       | `127.0.0.1:9999` (Windows default)                       | Listen on loopback TCP. Mutually exclusive with `--socket`. |
| `--browser`      | OS default (`open` / `xdg-open` / `rundll32 url.dll,FileProtocolHandler`) | Override the open command. URL passed as last arg. |
| `--auth-token`   | unset                                                    | Optional shared secret.                          |
| `--log-level`    | `info`                                                   |                                                  |

### `tether open`

| Flag             | Default                                                  | Notes                                            |
|------------------|----------------------------------------------------------|--------------------------------------------------|
| `--server`       | `127.0.0.1:9999`                                         | Target host:port (the SSH-forwarded port).       |
| `--socket`       | unset                                                    | Connect to a unix socket instead (rare on headless side). |
| `--timeout`      | `5m`                                                     | Overall timeout including any loopback wait.     |
| `--auth-token`   | unset                                                    | Must match host.                                 |

Env vars mirror the flags: `TETHER_SERVER`, `TETHER_SOCKET`,
`TETHER_AUTH_TOKEN`, `TETHER_TIMEOUT`.

### Install patterns (documented, not automated)

On the headless box, the user installs a small **backgrounding** shim and
points `$BROWSER` (or symlinks `xdg-open`) at it. The shim must exit
immediately so the calling tool's `webbrowser.open()` returns — otherwise
tools that use Python's `webbrowser` module (notably AWS CLI's
`aws sso login`) deadlock: `webbrowser.open_new_tab` calls `p.wait()` on
`$BROWSER` and blocks the calling tool's main thread until that process
exits, but the tether agent intentionally stays alive to relay the OAuth
callback that the calling tool is waiting for.

**Linux / macOS (Bash):**

```bash
#!/usr/bin/env bash
mkdir -p "$HOME/.cache/tether"
nohup tether open "$@" >>"$HOME/.cache/tether/open.log" 2>&1 &
exit 0
```

Save as `~/.local/bin/tether-open`, `chmod +x`, then:

```bash
export BROWSER="$HOME/.local/bin/tether-open"
# Optional: also handle xdg-open consumers
ln -sf "$HOME/.local/bin/tether-open" ~/.local/bin/xdg-open
```

`nohup … &` detaches the agent from the terminal session and the calling
tool's process group; stdout/stderr go to the log file so they don't
pollute the calling tool's terminal. `exit 0` returns immediately so
`webbrowser.open()` unblocks.

**Windows (PowerShell):**

```powershell
# tether-open.ps1
$logDir = "$env:LOCALAPPDATA\tether"
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
Start-Process -FilePath "tether" -ArgumentList (@("open") + $args) `
    -WindowStyle Hidden `
    -RedirectStandardOutput "$logDir\open.log" `
    -RedirectStandardError "$logDir\open.err.log"
exit 0
```

`Start-Process` without `-Wait` returns immediately and the agent runs
detached. Point `BROWSER` at this script (or wrap as a `.cmd` that calls
`powershell -ExecutionPolicy Bypass -File tether-open.ps1 %*`).

Tether does not modify `/usr/local/bin/xdg-open` or any system PATH
entries automatically.

## Error handling

| Condition                                | Behavior                                                                 | Exit |
|------------------------------------------|--------------------------------------------------------------------------|------|
| Loopback port already bound on desktop   | Host returns clear error; agent prints it; no browser opened.            | 2    |
| Host unreachable from agent              | Agent prints `failed to connect to host on ... — is RemoteForward set up?` | 3  |
| Auth token mismatch                      | Host logs + disconnects; agent prints + exits.                           | 4    |
| Timeout waiting for callback             | Agent prints; host listeners released.                                   | 5    |
| Browser launch failed on host            | Surfaced in control response; agent prints.                              | 6    |
| Successful, no loopback ports            | Agent exits immediately after `ok:true`.                                 | 0    |
| Successful, loopback ports drained       | Agent exits after last listener released.                                | 0    |

Logs: host writes structured logs to stderr (captured by
systemd/launchd/Event Viewer); agent writes to stderr.

## Build & release

- Single Go module (Go 1.26). Layout:
  - `cmd/tether/main.go` — entrypoint, wires cobra subcommands.
  - `internal/host/` — host daemon implementation.
  - `internal/agent/` — agent implementation.
  - `internal/proto/` — JSON message types, length-prefixed framing,
    URL parsing.
- `cobra` for subcommands, `hashicorp/yamux` for multiplexing,
  `log/slog` for logging.
- Goreleaser config produces archives for: `linux/{amd64,arm64}`,
  `darwin/{amd64,arm64}`, `windows/{amd64,arm64}`. Checksums + GitHub
  release on tag.
- CI: GitHub Actions. On push/PR: `go build ./...`, `go vet ./...`,
  `go test ./...`. On tag: goreleaser.
- No code signing / notarization in v1; document the macOS Gatekeeper
  warning in the README.

## Testing

- **Unit tests:** URL parsing across many shapes (`redirect_uri`,
  custom param names, fragments, IPv6 `[::1]`, percent-encoded
  `127.0.0.1`, mixed-case `LOCALHOST`); control-message framing;
  yamux session error paths.
- **Integration tests:** in-process host + agent connected via a temp
  unix socket. Exercises:
  - Simple URL open: mock browser command (just record argv); assert
    the host invoked it with the right URL.
  - Loopback round-trip: agent listens on a real loopback port on the
    test machine playing the role of "the SSO tool"; host's bound
    desktop port accepts a synthetic HTTP request; assert bytes
    arrive at the agent-side listener unchanged and the response
    flows back.
  - Port-collision error path.
- No real SSH in tests — we test directly against the socket
  transport, which is exactly what SSH delivers to us at runtime.

## Open questions deferred

- Concrete service-manager unit files (systemd user, launchd plist,
  Windows service registration) — to be designed during
  implementation.
- README/install docs — written alongside v1.
