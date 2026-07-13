# tether

Open URLs from a headless machine in the browser on your GUI machine, over SSH.
Tether is built for OAuth/SSO flows like `aws sso login`, `gcloud auth login`,
and `gh auth login` when those commands run on a remote server but need a local
browser and a `localhost` callback.

Unlike a hand-rolled `ssh -R` + `xdg-open`, tether **owns and supervises the SSH
forward itself** — it reconnects after sleep or network changes, and shows you
per-box status so a broken tunnel is visible instead of silent.

Supported platforms: **macOS host + Linux agent**.

## Vocabulary

- **Host:** the GUI machine with your browser (a Mac). It runs `tether host`, a
  long-lived per-user daemon that keeps an SSH remote-forward alive to each
  configured box, opens the browser, and tracks status.
- **Agent:** a headless machine, VM, or SSH target where your CLI commands run.
  It invokes `tether open` through `$BROWSER`, `xdg-open`, or `tether run`.

```text
[ host: Mac, tether host daemon ]                 [ agent: headless SSH box ]

  supervisor ── ssh -N -R 9999:box.sock box ─────▶ sshd listens 127.0.0.1:9999
      │  keepalive · ExitOnForwardFailure                  ▲
      │  auto-reconnect · per-box status                   │ tether open <url>
  per-box unix socket ◀── tunneled callback ───────────────┘
      │  (yamux relay; browser callbacks tunnel back to the CLI)
      ▼
  registry (status + recent requests) ──▶ `tether status`  ·  macOS GUI (planned)
```

Because tether launches the `ssh` process, it reuses everything in your
`~/.ssh/config` — host aliases, keys, `ProxyJump`/bastions. You no longer edit a
`RemoteForward` line by hand or reconnect your interactive session.

Requirement: key-based (non-interactive) SSH to each box. Tether runs ssh with
`BatchMode=yes`, so it never hangs on a password prompt.

## Quick start

Install the `tether` binary on both machines.

### 1. On the host (Mac): install the daemon and add a box

```sh
tether install                                   # per-user service, starts now
tether box add my-agent --ssh-host my-agent      # my-agent is a ~/.ssh/config alias
tether reload                                    # apply without restarting
tether status                                    # ● my-agent  connected
```

`--ssh-host` is any `ssh` destination that already works from your Mac — an alias
from `~/.ssh/config` is ideal. Add `--remote-port N` if `9999` is taken on the
box.

### 2. On the agent: run an auth command

For one-off use, no agent install is needed:

```sh
tether run -- aws sso login
```

For daily use, install the agent shim once:

```sh
tether install-shim
eval "$(tether source)"
```

Then run your normal command:

```sh
aws sso login
gcloud auth login
gh auth login
```

On Linux agents, `install-shim` also installs an `xdg-open` shim in
`~/.local/bin` when possible. Keep `~/.local/bin` before system paths so CLIs
that ignore `$BROWSER` still use tether.

## Install

### Homebrew / Linuxbrew

```sh
brew install mwdomino/tap/tether
```

### Go install

```sh
go install github.com/mwdomino/tether/cmd/tether@latest
```

Requires Go 1.26+.

### Release archive

Download a release from <https://github.com/mwdomino/tether/releases>, extract,
and put `tether` on `PATH`:

```sh
curl -fsSL https://github.com/mwdomino/tether/releases/latest/download/tether_<VERSION>_linux_amd64.tar.gz | \
    tar -xz tether
sudo install -m 0755 tether /usr/local/bin/tether
```

## Commands

### Host side

| Command | What |
|---|---|
| `tether install` | Install and start the host daemon as a per-user service (systemd user unit on Linux, launchd LaunchAgent on macOS). |
| `tether host` | Run the daemon in the foreground (what the service runs). |
| `tether box add <name> --ssh-host <alias> [--remote-port N]` | Add a box to the config. |
| `tether box list` | List configured boxes. |
| `tether box rm <name>` | Remove a box. |
| `tether reload` | Tell the running daemon to re-read its config and reconcile boxes. |
| `tether status [--watch]` | Show each box's connection status and recent open requests. |
| `tether uninstall` | Stop and remove the service. |

### Agent side

| Command | What |
|---|---|
| `tether run -- <cmd>` | Run `<cmd>` with an ephemeral browser shim; no install needed. |
| `tether open <url>` | Send a URL to the host to open (used by the shims). |
| `tether install-shim` | Install the persistent `tether-open` / `xdg-open` shim. |
| `tether source` | Print shell exports (`$PATH`, `$BROWSER`) for the shim. |

## Configuration

The daemon reads `~/.config/tether/config.json` (or `$XDG_CONFIG_HOME/tether/`).
It is normally managed with `tether box …`, but is plain JSON:

```json
{
  "boxes": [
    { "name": "my-agent", "ssh_host": "my-agent", "remote_port": 9999 }
  ],
  "auth_token": ""
}
```

- `ssh_host` — any `ssh` destination (typically a `~/.ssh/config` alias).
- `remote_port` — the loopback port bound on the box (the port `tether open`
  dials). Defaults to `9999`.
- `auth_token` — optional shared secret; set the same value on the agent via
  `TETHER_AUTH_TOKEN`.

### Agent flags and environment

| Flag | Env | Default | What |
|---|---|---|---|
| `--server` | `TETHER_SERVER` | `127.0.0.1:9999` | Host port exposed on the box by the forward |
| `--auth-token` | `TETHER_AUTH_TOKEN` | unset | Shared secret if the host requires one |
| `--timeout` | `TETHER_TIMEOUT` | `5m` | Overall wait time for the callback |

## Why the shim backgrounds `tether open`

Some CLIs wait for `$BROWSER` to exit before they start their local HTTP callback
server. If `$BROWSER` were `tether open` directly, the CLI could block while
tether waits for a callback the CLI has not started serving yet. The shim runs
`tether open` in the background and returns `0` immediately; the background
process keeps the tunnel alive, relays the callback, and exits when done or
after `--timeout`.

## Debugging

The fastest check is on the host:

```sh
tether status          # is the box connected? any recent requests?
tether status --watch  # stream status changes and requests live
```

A box shown `disconnected` prints the ssh error (e.g. host key, auth, or
unreachable). Fix it and the supervisor reconnects automatically.

Agent-side logs (recommended shim) are at `~/.cache/tether/open.log`:

```sh
tail -f ~/.cache/tether/open.log
```

Host daemon logs are captured by the service manager:

```sh
# macOS
log stream --predicate 'process == "tether"' --info
# Linux
journalctl --user -u tether-host -f
```

## Roadmap

A macOS menubar + window GUI is planned as a thin client over the daemon's
control socket (the same data `tether status` shows): at-a-glance green/red
status, alerts when a box drops, and a live view of the URLs sent.

## Building from source

```sh
git clone https://github.com/mwdomino/tether
cd tether
go build -o tether ./cmd/tether
go test ./...
```

Releases are cut by tagging `v*`; goreleaser produces archives for
`linux/{amd64,arm64}` and `darwin/{amd64,arm64}` and publishes the Homebrew
formula to `mwdomino/homebrew-tap`.

## License

[Add a LICENSE file. Until then: all rights reserved.]
