Release and IDE builds place platform-specific helper binaries here before
building the desktop application:

- `kubeloop-helper[.exe]` — privileged service
- Windows uses the same `kubeloop-helper.exe` for service, install, and uninstall operations.

The desktop binary embeds them and materializes copies under
`~/.kubeloop/helper/resources/` when a packaged `resources\` directory is not
available (for example during `wails dev`).
