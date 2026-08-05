// Package e2e contains Minikube end-to-end tests for KubeLoop core paths.
//
// Layout (by business area):
//
//	e2e/harness/   shared TUN Connect helpers and echo fixtures
//	e2e/connect/   Connect TUN data path, manual network, disconnect
//	e2e/podssh/    Pod-IP SSH/SCP, multi-container login selection
//	e2e/dns/       Split DNS / search proxy / PTR / host aliases
//	e2e/portfwd/   Port Forward (Service / Pod)
//	e2e/exchange/  Service Exchange
//	e2e/mirror/    Service Mirror
//	e2e/preview/   Preview
//	e2e/scripts/   Helper install / stop helpers for run.sh
//
// All tests use a real privileged TUN Connect via session.Manager and the
// local Helper (dev.fengqi.kubeloop.helper.dev).
//
// Prerequisites:
//   - Minikube (or compatible) context
//   - sudo / macOS admin prompt to install or upgrade the Helper
//   - No other TUN client owning Pod CIDRs (Clash steals 10.244/16 → Pod IP test skips)
//
// Run:
//
//	./e2e/run.sh
//
// Or (Helper must already be installed with matching token/sing-box):
//
//	KUBELOOP_E2E=1 ./e2e/scripts/run-go-test.sh
//
// run-go-test.sh prints a failed-test summary at the end.
package e2e
