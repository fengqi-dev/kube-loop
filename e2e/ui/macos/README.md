# Native macOS UI suite

This suite uses XCUITest to operate the accessibility tree of the real Wails `KubeLoop.app`; it does not load the React application in Playwright or replace Wails bindings.

The dedicated self-hosted runner must provide Xcode, Chrome as the default browser, Docker Desktop/Minikube, Helm, and a pre-authorized real KubeLoop Helper. Chrome must have Accessibility automation enabled for the runner account. The Helper must allow the runner account to install and remove real TUN/DNS state because the suite connects and disconnects both SOCKS and TUN modes. Build the application with `wails build`, then run `run-macos.sh`. Set `KUBELOOP_UI_E2E_PROFILE_PATH` to a disposable Server Profile file so a previous login cannot satisfy the test. The runner must not execute two UI jobs concurrently.
