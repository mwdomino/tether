# tether run — shimless headless usage

## Problem

Today the agent requires a two-step setup: `tether install-shim` (writes
`~/.local/bin/tether-open` and an `xdg-open` shim) plus `eval "$(tether source)"`
(exports `PATH`/`BROWSER`). This installs files on the agent. Some users
want a no-install option — a locked-down box, a one-off login, or just less
ceremony.

## Goal

Add `tether run` so a user can wrap any command and have the browser hook set up
automatically and ephemerally, with nothing left installed:

```
tether run -- aws sso login
```

This is purely additive. `install-shim` and `source` remain for users who want a
persistent setup.

## Command surface

```
tether run [flags] -- <command> [args...]
```

- Everything after `--` is the wrapped command, executed verbatim. A missing
  `--`, or `--` with no following command, is an error with a usage hint.
- `tether run` accepts the same connection flags/env as `tether open`:
  - `--server` / `TETHER_SERVER` (default `127.0.0.1:9999`)
  - `--socket` / `TETHER_SOCKET`
  - `--auth-token` / `TETHER_AUTH_TOKEN`
  - `--timeout` / `TETHER_TIMEOUT` (default `5m`)
  These are forwarded into the child's environment so the backgrounded
  `tether open` invocations pick them up.
- `tether run` exits with the wrapped command's exit code.

## Mechanism

On startup `tether run`:

1. Creates a private temp dir (`os.MkdirTemp`), with `defer os.RemoveAll`.
2. Calls the existing `shim.Install()` with `BinDir` = that temp dir,
   `InstallXDGOpen: true` on Linux, `ForceXDGOpen: true`. This writes the
   identical `tether-open` (and `xdg-open`) backgrounding shim, pointed at the
   current binary via `os.Executable()`. Reusing `shim.Install` guarantees the
   shimless path and the installed path cannot drift.
3. Builds the child environment from `os.Environ()` plus:
   - prepend the temp dir to `PATH`
   - `BROWSER` = the temp `tether-open` shim
   - `TETHER_SERVER` / `TETHER_SOCKET` / `TETHER_AUTH_TOKEN` / `TETHER_TIMEOUT`
     set from the resolved `run` flag values
4. Runs the command with inherited stdin/stdout/stderr and waits for it.
5. Removes the temp dir on exit. The detached `tether open` process (the real
   binary, not anything inside the temp dir) keeps running to relay the OAuth
   callback and exits on its own, so cleanup is safe.

`open.log` continues to go to the default `~/.cache/tether/open.log` (Linux/macOS)
so the existing debugging docs still apply — it is a log file, not an install.

### Reuse note

`tether run` writes no shim text of its own. It delegates entirely to
`shim.Install` against a throwaway `BinDir`, then constructs the child env.

## Exit code propagation

The wrapped command runs via `os/exec`. On a clean exit, `tether run` returns
that exit code. On an `*exec.ExitError`, it propagates `ExitCode()`. If the
command cannot be started (not found, not executable), `tether run` reports the
error and exits non-zero.

## Testing

- Wrap a command against a fake host listener (reuse the existing host/agent test
  harness) and assert the child process sees `BROWSER`, a `PATH` whose first
  entry is the temp dir, and the `TETHER_*` vars set from flags.
- Assert the temp dir is removed after `run` returns.
- Assert exit-code propagation (e.g. wrap `sh -c 'exit 7'`).
- Assert the missing-`--` / missing-command error path.

## Docs

Restructure the README agent setup into two clearly labeled options:

- **(A) Shimless: `tether run`** — no install; good for one-off logins or
  locked-down boxes. `tether run -- aws sso login`.
- **(B) Persistent shim** — `install-shim` + `source`; good for daily use.

Update the two-machine model blurb to mention both, and add a `run` row/section
to the flags reference.

## Out of scope

- No changes to the host `host`/`install` flow.
- No change to the wire protocol or `tether open` behavior.
- `tether run` still requires the host service and SSH `RemoteForward` to be set
  up on the host — it only removes the agent shim install/source
  steps.
