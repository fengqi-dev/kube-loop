# KubeLoop TUI

`kubeloop` is a K9s-style terminal client for KubeLoop's core workflows.
It is an alternative client implementation, not a direct Kubernetes client:
all authentication, discovery, resource operations, and task lifecycle calls
continue to use the configured KubeLoop Control Plane APIs.

## Install and run

GitHub Releases provide native archives for:

- `darwin-amd64` and `darwin-arm64`
- `windows-amd64` and `windows-arm64`
- `linux-amd64` and `linux-arm64`

Archive names follow `kubeloop-tui-<version>-<os>-<arch>.tar.gz` and contain one
self-contained `kubeloop` executable (`kubeloop.exe` on Windows). Verify the
archive against the release `SHA256SUMS`, then run the binary in a terminal of
at least 60x18.

For development builds:

```sh
make tui
./build/bin/kubeloop

VERSION=v2.1.0 make tui
TUI_GOOS=linux TUI_GOARCH=arm64 make tui
```

`TUI_BINARY` overrides the output path and `TUI_LDFLAGS` overrides the linker
flags. The defaults use `-trimpath`, strip debug tables with `-s -w`, and inject
`VERSION` into `main.version`. The target builds and embeds the matching Helper,
Supervisor (macOS), and sing-box components into the TUI executable.

Homebrew users can install the independent `kubeloop-tui` Formula, which
provides the `kubeloop` command:

```sh
brew tap kube-loop/kubeloop https://github.com/fengqi-dev/kube-loop
brew install --formula kube-loop/kubeloop/kubeloop-tui
```

Stable releases automatically update the Formula with the native macOS and
Linux archive checksums.

Server profiles and optional TUI configuration live under `~/.kubeloop`. The
first launch opens Servers so a KubeLoop Server URL can be added and
authenticated. After restoring or completing authentication, the TUI connects
automatically in TUN mode by default.

## Workspace

The screen contains a global status header, one full-width resource table or
detail page, top command/filter input bars, and contextual shortcuts. Header
shortcuts change with the active page and only show available actions.

| Resource | Commands | Core actions |
| --- | --- | --- |
| Connection | `:connection`, `:conn` | Connect/disconnect, switch SOCKS/TUN mode, uninstall the Helper Service |
| Pods | `:pods`, `:po` | Inspect, Port Forward, interactive SSH |
| Services | `:services`, `:svc` | Inspect, Port Forward, Exchange, Mirror, Preview |
| Sessions | `:sessions` | Inspect, stop, rerun, copy addresses or Exec output, clear completed |
| Servers | `:servers`, `:server` | Add, select, login, delete |
| Namespaces | `:namespaces`, `:ns` | Select the active authorized Namespace |

The TUI intentionally does not expose arbitrary Kubernetes resource kinds,
direct kubeconfig access, shell plugins, or arbitrary local command hooks.

## Navigation and filtering

- `:` opens the command bar at the top; `Tab` and `Shift+Tab` cycle completions, and Up/Down
  browse command history.
- `/` opens the live filter bar at the top. `/pattern` uses case-insensitive RE2 regex,
  `/!pattern` inverts it, and `/-f text` performs fuzzy matching.
- `j`/`k` or arrows move, `g`/`G` jump, and `Ctrl+u`/`Ctrl+d` or
  `PgUp`/`PgDn` page.
- `Enter` opens details or performs the primary action. `Esc` returns or
  cancels. `-` or `[` goes back in resource history and `]` goes forward.
- `r` refreshes, `?` opens contextual help, and `:q` exits.

## Aliases and hotkeys

Create `~/.kubeloop/tui.yaml` to add aliases or hotkeys:

```yaml
version: 1
aliases:
  pp: pods
hotkeys:
  ctrl+p: servers
```

Targets must resolve to built-in resources or commands. Reserved navigation
keys, unknown targets, unknown YAML fields, and unsupported versions are
ignored with a non-fatal warning. Configuration cannot run shell commands or
plugins.

## Connection modes and security boundary

SOCKS mode does not require the privileged network Helper. TUN mode uses the
same managed sing-box and Helper boundary as the desktop client and may require
local approval. TUN is the default, and the TUI connects automatically after
restoring or completing authentication. Changing Server is blocked while
connected; changing Namespace rebuilds the corresponding Session and Data
Plane. Disconnecting with active Sessions requires confirmation.

Both clients verify and share release-specific components under
`~/.kubeloop/components/<version>/<os>-<arch>/`. This user-owned cache is only
a distribution source. TUN installation promotes Helper and sing-box into
protected system paths before elevated execution; the privileged service never
executes sing-box directly from the cache or a package directory.

The client stores no Kubernetes credentials and never selects a dial target
from untrusted display metadata. Server authorization, Namespace scope,
RelayTicket, and task ownership remain authoritative.

## Tests and release gate

```sh
make tui-test-e2e
```

This CI-safe PTY fixture drives the real Bubble Tea event loop and covers
keyboard, mouse, paste, resize, commands, filters, details, actions, forms, and
confirmations without credentials or external side effects. CI runs it on
every normal change; the Release workflow runs it again before publishing all
six native TUI archives.

The live gate is separate:

```sh
KUBELOOP_TUI_E2E_LIVE_HOME=/path/to/isolated/authenticated/home \
  make tui-test-live-e2e
```

Set `KUBELOOP_TUI_E2E_LIVE_CONNECT=1` only in a disposable environment to
exercise real SOCKS/TUN connection and mode switching. Fixture success is not
evidence that the live server, local Helper, TUN routes, DNS, or external
network environment has been validated.
