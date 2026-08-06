//go:build darwin

package terminal

import (
	"fmt"
	"os/exec"
	"strings"
)

const terminalAppleScript = `on run argv
	tell application "Terminal"
		activate
		do script item 1 of argv
	end tell
end run`

func open(command string) error {
	output, err := exec.Command(
		"/usr/bin/osascript", "-e", terminalAppleScript, command,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("open Terminal: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
