package filemanager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/podssh"
	"github.com/kballard/go-shellquote"
)

const stateVersion = 1

type Manager struct {
	executor podssh.Executor
	catalog  podCatalog
	path     string
	nextID   atomic.Uint64
	slots    chan struct{}

	mu          sync.Mutex
	tasks       map[string]*TransferTask
	cancels     map[string]context.CancelFunc
	subscribers []func(TransferTask)
}

type podCatalog interface {
	ListPods(context.Context, string, string) ([]cluster.PodInfo, error)
}

func NewManager(executor podssh.Executor, statePath string) *Manager {
	catalog, _ := any(executor).(podCatalog)
	manager := &Manager{
		executor: executor, catalog: catalog, path: statePath, slots: make(chan struct{}, 2),
		tasks: map[string]*TransferTask{}, cancels: map[string]context.CancelFunc{},
	}
	_ = manager.load()
	return manager
}

func (m *Manager) Subscribe(callback func(TransferTask)) {
	if callback == nil {
		return
	}
	m.mu.Lock()
	m.subscribers = append(m.subscribers, callback)
	m.mu.Unlock()
}

func (m *Manager) LocalHomeDirectory() (string, error) {
	return os.UserHomeDir()
}

func (m *Manager) ListLocalDirectory(rawPath string) ([]FileEntry, error) {
	if strings.TrimSpace(rawPath) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		rawPath = home
	}
	root, err := filepath.Abs(filepath.Clean(rawPath))
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("list local directory: %w", err)
	}
	items := make([]FileEntry, 0, len(entries))
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		items = append(items, FileEntry{
			Name: entry.Name(), Path: filepath.Join(root, entry.Name()),
			Dir: entry.IsDir(), Size: info.Size(), Mode: uint32(info.Mode().Perm()),
			ModTime: info.ModTime(),
		})
	}
	sortEntries(items)
	return items, nil
}

func (m *Manager) ListPodDirectory(
	ctx context.Context,
	target Target,
	rawPath string,
) ([]FileEntry, error) {
	if err := validateTarget(target); err != nil {
		return nil, err
	}
	if err := m.validatePodIdentity(ctx, target); err != nil {
		return nil, err
	}
	remotePath, err := cleanRemotePath(rawPath)
	if err != nil {
		return nil, err
	}
	var stdout bytes.Buffer
	if err := m.exec(ctx, target, "ls -A1 -- "+shellquote.Join(remotePath), nil, &stdout); err != nil {
		return nil, fmt.Errorf("list container directory: %w", err)
	}
	names := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	items := make([]FileEntry, 0, len(names))
	for _, name := range names {
		if name == "" || strings.ContainsRune(name, '\n') {
			continue
		}
		info, statErr := m.remoteStat(ctx, target, path.Join(remotePath, name))
		if statErr != nil {
			continue
		}
		info.Name = name
		items = append(items, info)
	}
	sortEntries(items)
	return items, nil
}

func (m *Manager) CreateLocalDirectory(rawParent, name string) error {
	name, err := validateEntryName(name)
	if err != nil {
		return err
	}
	parent, err := cleanLocalPath(rawParent)
	if err != nil {
		return err
	}
	return os.Mkdir(filepath.Join(parent, name), 0o755)
}

func (m *Manager) CreateLocalFile(rawParent, name string) error {
	name, err := validateEntryName(name)
	if err != nil {
		return err
	}
	parent, err := cleanLocalPath(rawParent)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(
		filepath.Join(parent, name),
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		0o644,
	)
	if err != nil {
		return err
	}
	return file.Close()
}

func (m *Manager) CreatePodDirectory(
	ctx context.Context,
	target Target,
	rawParent, name string,
) error {
	if err := validateTarget(target); err != nil {
		return err
	}
	if err := m.validatePodIdentity(ctx, target); err != nil {
		return err
	}
	name, err := validateEntryName(name)
	if err != nil {
		return err
	}
	parent, err := cleanRemotePath(rawParent)
	if err != nil {
		return err
	}
	destination := path.Join(parent, name)
	if err := m.exec(ctx, target, "mkdir -- "+shellquote.Join(destination), nil, io.Discard); err != nil {
		return fmt.Errorf("create container directory: %w", err)
	}
	return nil
}

func (m *Manager) CreatePodFile(
	ctx context.Context,
	target Target,
	rawParent, name string,
) error {
	if err := validateTarget(target); err != nil {
		return err
	}
	if err := m.validatePodIdentity(ctx, target); err != nil {
		return err
	}
	name, err := validateEntryName(name)
	if err != nil {
		return err
	}
	parent, err := cleanRemotePath(rawParent)
	if err != nil {
		return err
	}
	destination := path.Join(parent, name)
	quoted := shellquote.Join(destination)
	script := "if [ -e " + quoted + " ] || [ -L " + quoted +
		" ]; then echo 'destination already exists' >&2; exit 1; fi; : > " + quoted
	if err := m.exec(ctx, target, script, nil, io.Discard); err != nil {
		return fmt.Errorf("create container file: %w", err)
	}
	return nil
}

func (m *Manager) RenameLocalPath(rawSource, newName string) error {
	name, err := validateEntryName(newName)
	if err != nil {
		return err
	}
	source, err := cleanLocalPath(rawSource)
	if err != nil {
		return err
	}
	if isLocalRoot(source) {
		return errors.New("cannot rename a filesystem root")
	}
	destination := filepath.Join(filepath.Dir(source), name)
	if _, statErr := os.Lstat(destination); statErr == nil {
		return fmt.Errorf("destination %s already exists", destination)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	return os.Rename(source, destination)
}

func (m *Manager) RenamePodPath(
	ctx context.Context,
	target Target,
	rawSource, newName string,
) error {
	if err := validateTarget(target); err != nil {
		return err
	}
	if err := m.validatePodIdentity(ctx, target); err != nil {
		return err
	}
	name, err := validateEntryName(newName)
	if err != nil {
		return err
	}
	source, err := cleanRemotePath(rawSource)
	if err != nil {
		return err
	}
	if source == "/" {
		return errors.New("cannot rename the container root")
	}
	destination := path.Join(path.Dir(source), name)
	if _, statErr := m.remoteStat(ctx, target, destination); statErr == nil {
		return fmt.Errorf("destination %s already exists", destination)
	}
	if err := m.exec(
		ctx, target,
		"mv -- "+shellquote.Join(source)+" "+shellquote.Join(destination),
		nil, io.Discard,
	); err != nil {
		return fmt.Errorf("rename container path: %w", err)
	}
	return nil
}

func (m *Manager) DeleteLocalPath(rawPath string) error {
	cleaned, err := cleanLocalPath(rawPath)
	if err != nil {
		return err
	}
	if isLocalRoot(cleaned) {
		return errors.New("cannot delete a filesystem root")
	}
	return os.RemoveAll(cleaned)
}

func (m *Manager) DeletePodPath(ctx context.Context, target Target, rawPath string) error {
	if err := validateTarget(target); err != nil {
		return err
	}
	if err := m.validatePodIdentity(ctx, target); err != nil {
		return err
	}
	cleaned, err := cleanRemotePath(rawPath)
	if err != nil {
		return err
	}
	if cleaned == "/" {
		return errors.New("cannot delete the container root")
	}
	if err := m.exec(ctx, target, "rm -rf -- "+shellquote.Join(cleaned), nil, io.Discard); err != nil {
		return fmt.Errorf("delete container path: %w", err)
	}
	return nil
}
