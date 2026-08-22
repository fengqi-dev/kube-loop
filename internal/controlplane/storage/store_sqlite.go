package storage

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func prepareSQLite(config Config) (string, error) {
	absolute, err := filepath.Abs(config.SQLitePath)
	if err != nil {
		return "", fmt.Errorf("resolve SQLite path: %w", err)
	}
	config.SQLitePath = absolute
	directory := filepath.Dir(absolute)
	if info, err := os.Lstat(directory); err == nil &&
		info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("sQLite directory must not be a symbolic link")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create SQLite directory: %w", err)
	}
	if info, err := os.Lstat(absolute); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("sQLite database must not be a symbolic link")
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("sQLite database path must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect SQLite database: %w", err)
	}
	file, err := os.OpenFile(absolute, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", fmt.Errorf("create SQLite database: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close SQLite database: %w", err)
	}
	dsn := sqliteFileURL(absolute, runtime.GOOS == operatingSystemWindows)
	query := url.Values{}
	query.Add(
		"_pragma",
		"busy_timeout("+strconv.FormatInt(
			config.BusyTimeout.Milliseconds(),
			10,
		)+")",
	)
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "synchronous(NORMAL)")
	query.Add("_txlock", "immediate")
	return dsn + "?" + query.Encode(), nil
}

func sqliteFileURL(absolute string, windows bool) string {
	path := filepath.ToSlash(absolute)
	if windows {
		// filepath.ToSlash follows the host OS. Tests exercise this conversion on
		// non-Windows builders too, so normalize Windows separators explicitly.
		path = strings.ReplaceAll(absolute, `\`, "/")
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}
