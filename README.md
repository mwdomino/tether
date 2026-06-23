# tether

Open URLs requested on a headless server in a browser on your desktop, over
an existing SSH connection. Solves the OAuth/SSO localhost-callback problem
that breaks tools like `aws sso login`, `gcloud auth login`, and
`gh auth login` when you run them on a remote box.

## What it does

`tether host` runs on the desktop machine you're sitting at. It listens on a
unix socket (Linux/macOS) or `127.0.0.1:9999` (Windows), exposed to your
headless server via an SSH `RemoteForward`.

`tether open <url>` runs on the headless server, invoked as `$BROWSER` by
whatever tool wants to pop a tab. It connects to the host through the SSH
forward, tells the host to open the URL, and — crucially — tunnels any
OAuth `localhost:PORT` callbacks back to the headless side so the
originating tool's local listener can receive them.

```
[ Mac / Win ]                              [ Linux headless box ]
                                                   $BROWSER
   browser   <─tunnel(SSO callback)──>  tether open <url>
      ▲                                          │
      │                                          │
   tether host  <───── SSH RemoteForward ────────┘
```

## Install

### Via Homebrew (macOS + Linuxbrew)

The repo ships a `Brewfile` you can use with `brew bundle`:

```sh
# from the repo root, or anywhere you've copied the Brewfile
brew bundle --file=./Brewfile
```

Equivalent one-liner without the Brewfile:

```sh
brew install mwdomino/tap/tether
```

Both pull from the `mwdomino/homebrew-tap` tap, which is published
automatically by goreleaser on every tagged release.

### Via `go install`

```sh
go install github.com/mwdomino/tether/cmd/tether@latest
```

Requires Go 1.26+.

### From a release archive

Grab the tarball/zip for your platform from the
[releases page](https://github.com/mwdomino/tether/releases),
extract, and put `tether` somewhere on `PATH`:

```sh
curl -fsSL https://github.com/mwdomino/tether/releases/latest/download/tether_<VERSION>_linux_amd64.tar.gz | \
    tar -xz tether
sudo install -m 0755 tether /usr/local/bin/tether
```

## Setup

### 1. Desktop (Mac / Linux / Windows): install the host service

```sh
tether install
```

Drops a per-user service unit (systemd user / launchd LaunchAgent / Windows
Startup folder) and starts the daemon. No root/admin required.

To use a non-default address:

```sh
tether install --listen 127.0.0.1:7777   # TCP
tether install --socket /custom/path     # unix socket (Linux/macOS)
```

### 2. Desktop: add a `RemoteForward` to your SSH config

In `~/.ssh/config` on the desktop:

```
Host headless-box
  HostName 1.2.3.4
  User you
  RemoteForward 9999 /Users/<you>/.local/share/tether/tether.sock
```

On Windows desktops replace the socket path with `127.0.0.1:9999`.

### 3. Headless box: install the agent shim

The agent must be backgrounded by the shim so calling tools (e.g. Python's
`webbrowser.open_new_tab`) don't block waiting for it to exit — see
[Why the backgrounding shim is required](#why-the-backgrounding-shim-is-required).

**Linux / macOS:**

```bash
cat > ~/.local/bin/tether-open <<'EOF'
#!/usr/bin/env bash
mkdir -p "$HOME/.cache/tether"
nohup tether open "$@" >>"$HOME/.cache/tether/open.log" 2>&1 &
exit 0
EOF
chmod +x ~/.local/bin/tether-open
```

Then in `~/.bashrc` / `~/.zshrc`:

```sh
export BROWSER="$HOME/.local/bin/tether-open"
```

Also intercept `xdg-open`:

```sh
ln -sf "$HOME/.local/bin/tether-open" ~/.local/bin/xdg-open
```

Setting `$BROWSER` covers tools that honor it (`aws`, `gh`, `gcloud`,
Python's `webbrowser`, etc.). But several Go-based CLIs — notably
`argocd`, some `kubectl` plugins, and various HashiCorp tools — use
libraries like `github.com/pkg/browser` that ignore `$BROWSER` on Linux
and shell out directly to `xdg-open`. The symlink above makes those work
too. Make sure `~/.local/bin` is on your `PATH` ahead of any system
`xdg-open` (from `xdg-utils`) so the shim wins.

**Windows (PowerShell, less common as the headless side):**

```powershell
# %USERPROFILE%\AppData\Local\tether\tether-open.ps1
$logDir = "$env:LOCALAPPDATA\tether"
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
Start-Process -FilePath "tether" -ArgumentList (@("open") + $args) `
    -WindowStyle Hidden `
    -RedirectStandardOutput "$logDir\open.log" `
    -RedirectStandardError "$logDir\open.err.log"
exit 0
```

Point `BROWSER` at this script (or wrap as a `.cmd` that calls
`powershell -ExecutionPolicy Bypass -File tether-open.ps1 %*`).

## Why the backgrounding shim is required

When a tool like AWS CLI calls Python's `webbrowser.open_new_tab(url)`,
the module launches `$BROWSER` and then calls `p.wait()` on the
subprocess. If `$BROWSER` is `tether open` directly, the agent stays
alive holding the SSH tunnel open to relay the OAuth callback — but
`p.wait()` doesn't return until the agent exits, so the calling tool's
main thread is blocked and never reaches its own HTTP listener to accept
the callback. **Deadlock.**

`nohup … &` (or PowerShell's `Start-Process`) detaches the agent so the
shim returns `0` immediately. The agent runs in the background, relays
the callback, and exits naturally when the OAuth flow completes (or
after `--timeout`, default 5 minutes; override per invocation with
`tether open --timeout 10m …`).

## Customization

### Agent flags (set on the headless side, e.g. inside the shim)

| Flag | Env | Default | What |
|---|---|---|---|
| `--server` | `TETHER_SERVER` | `127.0.0.1:9999` | TCP target |
| `--socket` | `TETHER_SOCKET` | unset | Unix socket target (overrides `--server`) |
| `--auth-token` | `TETHER_AUTH_TOKEN` | unset | Shared secret if host requires |
| `--timeout` | — | `5m` | Overall wait time |

### Host flags (set at `tether install` time)

| Flag | Default | What |
|---|---|---|
| `--listen` | `127.0.0.1:9999` (Windows) | TCP host:port to listen on |
| `--socket` | `$XDG_RUNTIME_DIR/tether.sock` (Linux/macOS) | Unix socket path |
| `--browser` | OS default | Custom open command |
| `--auth-token` | unset | Require this token from agents |

## Debugging

**Agent logs** are at `~/.cache/tether/open.log` if you used the recommended
shim. `tail -f` it while reproducing.

**Host logs** are wherever your service manager captures them:

```sh
# macOS
log stream --predicate 'process == "tether"' --info

# Linux
journalctl --user -u tether-host -f

# Windows: Event Viewer → Windows Logs → Application
```

Common failure modes:

- `failed to connect to host on … — is RemoteForward set up?` — the SSH
  forward isn't active in your current session. Reconnect with `ssh -v`
  to verify.
- `port <N> already in use on desktop` — another OAuth flow is mid-auth,
  or you have a real service on that port on your desktop.
- Browser shows "still waiting on 127.0.0.1" indefinitely — check
  `tail -f ~/.cache/tether/open.log` for `tunnel substream received`,
  `local dial succeeded`, and `tunnel pipe ended` lines.
  `bytes_from_local: 0` means the local tool received the callback but
  didn't respond — usually a problem with that tool, not tether (verify
  by hitting the local port with `curl` directly).

## Building from source

```sh
git clone https://github.com/mwdomino/tether
cd tether
go build -o tether ./cmd/tether
go test ./...
```

Releases are cut by tagging `v*`; goreleaser produces archives for
`linux/{amd64,arm64}`, `darwin/{amd64,arm64}`, `windows/{amd64,arm64}`
and publishes the Homebrew formula to `mwdomino/homebrew-tap`.

## License

[Add a LICENSE file. Until then: all rights reserved.]
