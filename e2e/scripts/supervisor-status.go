//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/fengqi-dev/kube-loop/internal/supervisor"
)

func main() {
	token, err := helper.ReadUserToken()
	if err != nil {
		fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := &supervisor.Client{Config: supervisor.CurrentConfig(), Token: token}
	var response any
	if len(os.Args) == 2 && os.Args[1] == "restart-worker" {
		response, err = client.RestartWorker(ctx)
	} else if len(os.Args) == 1 {
		response, err = client.Status(ctx)
	} else {
		fatal(fmt.Errorf("usage: supervisor-status.go [restart-worker]"))
	}
	if err != nil {
		fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
