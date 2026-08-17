//go:build ignore

// Exits 0 when the privileged helper is reachable and CoreReady.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	helperprotocol "github.com/fengqi-dev/kube-loop/internal/protocol/helper"
)

func main() {
	client, err := helper.NewClient()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := client.Ping(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !response.OK || !response.CoreReady || response.Protocol != helperprotocol.Version {
		fmt.Fprintf(os.Stderr, "helper not ready: %+v\n", response)
		os.Exit(1)
	}
}
