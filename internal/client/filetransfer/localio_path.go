package filetransfer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"errors"

	"github.com/google/uuid"
)

func cleanLocalPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.IndexByte(value, 0) >= 0 || !filepath.IsAbs(value) {
		return "", errors.New("local file transfer path is required")
	}
	absolute, err := filepath.Abs(filepath.Clean(value))
	if err != nil || absolute == filepath.VolumeName(absolute)+string(filepath.Separator) {
		return "", errors.New("local file transfer path is invalid")
	}
	return absolute, nil
}
func validateDestination(destination string, overwrite bool) error {
	_, err := os.Lstat(destination)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return err
	case !overwrite:
		return errors.New("local download destination already exists")
	default:
		return nil
	}
}

func publishLocalPath(temporary, destination string, overwrite bool) error {
	if err := validateDestination(destination, overwrite); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(temporary, destination); err != nil {
			return fmt.Errorf("publish local download: %w", err)
		}
		return nil
	} else if err != nil {
		return err
	}
	backup := destination + ".kubeloop-backup-" + uuid.NewString()
	if err := os.Rename(destination, backup); err != nil {
		return fmt.Errorf("stage existing local destination: %w", err)
	}
	if err := os.Rename(temporary, destination); err != nil {
		if rollbackErr := os.Rename(backup, destination); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("publish local download: %w", err),
				fmt.Errorf("restore previous destination: %w", rollbackErr),
			)
		}
		return fmt.Errorf("publish local download: %w", err)
	}
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("remove replaced local destination: %w", err)
	}
	return nil
}
