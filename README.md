# tether

Open URLs from a headless machine in the browser on your GUI machine, over an
existing SSH connection. Tether is built for OAuth/SSO flows like `aws sso
login`, `gcloud auth login`, and `gh auth login` when those commands run on a
remote server but need a local browser and `localhost` callback.

## Vocabulary

Tether has two roles:

- **Host:** the GUI machine with your browser. It runs `tether host`, usually as
  a per-user service installed by `tether install`.
- **Agent:** a headless machine, VM, container host, or SSH target where your CLI
  commands run. It invokes `tether open` through `$BROWSER`, `xdg-open`, or
  `tether run`.

```text
[ host: GUI + browser ]                    [ agent: headless SSH target ]

   browser   <--- tunneled callback ----   CLI listening on localhost:PORT
      ^                                             ^
      |                                             |
   tether host  <------ SSH RemoteForward ----  tether open <url>
```

The SSH `RemoteForward` lets the agent reach the host's `tether host` listener.
When the host browser follows an OAuth redirect to `localhost:PORT`, tether
binds that callback port on the host and tunnels the bytes back to the CLI still
running on the agent.

## Quick start

Install the `tether` binary on both machines, then do this once per host/agent
pair.

### 1. On the host: install and start the host service

```sh
tether install
```

This installs a per-user service, starts `tether host`, and prints a
`RemoteForward` line.

### 2. On the host: add the SSH RemoteForward

Add the printed line under the SSH config entry for your agent:

```sshconfig
Host my-agent
  HostName 1.2.3.4
  User you
  RemoteForward 9999 /Users/you/.local/share/tether/tether.sock
```

Linux/macOS hosts normally forward to a Unix socket. Windows hosts use TCP:

```sshconfig
Host my-agent
  HostName 1.2.3.4
  User you
  RemoteForward 9999 127.0.0.1:9999
```

Reconnect to the agent after editing SSH config.

### 3. On the agent: run an auth command

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

Or with the repo's Brewfile:

```sh
brew bundle --file=./Brewfile
```

### Go install

```sh
go install github.com/mwdomino/tether/cmd/tether@latest
```

Requires Go 1.26+.

### Release archive

Download a release from <https://github.com/mwdomino/tether/releases>, extract
it, and put `tether` on `PATH`:

```sh
curl -fsSL https://github.com/mwdomino/tether/releases/latest/download/tether_<VERSION>_linux_amd64.tar.gz | \
    tar -xz tether
sudo install -m 0755 tether /usr/local/bin/tether
```

## Commands

### `tether install` — host setup

Run on the host. It writes and starts a per-user service:

- Linux: systemd user unit
- macOS: launchd LaunchAgent
- Windows: Startup folder command

Useful flags:

```sh
tether install --listen 127.0.0.1:7777   # TCP listener
tether install --socket /custom/path     # Unix socket on Linux/macOS
tether install --auth-token secret       # require agents to present a token
```

If you use `--auth-token`, configure the agent with the same value:

```sh
export TETHER_AUTH_TOKEN='secret'
```

### `tether run` — temporary agent shim

Run on the agent. It creates a private temp directory containing `tether-open`
and, on Linux, `xdg-open`; points the child process at those shims; runs your
command; then removes the temp directory.

```sh
tether run -- aws sso login
tether run --timeout 10m -- gh auth login
tether run --server 127.0.0.1:9999 -- gcloud auth login
```

Separate tether flags from the wrapped command with `--`.

### `tether install-shim` and `tether source` — persistent agent shim

Run on the agent for daily use:

```sh
tether install-shim
```

This writes `~/.local/bin/tether-open`, creates the log directory, and on Linux
also writes `~/.local/bin/xdg-open` when that path is free or already points at
tether.

Configure your shell:

```sh
eval "$(tether source)"
```

Add the printed exports to your shell profile if you want them to persist.

Shim flags:

| Flag | Default | What |
|---|---|---|
| `--bin-dir` | `~/.local/bin` | Where `tether-open` is written |
| `--log-dir` | `~/.cache/tether` | Where shim logs are written |
| `--xdg-open` | `true` on Linux | Also install `xdg-open` in `--bin-dir` |
| `--force-xdg-open` | `false` | Replace an existing `--bin-dir/xdg-open` |

## Why the shim backgrounds `tether open`

Some CLIs wait for `$BROWSER` to exit before they start or resume their local
HTTP callback server. If `$BROWSER` were `tether open` directly, the CLI could
block forever while tether waits for a callback the CLI has not accepted yet.

The shim starts `tether open` in the background and returns `0` immediately. The
background agent keeps the SSH tunnel alive, relays the callback, and exits when
the flow completes or after `--timeout`.

## Configuration reference

### Host flags

| Flag | Default | What |
|---|---|---|
| `--listen` | `127.0.0.1:9999` on Windows | TCP host:port to listen on |
| `--socket` | `$XDG_RUNTIME_DIR/tether.sock` or `~/.local/share/tether/tether.sock` on Linux/macOS | Unix socket path |
| `--browser` | OS default | Custom browser launch argv |
| `--auth-token` | unset | Shared secret required from agents |

### Agent flags and environment

| Flag | Env | Default | What |
|---|---|---|---|
| `--server` | `TETHER_SERVER` | `127.0.0.1:9999` | TCP target exposed by SSH |
| `--socket` | `TETHER_SOCKET` | unset | Unix socket target; overrides `--server` |
| `--auth-token` | `TETHER_AUTH_TOKEN` | unset | Shared secret if host requires one |
| `--timeout` | `TETHER_TIMEOUT` | `5m` | Overall wait time |

`tether run` accepts these same flags before `--` and forwards them to
background `tether open` processes through environment variables.

## Debugging

Agent logs are at `~/.cache/tether/open.log` when you use the recommended shim:

```sh
tail -f ~/.cache/tether/open.log
```

Host logs are captured by the service manager:

```sh
# macOS
log stream --predicate 'process == "tether"' --info

# Linux
journalctl --user -u tether-host -f

# Windows
# Event Viewer -> Windows Logs -> Application
```

Common failures:

- `failed to connect to host on ... is RemoteForward set up?` means the SSH
  forward is not active in the current SSH session. Reconnect with `ssh -v` to
  verify the forward.
- `port <N> already in use on host` means another OAuth flow is mid-auth, or a
  service on the host already uses that callback port.
- Browser waits forever on `127.0.0.1` or `[::1]`: check the agent log for
  `tunnel substream received`, `local dial succeeded`, and `tunnel pipe ended`.
  `bytes_from_agent: 0` means the CLI received the callback but did not respond.

## Building from source

```sh
git clone https://github.com/mwdomino/tether
cd tether
go build -o tether ./cmd/tether
go test ./...
```

Releases are cut by tagging `v*`; goreleaser produces archives for
`linux/{amd64,arm64}`, `darwin/{amd64,arm64}`, and `windows/{amd64,arm64}` and
publishes the Homebrew formula to `mwdomino/homebrew-tap`.

## License

[Add a LICENSE file. Until then: all rights reserved.]
