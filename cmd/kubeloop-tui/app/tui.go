package app

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

	"github.com/fengqi-dev/kube-loop/internal/buildinfo"
	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/fengqi-dev/kube-loop/internal/supervisor"
	"github.com/fengqi-dev/kube-loop/internal/tui"
)

// NewTUICommand returns the kubeloop cobra command.
func NewTUICommand() *cobra.Command {
	return newTUICommand(buildinfo.Get())
}

func newTUICommand(info buildinfo.Info) *cobra.Command {
	command := &cobra.Command{
		Use:     "kubeloop",
		Short:   "Open the KubeLoop terminal client",
		Version: info.Version,
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			signalContext, stopSignals := signal.NotifyContext(
				command.Context(),
				os.Interrupt,
				syscall.SIGTERM,
			)
			defer stopSignals()
			return runTUI(signalContext, info)
		},
	}
	command.SilenceErrors = true
	command.SilenceUsage = true
	command.SetVersionTemplate("kubeloop {{.Version}}\n")
	command.AddCommand(
		&cobra.Command{
			Use:   "version",
			Short: "Print the version",
			Args:  cobra.NoArgs,
			RunE: func(command *cobra.Command, _ []string) error {
				_, err := fmt.Fprintf(command.OutOrStdout(), "kubeloop %s\n", info.Version)
				return err
			},
		},
	)
	return command
}

func runTUI(ctx context.Context, info buildinfo.Info) error {
	restoreLogs := silenceTUIProcessLogs()
	defer restoreLogs()

	if err := registerBundledResources(); err != nil {
		return fmt.Errorf("register bundled resources: %w", err)
	}
	if info.Version != "" && info.Version != "dev" {
		helper.Version = info.Version
		supervisor.Version = info.Version
	}
	state, err := tui.NewState(info.Version)
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
