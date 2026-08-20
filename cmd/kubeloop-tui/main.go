package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/fengqi-dev/kube-loop/internal/supervisor"
	"github.com/fengqi-dev/kube-loop/internal/tui"
)

var version = "dev"

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Printf("kubeloop %s\n", version)
		return
	}
	if err := registerBundledResources(); err != nil {
		fmt.Fprintf(os.Stderr, "kubeloop: register bundled resources: %v\n", err)
		os.Exit(1)
	}
	if version != "" && version != "dev" {
		helper.Version = version
		supervisor.Version = version
	}
	state, err := tui.NewState(version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kubeloop: %v\n", err)
		os.Exit(1)
	}
	defer state.Close()

	program := tea.NewProgram(
		tui.New(state),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "kubeloop: %v\n", err)
		os.Exit(1)
	}
}
