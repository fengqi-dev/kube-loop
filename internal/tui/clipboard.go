package tui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type clipboardCopiedMsg struct {
	kind string
	err  error
}

func copySessionToClipboard(ctx context.Context, kind, value string) tea.Cmd {
	return func() tea.Msg {
		command, err := clipboardCommand(ctx)
		if err != nil {
			return clipboardCopiedMsg{kind: strings.ToUpper(kind), err: err}
		}
		command.Stdin = strings.NewReader(value)
		if output, err := command.CombinedOutput(); err != nil {
			detail := strings.TrimSpace(string(output))
			if detail != "" {
				err = fmt.Errorf("%w: %s", err, detail)
			}
			return clipboardCopiedMsg{kind: strings.ToUpper(kind), err: err}
		}
		return clipboardCopiedMsg{kind: strings.ToUpper(kind)}
	}
}

func clipboardCommand(ctx context.Context) (*exec.Cmd, error) {
	switch runtime.GOOS {
	case "darwin":
		return exec.CommandContext(ctx, "pbcopy"), nil
	case "windows":
		return exec.CommandContext(ctx, "cmd", "/c", "clip"), nil
	case "linux":
		if path, err := exec.LookPath("wl-copy"); err == nil {
			return exec.CommandContext(ctx, path), nil
		}
		if path, err := exec.LookPath("xclip"); err == nil {
			return exec.CommandContext(ctx, path, "-selection", "clipboard"), nil
		}
		return nil, errors.New("install wl-copy or xclip to use the clipboard")
	default:
		return nil, fmt.Errorf("clipboard is unsupported on %s", runtime.GOOS)
	}
}
