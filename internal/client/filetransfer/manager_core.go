package filetransfer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/fsatomic"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
)

const (
	StatusQueued      = "queued"
	StatusPreparing   = "preparing"
	StatusRunning     = "running"
	StatusCompleted   = "completed"
	StatusFailed      = "failed"
	StatusCancelled   = "cancelled"
	StatusInterrupted = "interrupted"

	stateVersion       = 1
	defaultMaximumSize = uint64(1 << 30)
)

type Request struct {
	ProfileID  string `json:"profileId"`
	Direction  string `json:"direction"`
	Kind       string `json:"kind"`
	Pod        string `json:"pod"`
	Container  string `json:"container,omitempty"`
	LocalPath  string `json:"localPath"`
	RemotePath string `json:"remotePath"`
	Overwrite  bool   `json:"overwrite,omitempty"`
}

type Task struct {
	ID            string     `json:"id"`
	ProfileID     string     `json:"profileId"`
	SessionID     string     `json:"sessionId"`
	Namespace     string     `json:"namespace"`
	Direction     string     `json:"direction"`
	Kind          string     `json:"kind"`
	Pod           string     `json:"pod"`
	Container     string     `json:"container,omitempty"`
	LocalPath     string     `json:"localPath"`
	RemotePath    string     `json:"remotePath"`
	Overwrite     bool       `json:"overwrite,omitempty"`
	Status        string     `json:"status"`
	TotalBytes    uint64     `json:"totalBytes,omitempty"`
	DoneBytes     uint64     `json:"doneBytes,omitempty"`
	Checksum      string     `json:"checksum,omitempty"`
	ResumeID      string     `json:"resumeId,omitempty"`
	TemporaryPath string     `json:"temporaryPath,omitempty"`
	Error         string     `json:"error,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"               ts_type:"string"`
	UpdatedAt     time.Time  `json:"updatedAt"               ts_type:"string"`
	CompletedAt   *time.Time `json:"completedAt,omitempty"   ts_type:"string"`
}

type Config struct {
	StatePath    string
	TemporaryDir string
	MaximumBytes uint64
	Now          func() time.Time
	OnEvent      func(Task)
}

type persistedState struct {
	Version int    `json:"version"`
	Tasks   []Task `json:"tasks"`
}

type activeTransfer struct {
	profile       profile.Profile
	session       remote.Session
	request       Request
	cancel        context.CancelFunc
	done          chan struct{}
	resumeID      string
	temporaryPath string
}

type Manager struct {
	client       Client
	statePath    string
	temporaryDir string
	maximumBytes uint64
	now          func() time.Time
	onEvent      func(Task)
	ctx          context.Context
	cancel       context.CancelFunc

	lifecycle sync.RWMutex
	persistMu sync.Mutex
	mu        sync.Mutex
	tasks     map[string]Task
	active    map[string]*activeTransfer
	wg        sync.WaitGroup
	writeFile func(string, []byte, os.FileMode, os.FileMode) error
}

func NewManager(client Client, config Config) (*Manager, error) {
	if client == nil {
		return nil, errors.New("file transfer client is required")
	}
	if config.MaximumBytes == 0 {
		config.MaximumBytes = defaultMaximumSize
	}
	if config.MaximumBytes < filestream.MaximumData || config.MaximumBytes > 1<<40 {
		return nil, errors.New("file transfer maximum size must be between 256 KiB and 1 TiB")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.OnEvent == nil {
		config.OnEvent = func(Task) {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		client:       client,
		statePath:    strings.TrimSpace(config.StatePath),
		temporaryDir: strings.TrimSpace(config.TemporaryDir),
		maximumBytes: config.MaximumBytes,
		now:          config.Now,
		onEvent:      config.OnEvent,
		ctx:          ctx,
		cancel:       cancel,
		tasks:        make(map[string]Task),
		active:       make(map[string]*activeTransfer),
		writeFile:    fsatomic.WriteFile,
	}
	if manager.statePath != "" {
		if err := fsatomic.CleanupTemps(manager.statePath); err != nil {
			cancel()
			return nil, fmt.Errorf("clean file transfer state: %w", err)
		}
	}
	if err := manager.load(); err != nil {
		cancel()
		return nil, err
	}
	return manager, nil
}
