# tether

Tether opens a browser on your Mac for a command running on a remote Linux machine. It also relays `localhost` callbacks to the command. This is useful for logins such as `aws sso login`, `gcloud auth login`, and `gh auth login` over SSH.

The Mac runs a host daemon that keeps an SSH remote forward open to each configured machine. The remote machine runs the `tether` CLI as an agent. The daemon reconnects the forward after a connection drops and shows its state with `tether status`.

Tether needs key-based SSH access from the Mac to the remote machine. It runs SSH with `BatchMode=yes`, so password prompts do not work. The documented setup is a macOS host and a Linux agent. The CLI host service also supports Linux. The status app is macOS-only.

## Quick start

Install the `tether` CLI on both machines. See [Install](#install) for the available packages.

On the Mac, install the host service and add an SSH target:

```sh
tether install
tether box add my-agent --ssh-host my-agent
tether reload
tether status
```

Use an SSH destination that already works from the Mac. For example, `my-agent` can be an alias in `~/.ssh/config`. If port `9999` is in use on the agent, add `--remote-port N` to `tether box add`.

On the agent, run a login command without installing a browser shim:

```sh
tether run -- aws sso login
```

`tether run` sets `$BROWSER` for the command and provides a temporary `xdg-open` shim on Linux. The agent still needs the `tether` binary.

For commands you run regularly, install a persistent shim on the agent:

```sh
tether install-shim
eval "$(tether source)"
aws sso login
```

Add `eval "$(tether source)"` to your shell startup file if you want it in new shells. On Linux, `install-shim` also adds an `xdg-open` shim to `~/.local/bin` unless that path already has a conflicting file. Keep that directory first in `$PATH` for commands that ignore `$BROWSER`.

## Install

Install the CLI with Homebrew on macOS or Linux:

```sh
brew install mwdomino/tap/tether
```

Or build the CLI with Go 1.26.2 or newer:

```sh
go install github.com/mwdomino/tether/cmd/tether@latest
```

For the macOS menu bar app, use the cask instead of the formula:

```sh
brew install --cask mwdomino/tap/tether
```

The cask includes the CLI and places `Tether.app` in `/Applications`. Its installer attempts to start the host service. If that step fails, run `tether install`. Launch the app once to enable its login item. You can turn off the app login item with `Start at login` in its menu. The app shows connection state and recent requests. The app release is for Apple Silicon. The CLI also has an Intel Mac build. The app release workflow signs and notarizes the bundle.

You can also download a CLI archive and `checksums.txt` from [GitHub Releases](https://github.com/mwdomino/tether/releases). Choose the archive for your OS and CPU. Compare its SHA-256 digest with `checksums.txt`. Put the extracted `tether` binary on your `$PATH`.

## How it works

```text
Mac (host)                                      Remote machine (agent)

tether host ── SSH remote forward ────────────▶ 127.0.0.1:9999
   │                                                  ▲
   │ opens the URL in a local browser                  │ tether open <url>
   └── browser localhost callback ── tunnel ──────────┘
```

Tether starts a separate SSH process for each box. SSH uses your existing `~/.ssh/config`, including keys and jump hosts. You do not need to add a `RemoteForward` entry or keep an interactive SSH session open.

For a URL that contains an explicit `localhost`, `127.0.0.1`, or `[::1]` port, tether binds that port on the Mac and sends browser connections back to the agent. If the port is already in use on the Mac, the request fails. Tether waits up to five minutes for the callback by default.

Some commands wait for `$BROWSER` to exit before they start their callback server. The shim starts `tether open` in the background and returns immediately. The background process stays alive for the callback or until its timeout.

## Commands

| Command | Where | Purpose |
|---|---|---|
| `tether install` | Host | Install and start the per-user service. |
| `tether host` | Host | Run the daemon in the foreground. |
| `tether box add <name> --ssh-host <alias> [--remote-port N]` | Host | Add an SSH target. |
| `tether box list` / `tether box rm <name>` | Host | List or remove targets. |
| `tether reload` | Host | Apply changes to the box configuration. |
| `tether status [--watch]` | Host | Show connections and recent requests. |
| `tether uninstall` | Host | Stop and remove the service. |
| `tether run -- <cmd>` | Agent | Run a command with a temporary browser shim. |
| `tether open <url>` | Agent | Send one URL to the host. |
| `tether install-shim` | Agent | Install `tether-open` and, on Linux, `xdg-open`. |
| `tether source` | Agent | Print shell exports for `$PATH` and `$BROWSER`. |

## Configuration

`tether box add` writes `~/.config/tether/config.json` on the host. If `$XDG_CONFIG_HOME` is set, tether uses `$XDG_CONFIG_HOME/tether/config.json` instead. You can edit the file directly:

```json
{
  "boxes": [
    { "name": "my-agent", "ssh_host": "my-agent", "remote_port": 9999 }
  ]
}
```

Run `tether reload` after a change. `remote_port` defaults to `9999` when you use `tether box add`. An optional `auth_token` field requires the same token on the agent via `TETHER_AUTH_TOKEN` or `--auth-token`. Protect the configuration file if you add a token.

The agent accepts these connection options for `tether open` and `tether run`:

| Flag | Environment variable | Default |
|---|---|---|
| `--server` | `TETHER_SERVER` | `127.0.0.1:9999` |
| `--socket` | `TETHER_SOCKET` | Unset (overrides `--server`) |
| `--auth-token` | `TETHER_AUTH_TOKEN` | Unset |
| `--timeout` | `TETHER_TIMEOUT` | `5m` |

The forwarded port listens on the remote machine. Tether does not require a token by default. On a shared remote machine, other local users can reach that port. Set an `auth_token` on the host and agent if those users must not send browser requests. Tether records requested URLs in host logs and recent request history. OAuth URLs can contain sensitive query values. Do not share these logs or screenshots without reviewing them.

## Troubleshooting

If a login does not open a browser, start on the host:

```sh
tether status
tether status --watch
```

If the box is disconnected, read the SSH error in the status output. If the box is connected but the login fails, inspect the shim log on the agent:

```sh
tail -f ~/.cache/tether/open.log
```

Read host service logs with the service manager:

```sh
# macOS
log stream --predicate 'process == "tether"' --info
# Linux
journalctl --user -u tether-host -f
```

If `xdg-open` does not invoke tether, run `command -v xdg-open` on the agent. It must resolve to the shim directory for commands that do not use `$BROWSER`.

If you installed the service or shim before this release, run `tether install` on the host and `tether install-shim` on the agent once. New installs prefer a matching `tether` entry on `$PATH`. Homebrew's link stays at the same path across upgrades. If you run the binary without a stable entry on `$PATH`, reinstall the service and shim after an upgrade.

## Build from source

```sh
git clone https://github.com/mwdomino/tether
cd tether
go build -o tether ./cmd/tether
go test ./...
```

The macOS app also needs the macOS build tools and GUI dependencies. `scripts/package-macos.sh` builds its app bundle. See [design notes](docs/README.md) for the historical specifications. They are not setup instructions.

## License

[MIT](LICENSE).
