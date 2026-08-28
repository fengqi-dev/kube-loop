package runtime

import (
	"context"
	"io"
	"log"
	"log/slog"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fengqi-dev/kube-loop/internal/buildinfo"
	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/fengqi-dev/kube-loop/internal/supervisor"
	"github.com/fengqi-dev/kube-loop/internal/tui"
)

func Run(ctx context.Context, info buildinfo.Info) error {
	restoreLogs := SilenceProcessLogs()
	defer restoreLogs()

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

func SilenceProcessLogs() func() {
	previousLogger := slog.Default()
	previousLogWriter := log.Writer()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	log.SetOutput(io.Discard)
	return func() {
		slog.SetDefault(previousLogger)
		log.SetOutput(previousLogWriter)
	}
}
