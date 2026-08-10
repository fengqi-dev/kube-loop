package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	controllerstorage "github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/fsatomic"
)

func runStorageCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printStorageUsage(stderr)
		return 2
	}
	var err error
	switch args[0] {
	case "export":
		err = runStorageExport(ctx, args[1:], stdout, stderr)
	case "import":
		err = runStorageImport(ctx, args[1:], stdout, stderr)
	case "backup":
		err = runStorageBackup(ctx, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printStorageUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown storage command %q\n", args[0])
		printStorageUsage(stderr)
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "storage %s failed: %v\n", args[0], err)
		return 1
	}
	return 0
}

func runStorageExport(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("storage export", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "", "new logical export file")
	force := flags.Bool("force", false, "replace an existing regular export file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*output) == "" {
		return errors.New("--output is required and positional arguments are not accepted")
	}
	config, err := controllerstorage.ConfigFromEnv()
	if err != nil {
		return err
	}
	raw, metadata, err := controllerstorage.Export(ctx, config, controllerstorage.ExportOptions{
		CreatedByVersion: controllerStorageVersion(),
	})
	if err != nil {
		return err
	}
	path, err := writeStorageOutput(*output, raw, *force)
	if err != nil {
		return err
	}
	return writeStorageResult(stdout, struct {
		Path string `json:"path"`
		controllerstorage.ExportMetadata
	}{Path: path, ExportMetadata: metadata})
}

func runStorageImport(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("storage import", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "", "validated logical export file")
	actor := flags.String("actor", "", "operator identity recorded in the import audit event")
	confirmedEmpty := flags.Bool("confirm-empty", false, "confirm the PostgreSQL target is offline and expected to be empty")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*input) == "" || strings.TrimSpace(*actor) == "" {
		return errors.New("--input and --actor are required and positional arguments are not accepted")
	}
	if !*confirmedEmpty {
		return errors.New("--confirm-empty is required for this destructive offline operation")
	}
	raw, err := readStorageInput(*input)
	if err != nil {
		return err
	}
	config, err := controllerstorage.ConfigFromEnv()
	if err != nil {
		return err
	}
	result, err := controllerstorage.Import(ctx, config, raw, controllerstorage.ImportOptions{
		ConfirmedEmpty: true,
		ImportedBy:     *actor,
	})
	if err != nil {
		return err
	}
	return writeStorageResult(stdout, result)
}

func runStorageBackup(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("storage backup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "", "new SQLite snapshot file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*output) == "" {
		return errors.New("--output is required and positional arguments are not accepted")
	}
	config, err := controllerstorage.ConfigFromEnv()
	if err != nil {
		return err
	}
	result, err := controllerstorage.BackupSQLite(ctx, config, *output, nil)
	if err != nil {
		return err
	}
	return writeStorageResult(stdout, result)
}

func controllerStorageVersion() string {
	return "kubeloop-controller/" + strings.TrimSpace(version) + " commit/" + strings.TrimSpace(commit)
}

func readStorageInput(path string) ([]byte, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return nil, errors.New("resolve storage import path")
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, errors.New("inspect storage import file")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("storage import path must be a regular file")
	}
	if info.Size() > controllerstorage.MaxExportBytes {
		return nil, errors.New("storage import exceeds the size limit")
	}
	file, err := os.Open(absolute)
	if err != nil {
		return nil, errors.New("open storage import file")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, controllerstorage.MaxExportBytes+1))
	if err != nil {
		return nil, errors.New("read storage import file")
	}
	if len(raw) > controllerstorage.MaxExportBytes {
		return nil, errors.New("storage import exceeds the size limit")
	}
	return raw, nil
}

func writeStorageOutput(path string, raw []byte, force bool) (string, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", errors.New("resolve storage export path")
	}
	if info, err := os.Lstat(absolute); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", errors.New("existing storage export path must be a regular file")
		}
		if !force {
			return "", errors.New("storage export path already exists; use --force to replace it")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("inspect storage export path")
	}
	directory := filepath.Dir(absolute)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", errors.New("create storage export directory")
	}
	if info, err := os.Lstat(directory); err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("storage export directory must be a real directory")
	}
	if err := fsatomic.WriteFile(absolute, raw, 0o700, 0o600); err != nil {
		return "", errors.New("write storage export file")
	}
	return absolute, nil
}

func writeStorageResult(writer io.Writer, result any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		return errors.New("write storage command result")
	}
	return nil
}

func printStorageUsage(writer io.Writer) {
	fmt.Fprintln(writer, "Offline Controller storage management:")
	fmt.Fprintln(writer, "  kubeloop-controller storage export --output FILE [--force]")
	fmt.Fprintln(writer, "  kubeloop-controller storage import --input FILE --actor ID --confirm-empty")
	fmt.Fprintln(writer, "  kubeloop-controller storage backup --output FILE")
	fmt.Fprintln(writer, "Storage configuration is read only from KUBELOOP_STORAGE_* / KUBELOOP_POSTGRESQL_* environment variables.")
}
