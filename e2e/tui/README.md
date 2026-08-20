# TUI end-to-end tests

The TUI has two deliberately separate PTY suites.

## Isolated fixture gate

```sh
make tui-test-e2e
```

This compiles the Go test binary, starts the real Bubble Tea program in an
Expect-managed PTY, sends keyboard, paste, mouse and resize events, and checks
the rendered terminal output. External service effects are replaced by
deterministic fixture state. It is safe for CI and does not read user profiles
or credentials.

The fixture gate covers the K9s-style resource workspace, command completion,
resource history, regex/inverse/fuzzy filters, detail pages, contextual Pod,
Service and Task actions, profile forms and confirmations, connection mode
switching, minimum-size recovery, mouse routing and bracketed paste.

The normal CI `Checks` job runs this fixture gate. The tag-triggered Release
workflow runs it again on Linux amd64 before publishing TUI archives, then
builds native TUI binaries for macOS, Windows and Linux on amd64 and arm64.
The release gate remains fixture-based and does not use credentials.

## Live gate

```sh
KUBELOOP_TUI_E2E_LIVE_HOME=/path/to/isolated/authenticated/home \
  make tui-test-live-e2e
```

The supplied home directory must already contain an authenticated profile.
The default live gate only launches the real binary and checks navigation.
To connect the real SOCKS data plane, switch to TUN and switch back, opt in
explicitly:

```sh
KUBELOOP_TUI_E2E_LIVE_HOME=/path/to/isolated/authenticated/home \
KUBELOOP_TUI_E2E_LIVE_CONNECT=1 \
  make tui-test-live-e2e
```

The connect variant may install system routes and DNS through the existing TUN
helper. Use only a disposable profile and cluster. Fixture success must not be
reported as live E2E success.

## Workspace configuration

Optional aliases and hotkeys are loaded from `~/.kubeloop/tui.yaml`:

```yaml
version: 1
aliases:
  pp: pods
hotkeys:
  ctrl+p: profiles
```

Targets must resolve to built-in resources or commands. Reserved navigation
keys and unknown targets are ignored with a non-fatal warning; configuration
cannot execute shell commands or plugins.
