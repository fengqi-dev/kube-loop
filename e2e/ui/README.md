# UI end-to-end tests

The admin suite drives the browser management UI against a fresh, real KubeLoop deployment. It never starts a mock API. Provision an isolated cluster, capture the one-time IAM bootstrap token without printing it, expose the HTTPS public URL, then run `run-admin.sh` with the required environment variables.

The macOS suite is intentionally separate: it builds and drives the real Wails application through XCUITest and requires the dedicated `kubeloop-ui-e2e-macos` runner baseline documented in `macos/README.md`.

`run-real-environment.sh` is the nightly and release-gate entrypoint. On a dedicated macOS runner it creates an isolated Minikube profile and desktop Server Profile, deploys the real Control Plane, Gateway, and Operator with a run-specific service ID, retrieves the single-use bootstrap token without printing it, runs the browser suite, builds `KubeLoop.app`, and runs XCUITest. The profile is deleted on exit unless `KUBELOOP_UI_E2E_KEEP_CLUSTER=1` is explicitly set; collected Control Plane logs redact bootstrap tokens.

The browser suite covers IAM bootstrap, local Authorization Code + PKCE login, management navigation, group and Namespace access, local-user and invitation creation, OAuth Client creation, recovery, audit export, sign-out, mobile navigation, and rejection of Password/Implicit/Hybrid grants. The native suite verifies the real Wails accessibility surface, desktop login, authentication retention across tabs, SOCKS and TUN connection/disconnection, and the TUN/SOCKS selection regression.
