package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/fengqi-dev/kube-loop/internal/helper"
)

var version = "dev"
var buildMarker = ""

func main() {
	// buildMarker lets E2E produce a byte-distinct binary without changing the
	// dev service identity used for isolated test installs.
	_ = buildMarker
	if version != "" && version != "dev" {
		helper.Version = version
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(execute(ctx, os.Args[1:], os.Stdout, os.Stderr, productionDependencies(), helper.Version))
}
