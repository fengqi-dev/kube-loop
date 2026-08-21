Release and IDE builds place platform-specific helper binaries here before
building the desktop application:

- `kubeloop-helper[.exe]` — privileged service
- `kubeloop-supervisor` — stable macOS privileged worker updater
- Windows uses the same `kubeloop-helper.exe` for service, install, and uninstall operations.

The desktop and TUI binaries embed them and materialize verified copies under
`~/.kubeloop/cache/components/<version>/<os>-<arch>/` (or the isolated
`~/.kubeloop-dev` tree for development builds).

The first macOS install authorizes both services. Later exact worker updates are
streamed to the stable supervisor and do not display another administrator
password prompt. Automatic TUN startup may still reuse a healthy development
worker when no exact update was requested.
