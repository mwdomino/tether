# tether

Open URLs requested on a headless server in a browser on another machine, over
an existing SSH connection. Tether solves the OAuth/SSO localhost-callback
problem that breaks tools like `aws sso login`, `gcloud auth login`, and
`gh auth login` when you run them on a remote box.

## Two-Machine Model

Tether always has two sides:

- **Browser box:** the laptop, desktop, or workstation with a browser
  installed. This side runs `tether host`.
- **Headless box:** the remote server, VM, or container host where CLIs run.
  This side needs `$BROWSER` (and, on Linux, `xdg-open`) pointed at a shim that
  starts `tether open <url>` in the background. You get that shim one of two
  ways: wrap a single command with `tether run` (nothing installed), or install
  a persistent shim with `tether install-shim`.

The browser box exposes its local `tether host` listener to the headless box
with an SSH `RemoteForward`. When an OAuth flow redirects to `localhost:PORT`,
the browser-box callback is tunneled back to the tool still running on the
headless box.

```text
[ browser box ]                              [ headless box ]
                                                  $BROWSER
   browser   <--- tunnel(SSO callback) -->  tether open <url>
      ^                                          |
      |                                          |
   tether host  <------ SSH RemoteForward -------+
```

## Install

Install the `tether` binary on both machines.

### Via Homebrew (macOS + Linuxbrew)

The repo ships a `Brewfile` you can use with `brew bundle`:

```sh
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
[releases page](https://github.com/mwdomino/tether/releases), extract it, and
put `tether` somewhere on `PATH`:

```sh
curl -fsSL https://github.com/mwdomino/tether/releases/latest/download/tether_<VERSION>_linux_amd64.tar.gz | \
    tar -xz tether
sudo install -m 0755 tether /usr/local/bin/tether
```

## Setup

### 1. Browser box: install the host service

Run this on the machine with the browser:

```sh
tether install
```

This writes a per-user service unit (systemd user / launchd LaunchAgent /
Windows Startup folder) and starts `tether host`. No root/admin required.
It also prints the `RemoteForward` line to add to SSH config.

To use a non-default host address:

```sh
tether install --listen 127.0.0.1:7777   # TCP
tether install --socket /custom/path     # unix socket (Linux/macOS)
```

### 2. Browser box: add the SSH RemoteForward

In `~/.ssh/config` on the browser box, add the `RemoteForward` printed by
`tether install` under the headless box you SSH into:

```sshconfig
Host headless-box
  HostName 1.2.3.4
  User you
  RemoteForward 9999 /Users/<you>/.local/share/tether/tether.sock
```

On Linux/macOS browser boxes, the right side is the `tether host` Unix socket.
If `$XDG_RUNTIME_DIR` is set, the default is usually
`$XDG_RUNTIME_DIR/tether.sock`; otherwise it is
`~/.local/share/tether/tether.sock`. On Windows browser boxes, `tether install`
prints a TCP target:

```sshconfig
Host headless-box
  HostName 1.2.3.4
  User you
  RemoteForward 9999 127.0.0.1:9999
```

Reconnect to the headless box after changing SSH config so the forward is
active in that SSH session.

### 3. Headless box: get the browser shim

The headless box needs `$BROWSER` (and, on Linux, `xdg-open`) pointed at a shim
that backgrounds `tether open`. Pick one of two options.

#### Option A — Shimless: `tether run` (nothing installed)

Wrap the command you want to authenticate:

```sh
tether run -- aws sso login
```

`tether run` creates the shim in a private temp directory, points `$BROWSER`
(and `xdg-open` on Linux) at it for that command only, runs the command, and
removes the temp directory when it exits. Nothing is left on the box, and it
exits with the wrapped command's exit code. Separate tether's own flags from the
wrapped command with `--`:

```sh
tether run --timeout 10m -- gh auth login
tether run --server 127.0.0.1:9999 -- gcloud auth login
```

This is the simplest path and ideal for one-off logins or locked-down boxes. The
host service and SSH `RemoteForward` (steps 1–2) still have to be set up on the
browser box.

#### Option B — Persistent shim: `install-shim` + `source`

For daily use, install the shim once:

```sh
tether install-shim
```

`tether install-shim` is idempotent. It writes `~/.local/bin/tether-open`,
creates the log directory, and on Linux also installs `~/.local/bin/xdg-open`
as a shim when that path is free or already points at `tether-open`.

Configure the current shell:

```sh
eval "$(tether source)"
```

Then add the same exports to your shell profile. To print them without
installing anything:

```sh
tether source
```

`tether source` prints shell code like:

```sh
export PATH='/home/you/.local/bin':"$PATH"
export BROWSER='/home/you/.local/bin/tether-open'
```

Setting `$BROWSER` covers tools that honor it (`aws`, `gh`, `gcloud`,
Python's `webbrowser`, etc.). Some Go-based CLIs, notably `argocd`, some
`kubectl` plugins, and various HashiCorp tools, ignore `$BROWSER` on Linux
and shell out directly to `xdg-open`. The `xdg-open` shim makes those work
too. Make sure `~/.local/bin` is on `PATH` ahead of any system `xdg-open`.

If `~/.local/bin/xdg-open` already exists and is not tether's shim,
`install-shim` leaves it alone. Use `--xdg-open=false` to skip it or
`--force-xdg-open` to replace it.

Both options use the same backgrounding shim, so the behavior described in
[Why the Shim Backgrounds `tether open`](#why-the-shim-backgrounds-tether-open)
applies to either path.

## Why the Shim Backgrounds `tether open`

When a tool like AWS CLI calls Python's `webbrowser.open_new_tab(url)`, the
module launches `$BROWSER` and then calls `p.wait()` on the subprocess. If
`$BROWSER` is `tether open` directly, the agent stays alive holding the SSH
tunnel open to relay the OAuth callback, but `p.wait()` does not return until
the agent exits. The calling tool's main thread is blocked and never reaches
its own HTTP listener to accept the callback.

The shim detaches the agent so it returns `0` immediately. The agent runs in
the background, relays the callback, and exits naturally when the OAuth flow
completes, or after `--timeout` (default 5 minutes).

## Customization

### Host flags (browser box)

Set these on the browser box. `tether install` persists `--listen`,
`--socket`, and `--auth-token` into the service unit; `--browser` is available
when running `tether host` directly.

| Flag | Default | What |
|---|---|---|
| `--listen` | `127.0.0.1:9999` (Windows) | TCP host:port to listen on |
| `--socket` | `$XDG_RUNTIME_DIR/tether.sock` (Linux/macOS) | Unix socket path |
| `--browser` | OS default | Custom open command |
| `--auth-token` | unset | Require this token from agents |

### Agent flags (headless box)

Set these on the headless box, for example inside the shim:

| Flag | Env | Default | What |
|---|---|---|---|
| `--server` | `TETHER_SERVER` | `127.0.0.1:9999` | TCP target exposed by SSH |
| `--socket` | `TETHER_SOCKET` | unset | Unix socket target (overrides `--server`) |
| `--auth-token` | `TETHER_AUTH_TOKEN` | unset | Shared secret if host requires |
| `--timeout` | `TETHER_TIMEOUT` | `5m` | Overall wait time |

`tether run` accepts these same flags (place them before `--`) and forwards them
to the backgrounded `tether open` invocations via the environment.

### Shim flags (headless box)

| Flag | Default | What |
|---|---|---|
| `--bin-dir` | `~/.local/bin` | Where `tether-open` is written |
| `--log-dir` | `~/.cache/tether` | Where shim logs are written |
| `--xdg-open` | `true` on Linux | Also install `xdg-open` in `--bin-dir` |
| `--force-xdg-open` | `false` | Replace an existing `--bin-dir/xdg-open` |

## Debugging

**Agent logs** are at `~/.cache/tether/open.log` if you used the recommended
shim. `tail -f` it while reproducing.

**Host logs** are wherever your service manager captures them:

```sh
# macOS
log stream --predicate 'process == "tether"' --info

# Linux
journalctl --user -u tether-host -f

# Windows: Event Viewer -> Windows Logs -> Application
```

Common failure modes:

- `failed to connect to host on ... is RemoteForward set up?` means the SSH
  forward is not active in your current session. Reconnect with `ssh -v` to
  verify.
- `port <N> already in use on browser box` means another OAuth flow is mid-auth,
  or a real service is already using that port on the browser box.
- Browser shows "still waiting on 127.0.0.1" indefinitely: check
  `tail -f ~/.cache/tether/open.log` for `tunnel substream received`,
  `local dial succeeded`, and `tunnel pipe ended` lines. `bytes_from_local: 0`
  means the local tool received the callback but did not respond.

## Building from Source

```sh
git clone https://github.com/mwdomino/tether
cd tether
go build -o tether ./cmd/tether
go test ./...
```

Releases are cut by tagging `v*`; goreleaser produces archives for
`linux/{amd64,arm64}`, `darwin/{amd64,arm64}`, `windows/{amd64,arm64}` and
publishes the Homebrew formula to `mwdomino/homebrew-tap`.

## License

[Add a LICENSE file. Until then: all rights reserved.]
