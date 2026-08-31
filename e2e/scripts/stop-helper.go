//go:build ignore

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/helper"
)

func main() {
	client, err := helper.NewClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "helper client: %v\n", err)
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := client.StopAll(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "helper stop-all: %v\n", err)
		os.Exit(0)
	}
}
