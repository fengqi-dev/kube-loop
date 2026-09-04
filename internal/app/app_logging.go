package app

import (
	"context"
	"errors"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fengqi-dev/kube-loop/internal/logging"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

const appLogFileName = "app.log"

// appLog is the file-backed half of the application log. slog records are
// routed to it by the shared logger's handler alongside the terminal stream.
// The file is truncated (covered) on each application start so it cannot grow
// unbounded. It implements io.Writer so it can be a slog handler output.
type appLog struct {
	mu    sync.Mutex
	path  string
	level slog.LevelVar
	file  *os.File
}

// Level reports the currently configured threshold (LevelVar implements
// slog.Leveler, so it doubles as the handler level filter).
func (sink *appLog) Level() slog.Level { return sink.level.Level() }

// Write appends a single formatted slog line to the log file. It is best
// effort; a failed write only degrades logging, never the caller.
func (sink *appLog) Write(line []byte) (int, error) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.file == nil {
		if sink.openLocked() != nil {
			return 0, errors.New("application log file unavailable")
		}
	}
	if _, err := sink.file.Write(line); err != nil {
		sink.file = nil
		return 0, err
	}
	return len(line), nil
}

// openLocked opens the log file in append mode. Callers must hold sink.mu.
func (sink *appLog) openLocked() error {
	if sink.path == "" {
		return errors.New("application log path is unavailable")
	}
	file, err := os.OpenFile(sink.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	sink.file = file
	return nil
}

// truncate opens the log file after clearing any existing content. It is used
// once at application startup so a fresh run starts with an empty log.
func (sink *appLog) truncate() error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.path == "" {
		return errors.New("application log path is unavailable")
	}
	file, err := os.OpenFile(sink.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	sink.file = file
	return nil
}

func (sink *appLog) close() {
	if sink == nil {
		return
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.file != nil {
		_ = sink.file.Close()
		sink.file = nil
	}
}

// configureAppLog prepares the file-backed log sink for layout. It is safe to
// call even when the layout is unavailable; the sink then only feeds the
// terminal stream. The LevelVar defaults to info (slog.LevelVar zero value).
func configureAppLog(layout utils.Layout, available bool) *appLog {
	sink := &appLog{}
	if !available {
		return sink
	}
	sink.path = filepath.Join(layout.LogsDir(), appLogFileName)
	if err := os.MkdirAll(filepath.Dir(sink.path), 0o700); err != nil {
		log.Printf("create application log directory: %v", err)
		return sink
	}
	if err := sink.truncate(); err != nil {
		log.Printf("truncate application log file: %v", err)
	}
	return sink
}

// newAppLogger builds the application logger. It writes to both the terminal
// stream and the file sink through a MultiHandler that shares a single
// threshold, so a runtime level change filters both immediately.
func newAppLogger(sink *appLog, terminal io.Writer) *slog.Logger {
	options := &slog.HandlerOptions{Level: &sink.level}
	return slog.New(slog.NewMultiHandler(
		slog.NewTextHandler(terminal, options),
		slog.NewTextHandler(sink, options),
	))
}

func (a *App) log(level slog.Level, message string) {
	if a == nil {
		return
	}
	if a.logger == nil {
		return
	}
	a.logger.Log(context.Background(), level, message)
}

func (a *App) logDebug(message string) { a.log(slog.LevelDebug, message) }
func (a *App) logInfo(message string)  { a.log(slog.LevelInfo, message) }
func (a *App) logWarn(message string)  { a.log(slog.LevelWarn, message) }
func (a *App) logError(message string) { a.log(slog.LevelError, message) }

// parseSlogLevel converts a user-facing level string into a slog level. The
// level names themselves come from internal/logging, which owns the set the
// Gateway and Control Plane already accept; only the empty default is local.
func parseSlogLevel(raw string) (slog.Level, error) {
	if strings.TrimSpace(raw) == "" {
		return slog.LevelInfo, nil
	}
	return logging.ParseLevel(raw)
}

// slogLevelString maps a slog level onto the canonical lowercase label used by
// settings and the sing-box config.
func slogLevelString(level slog.Level) string {
	switch {
	case level >= slog.LevelError:
		return "error"
	case level >= slog.LevelWarn:
		return "warn"
	case level >= slog.LevelInfo:
		return "info"
	default:
		return "debug"
	}
}
