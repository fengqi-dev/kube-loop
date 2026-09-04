//go:build ignore

package main

import (
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	helperprotocol "github.com/fengqi-dev/kube-loop/internal/protocol/helperrpc"
)

func main() {
	operation := flag.String("operation", "", "install or uninstall")
	source := flag.String("source", "", "helper service binary")
	singBox := flag.String("sing-box", "", "sing-box binary")
	elevate := flag.Bool("elevate", false, "run the privileged child through sudo -n")
	flag.Parse()

	var err error
	switch *operation {
	case "install":
		err = install(*source, *singBox, *elevate)
	case "uninstall":
		err = uninstall(*source, *elevate)
	default:
		err = fmt.Errorf("--operation must be install or uninstall")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func install(source, singBox string, elevate bool) error {
	if source == "" || singBox == "" {
		return fmt.Errorf("--source and --sing-box are required")
	}
	token, err := helper.EnsureUserToken()
	if err != nil {
		return err
	}
	home, err := helper.UserHomeDir()
	if err != nil {
		return err
	}
	current, err := user.Current()
	if err != nil {
		return fmt.Errorf("current user: %w", err)
	}
	uid := 0
	ownerSID := ""
	if runtime.GOOS == "windows" {
		ownerSID = current.Uid
	} else {
		uid, err = strconv.Atoi(current.Uid)
		if err != nil {
			return fmt.Errorf("parse uid %q: %w", current.Uid, err)
		}
	}

	command := source
	args := []string{"install"}
	args = append(args,
		"--source", source,
		"--token", token,
		"--uid", strconv.Itoa(uid),
		"--version", helper.Version,
		"--home", home,
		"--sing-box", singBox,
	)
	if ownerSID != "" {
		args = append(args, "--sid", ownerSID)
	}
	if err := runPrivileged(command, args, elevate); err != nil {
		return fmt.Errorf("install helper: %w", err)
	}

	installedPath := helper.BinaryInstallPath()
	if runtime.GOOS == "windows" {
		// manage-helper is run via "go run", so its own executable lives in a
		// temporary directory. The packaged helper selects its destination from
		// the application root containing the resource copy.
		installedPath = helper.BinaryInstallPathForExecutable(source)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		status := helper.GetStatus(ctx)
		if status.Installed && status.Running && status.CoreReady &&
			status.Protocol == helperprotocol.Version {
			if err := verifyInstalledBinary(source, installedPath); err != nil {
				return err
			}
			fmt.Printf("helper ready: version=%s protocol=%d\n", status.Version, status.Protocol)
			return nil
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("helper did not become ready: %+v: %w", status, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func verifyInstalledBinary(source, installedPath string) error {
	sourceContent, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read helper source for verification: %w", err)
	}
	installedContent, err := os.ReadFile(installedPath)
	if err != nil {
		return fmt.Errorf("read installed helper for verification: %w", err)
	}
	sourceHash := sha256.Sum256(sourceContent)
	installedHash := sha256.Sum256(installedContent)
	if sourceHash != installedHash {
		return fmt.Errorf(
			"installed helper hash does not match source: got %x want %x",
			installedHash, sourceHash,
		)
	}
	return nil
}

func uninstall(source string, elevate bool) error {
	client, err := helper.NewClient()
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, _ = client.StopAll(ctx)
		cancel()
	}

	command := source
	args := []string{"uninstall"}
	if command == "" {
		return fmt.Errorf("--source is required")
	}
	if err := runPrivileged(command, args, elevate); err != nil {
		return fmt.Errorf("uninstall helper: %w", err)
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		status := helper.GetStatus(context.Background())
		if !status.Installed && !status.Running {
			fmt.Println("helper uninstalled")
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("helper is still installed or running after uninstall")
}

func runPrivileged(command string, args []string, elevate bool) error {
	if elevate {
		args = append([]string{"-n", command}, args...)
		command = "sudo"
	}
	cmd := exec.Command(command, args...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, detail)
}
