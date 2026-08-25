package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	internalcli "github.com/fengqi-dev/kube-loop/internal/cli"
	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/fengqi-dev/kube-loop/internal/supervisor"
	"github.com/fengqi-dev/kube-loop/internal/tui"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	exitCode := executeTUI(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(exitCode)
}

func executeTUI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return internalcli.Execute(ctx, newTUICommand(), args, stdout, stderr)
}

func newTUICommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "kubeloop",
		Short:   "Open the KubeLoop terminal client",
		Version: version,
		Args:    internalcli.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runTUI(command.Context())
		},
	}
	internalcli.ConfigureRoot(command, "kubeloop")
	internalcli.AddVersionCommand(command, "kubeloop", version)
	return command
}

func runTUI(ctx context.Context) error {
	restoreLogs := silenceTUIProcessLogs()
	defer restoreLogs()

	if err := registerBundledResources(); err != nil {
		return fmt.Errorf("register bundled resources: %w", err)
	}
	if version != "" && version != "dev" {
		helper.Version = version
		supervisor.Version = version
	}
	state, err := tui.NewState(version)
	if err != nil {
		return err
	}
	defer state.Close()
	program := tea.NewProgram(
		tui.New(state),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithContext(ctx),
	)
	_, err = program.Run()
	return err
}

func silenceTUIProcessLogs() func() {
	previousLogger := slog.Default()
	previousLogWriter := log.Writer()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	log.SetOutput(io.Discard)
	return func() {
		slog.SetDefault(previousLogger)
		log.SetOutput(previousLogWriter)
	}
}
