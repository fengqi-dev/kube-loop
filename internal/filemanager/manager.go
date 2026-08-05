package filemanager

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/podssh"
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
	if err := m.exec(ctx, target, "ls -A1 -- "+shellQuote(remotePath), nil, &stdout); err != nil {
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
	if err := m.exec(ctx, target, "mkdir -- "+shellQuote(destination), nil, io.Discard); err != nil {
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
	quoted := shellQuote(destination)
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
		"mv -- "+shellQuote(source)+" "+shellQuote(destination),
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
	if err := m.exec(ctx, target, "rm -rf -- "+shellQuote(cleaned), nil, io.Discard); err != nil {
		return fmt.Errorf("delete container path: %w", err)
	}
	return nil
}

func (m *Manager) Start(ctx context.Context, request TransferRequest) (TransferTask, error) {
	if err := validateTarget(request.Target); err != nil {
		return TransferTask{}, err
	}
	if request.Direction != DirectionUpload && request.Direction != DirectionDownload {
		return TransferTask{}, errors.New("direction must be upload or download")
	}
	var source FileEntry
	var destination string
	var err error
	switch request.Direction {
	case DirectionUpload:
		source, err = localStat(request.SourcePath)
		if err == nil {
			destinationRoot, cleanErr := cleanRemotePath(request.DestinationDir)
			if cleanErr != nil {
				err = cleanErr
			} else {
				destination = path.Join(destinationRoot, source.Name)
			}
		}
	case DirectionDownload:
		sourcePath, cleanErr := cleanRemotePath(request.SourcePath)
		if cleanErr != nil {
			err = cleanErr
			break
		}
		source, err = m.remoteStat(ctx, request.Target, sourcePath)
		if err == nil {
			destinationRoot, absErr := filepath.Abs(filepath.Clean(request.DestinationDir))
			if absErr != nil {
				err = absErr
			} else {
				destination = filepath.Join(destinationRoot, path.Base(sourcePath))
			}
		}
	}
	if err != nil {
		return TransferTask{}, err
	}
	if source.Dir {
		if request.Direction == DirectionUpload && isLocalRoot(source.Path) {
			return TransferTask{}, errors.New("cannot upload a filesystem root")
		}
		if request.Direction == DirectionDownload && source.Path == "/" {
			return TransferTask{}, errors.New("cannot download the container root")
		}
	}
	if source.Dir && request.Direction == DirectionUpload {
		source.Size, err = localTreeSize(source.Path)
		if err != nil {
			return TransferTask{}, err
		}
	}
	if !request.Overwrite {
		if request.Direction == DirectionDownload {
			if _, statErr := os.Stat(destination); statErr == nil {
				return TransferTask{}, fmt.Errorf("destination %s already exists", destination)
			}
		} else if _, statErr := m.remoteStat(ctx, request.Target, destination); statErr == nil {
			return TransferTask{}, fmt.Errorf("destination %s already exists", destination)
		}
	}
	now := time.Now()
	id := fmt.Sprintf("transfer-%d-%d", now.UnixMilli(), m.nextID.Add(1))
	tempPath := destination + ".kubeloop-" + id + ".part"
	if request.Direction == DirectionUpload {
		tempPath = destination + ".kubeloop-upload-" + id + ".part"
	}
	if source.Dir {
		tempPath += "dir"
	}
	task := TransferTask{
		ID: id, Direction: request.Direction, Target: request.Target,
		SourcePath: source.Path, DestinationPath: destination, TempPath: tempPath,
		Directory: source.Dir, Status: StatusQueued, TotalBytes: source.Size,
		SourceModTime: source.ModTime,
		Overwrite:     request.Overwrite, CreatedAt: now, UpdatedAt: now,
	}
	m.mu.Lock()
	m.tasks[id] = &task
	_ = m.saveLocked()
	m.mu.Unlock()
	m.emit(task)
	m.launch(id)
	return task, nil
}

func (m *Manager) ListTransfers() []TransferTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]TransferTask, 0, len(m.tasks))
	for _, task := range m.tasks {
		items = append(items, *task)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items
}

func (m *Manager) Pause(id string) error {
	return m.stop(id, StatusPaused)
}

func (m *Manager) Cancel(id string) error {
	return m.stop(id, StatusCancelled)
}

func (m *Manager) stop(id string, status TaskStatus) error {
	m.mu.Lock()
	task := m.tasks[id]
	if task == nil {
		m.mu.Unlock()
		return fmt.Errorf("transfer %q not found", id)
	}
	if task.Status == StatusCompleted || task.Status == StatusCancelled {
		m.mu.Unlock()
		return nil
	}
	if cancel := m.cancels[id]; cancel != nil {
		cancel()
	}
	task.Status = status
	task.UpdatedAt = time.Now()
	copy := *task
	_ = m.saveLocked()
	m.mu.Unlock()
	m.emit(copy)
	return nil
}

func (m *Manager) Resume(id string) error {
	m.mu.Lock()
	task := m.tasks[id]
	if task == nil {
		m.mu.Unlock()
		return fmt.Errorf("transfer %q not found", id)
	}
	switch task.Status {
	case StatusPaused, StatusFailed, StatusStale:
	default:
		m.mu.Unlock()
		return fmt.Errorf("transfer %q cannot resume from %s", id, task.Status)
	}
	task.Status = StatusQueued
	task.Error = ""
	task.UpdatedAt = time.Now()
	copy := *task
	_ = m.saveLocked()
	m.mu.Unlock()
	m.emit(copy)
	m.launch(id)
	return nil
}

func (m *Manager) ClearHistory() error {
	m.mu.Lock()
	for id, task := range m.tasks {
		switch task.Status {
		case StatusCompleted, StatusCancelled, StatusFailed, StatusStale:
			delete(m.tasks, id)
		}
	}
	err := m.saveLocked()
	m.mu.Unlock()
	return err
}

func (m *Manager) Shutdown() {
	m.mu.Lock()
	for id, cancel := range m.cancels {
		cancel()
		if task := m.tasks[id]; task != nil && task.Status == StatusRunning {
			task.Status = StatusPaused
			task.UpdatedAt = time.Now()
		}
	}
	_ = m.saveLocked()
	m.mu.Unlock()
}

func (m *Manager) launch(id string) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	if previous := m.cancels[id]; previous != nil {
		previous()
	}
	m.cancels[id] = cancel
	m.mu.Unlock()
	go func() {
		select {
		case m.slots <- struct{}{}:
			defer func() { <-m.slots }()
		case <-ctx.Done():
			return
		}
		m.setStatus(id, StatusRunning, "")
		err := m.run(ctx, id)
		if err == nil {
			now := time.Now()
			m.mu.Lock()
			if task := m.tasks[id]; task != nil {
				task.Status = StatusCompleted
				if task.TotalBytes > 0 {
					task.DoneBytes = task.TotalBytes
				}
				task.CompletedAt = &now
				task.UpdatedAt = now
				copy := *task
				delete(m.cancels, id)
				_ = m.saveLocked()
				m.mu.Unlock()
				m.emit(copy)
				return
			}
			m.mu.Unlock()
			return
		}
		if errors.Is(err, context.Canceled) {
			return
		}
		status := StatusFailed
		if errors.Is(err, errSourceChanged) {
			status = StatusStale
		}
		m.setStatus(id, status, err.Error())
	}()
}

var errSourceChanged = errors.New("source file changed")

func (m *Manager) run(ctx context.Context, id string) error {
	task, err := m.task(id)
	if err != nil {
		return err
	}
	if err := m.validatePodIdentity(ctx, task.Target); err != nil {
		return fmt.Errorf("%w: %v", errSourceChanged, err)
	}
	if task.Directory {
		if task.Direction == DirectionDownload {
			return m.downloadDirectory(ctx, task)
		}
		return m.uploadDirectory(ctx, task)
	}
	if task.Direction == DirectionDownload {
		return m.download(ctx, task)
	}
	return m.upload(ctx, task)
}

func (m *Manager) download(ctx context.Context, task TransferTask) error {
	info, err := m.remoteStat(ctx, task.Target, task.SourcePath)
	if err != nil {
		return err
	}
	if info.Size != task.TotalBytes || !sameModTime(info.ModTime, task.SourceModTime) {
		return errSourceChanged
	}
	if err := os.MkdirAll(filepath.Dir(task.DestinationPath), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(task.TempPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	current, err := file.Stat()
	if err != nil {
		return err
	}
	offset := current.Size()
	if offset > task.TotalBytes {
		if err := file.Truncate(0); err != nil {
			return err
		}
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	m.updateProgress(task.ID, offset)
	script := "cat -- " + shellQuote(task.SourcePath)
	if offset > 0 {
		script = "tail -c +" + strconv.FormatInt(offset+1, 10) + " -- " + shellQuote(task.SourcePath)
	}
	writer := &progressWriter{
		writer: file, done: offset,
		update: func(done int64) { m.updateProgress(task.ID, done) },
	}
	if err := m.exec(ctx, task.Target, script, nil, writer); err != nil {
		return err
	}
	if writer.done != task.TotalBytes {
		return fmt.Errorf("download stopped at %d of %d bytes", writer.done, task.TotalBytes)
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	err = os.Rename(task.TempPath, task.DestinationPath)
	if err != nil && task.Overwrite {
		if removeErr := os.RemoveAll(task.DestinationPath); removeErr != nil {
			return removeErr
		}
		err = os.Rename(task.TempPath, task.DestinationPath)
	}
	return err
}

func (m *Manager) upload(ctx context.Context, task TransferTask) error {
	info, err := localStat(task.SourcePath)
	if err != nil {
		return err
	}
	if info.Size != task.TotalBytes || !sameModTime(info.ModTime, task.SourceModTime) {
		return errSourceChanged
	}
	offset := int64(0)
	if remote, statErr := m.remoteStat(ctx, task.Target, task.TempPath); statErr == nil {
		offset = remote.Size
	}
	if offset > task.TotalBytes {
		_ = m.exec(ctx, task.Target, "rm -f -- "+shellQuote(task.TempPath), nil, io.Discard)
		offset = 0
	}
	file, err := os.Open(task.SourcePath)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	m.updateProgress(task.ID, offset)
	operator := ">"
	if offset > 0 {
		operator = ">>"
	}
	parent := path.Dir(task.TempPath)
	script := "mkdir -p -- " + shellQuote(parent) + " && cat " + operator + " " + shellQuote(task.TempPath)
	reader := &progressReader{
		reader: file, done: offset,
		update: func(done int64) { m.updateProgress(task.ID, done) },
	}
	if err := m.exec(ctx, task.Target, script, reader, io.Discard); err != nil {
		return err
	}
	if reader.done != task.TotalBytes {
		return fmt.Errorf("upload stopped at %d of %d bytes", reader.done, task.TotalBytes)
	}
	if !task.Overwrite {
		if _, statErr := m.remoteStat(ctx, task.Target, task.DestinationPath); statErr == nil {
			return fmt.Errorf("destination %s already exists", task.DestinationPath)
		}
	}
	finalize := "mv -- " + shellQuote(task.TempPath) + " " + shellQuote(task.DestinationPath)
	if task.Overwrite {
		finalize = "rm -rf -- " + shellQuote(task.DestinationPath) + " && " + finalize
	}
	return m.exec(ctx, task.Target, finalize, nil, io.Discard)
}

func (m *Manager) downloadDirectory(ctx context.Context, task TransferTask) error {
	info, err := m.remoteStat(ctx, task.Target, task.SourcePath)
	if err != nil {
		return err
	}
	if !info.Dir || !sameModTime(info.ModTime, task.SourceModTime) {
		return errSourceChanged
	}
	if err := os.RemoveAll(task.TempPath); err != nil {
		return err
	}
	if err := os.MkdirAll(task.TempPath, 0o755); err != nil {
		return err
	}
	m.updateProgress(task.ID, 0)

	reader, writer := io.Pipe()
	result := make(chan error, 1)
	go func() {
		script := "tar cf - -C " + shellQuote(task.SourcePath) + " ."
		execErr := m.exec(ctx, task.Target, script, nil, writer)
		_ = writer.CloseWithError(execErr)
		result <- execErr
	}()

	extractErr := extractArchive(ctx, reader, task.TempPath, func(done int64) {
		m.updateProgress(task.ID, done)
	})
	if extractErr != nil {
		_ = reader.CloseWithError(extractErr)
	}
	execErr := <-result
	if extractErr != nil {
		return extractErr
	}
	if execErr != nil {
		return execErr
	}
	if !task.Overwrite {
		if _, statErr := os.Lstat(task.DestinationPath); statErr == nil {
			return fmt.Errorf("destination %s already exists", task.DestinationPath)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
	} else if err := os.RemoveAll(task.DestinationPath); err != nil {
		return err
	}
	return os.Rename(task.TempPath, task.DestinationPath)
}

func (m *Manager) uploadDirectory(ctx context.Context, task TransferTask) error {
	info, err := localStat(task.SourcePath)
	if err != nil {
		return err
	}
	if !info.Dir || !sameModTime(info.ModTime, task.SourceModTime) {
		return errSourceChanged
	}
	total, err := localTreeSize(task.SourcePath)
	if err != nil {
		return err
	}
	if total != task.TotalBytes {
		return errSourceChanged
	}
	m.updateProgress(task.ID, 0)

	reader, writer := io.Pipe()
	result := make(chan error, 1)
	go func() {
		archiveErr := writeArchive(ctx, task.SourcePath, writer, func(done int64) {
			m.updateProgress(task.ID, done)
		})
		_ = writer.CloseWithError(archiveErr)
		result <- archiveErr
	}()
	script := "rm -rf -- " + shellQuote(task.TempPath) +
		" && mkdir -p -- " + shellQuote(task.TempPath) +
		" && tar xf - -C " + shellQuote(task.TempPath)
	execErr := m.exec(ctx, task.Target, script, reader, io.Discard)
	if execErr != nil {
		_ = reader.CloseWithError(execErr)
	}
	archiveErr := <-result
	if execErr != nil {
		return execErr
	}
	if archiveErr != nil {
		return archiveErr
	}
	if !task.Overwrite {
		if _, statErr := m.remoteStat(ctx, task.Target, task.DestinationPath); statErr == nil {
			return fmt.Errorf("destination %s already exists", task.DestinationPath)
		}
	}
	finalize := "mv -- " + shellQuote(task.TempPath) + " " + shellQuote(task.DestinationPath)
	if task.Overwrite {
		finalize = "rm -rf -- " + shellQuote(task.DestinationPath) + " && " + finalize
	}
	return m.exec(ctx, task.Target, finalize, nil, io.Discard)
}

func (m *Manager) remoteStat(ctx context.Context, target Target, remotePath string) (FileEntry, error) {
	cleaned, err := cleanRemotePath(remotePath)
	if err != nil {
		return FileEntry{}, err
	}
	parent, base := path.Split(cleaned)
	if parent == "" {
		parent = "/"
	}
	reader, writer := io.Pipe()
	result := make(chan error, 1)
	go func() {
		err := m.executor.Exec(ctx, podTarget(target), []string{
			"/bin/sh", "-c",
			"tar cf - --no-recursion -C " + shellQuote(parent) + " " + shellQuote("./"+base),
		}, podssh.Streams{Stdout: writer, Stderr: io.Discard})
		_ = writer.CloseWithError(err)
		result <- err
	}()
	archive := tar.NewReader(reader)
	header, readErr := archive.Next()
	if readErr == nil {
		_, _ = io.Copy(io.Discard, archive)
	}
	_, _ = io.Copy(io.Discard, reader)
	execErr := <-result
	if execErr != nil {
		return FileEntry{}, execErr
	}
	if readErr != nil {
		return FileEntry{}, readErr
	}
	return FileEntry{
		Name: path.Base(cleaned), Path: cleaned, Dir: header.FileInfo().IsDir(),
		Size: header.Size, Mode: uint32(header.FileInfo().Mode().Perm()),
		ModTime: header.ModTime,
	}, nil
}

func (m *Manager) exec(
	ctx context.Context,
	target Target,
	script string,
	stdin io.Reader,
	stdout io.Writer,
) error {
	var stderr bytes.Buffer
	err := m.executor.Exec(ctx, podTarget(target), []string{"/bin/sh", "-c", script}, podssh.Streams{
		Stdin: stdin, Stdout: stdout, Stderr: &stderr,
	})
	if err != nil && strings.TrimSpace(stderr.String()) != "" {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return err
}

func (m *Manager) task(id string) (TransferTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.tasks[id]
	if task == nil {
		return TransferTask{}, fmt.Errorf("transfer %q not found", id)
	}
	return *task, nil
}

func (m *Manager) setStatus(id string, status TaskStatus, message string) {
	m.mu.Lock()
	task := m.tasks[id]
	if task == nil {
		m.mu.Unlock()
		return
	}
	task.Status = status
	task.Error = message
	task.UpdatedAt = time.Now()
	copy := *task
	_ = m.saveLocked()
	m.mu.Unlock()
	m.emit(copy)
}

func (m *Manager) updateProgress(id string, done int64) {
	m.mu.Lock()
	task := m.tasks[id]
	if task == nil {
		m.mu.Unlock()
		return
	}
	task.DoneBytes = done
	task.UpdatedAt = time.Now()
	copy := *task
	_ = m.saveLocked()
	m.mu.Unlock()
	m.emit(copy)
}

func (m *Manager) emit(task TransferTask) {
	m.mu.Lock()
	subscribers := append([]func(TransferTask){}, m.subscribers...)
	m.mu.Unlock()
	for _, subscriber := range subscribers {
		subscriber(task)
	}
}

func (m *Manager) load() error {
	if m.path == "" {
		return nil
	}
	raw, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var state persistedState
	if err := json.Unmarshal(raw, &state); err != nil {
		return err
	}
	for i := range state.Tasks {
		task := state.Tasks[i]
		if task.Status == StatusRunning || task.Status == StatusQueued {
			task.Status = StatusPaused
		}
		m.tasks[task.ID] = &task
	}
	return nil
}

func (m *Manager) saveLocked() error {
	if m.path == "" {
		return nil
	}
	state := persistedState{Version: stateVersion}
	for _, task := range m.tasks {
		state.Tasks = append(state.Tasks, *task)
	}
	sort.Slice(state.Tasks, func(i, j int) bool {
		return state.Tasks[i].CreatedAt.Before(state.Tasks[j].CreatedAt)
	})
	if len(state.Tasks) > 500 {
		state.Tasks = state.Tasks[len(state.Tasks)-500:]
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(m.path), ".transfers-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(raw); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, m.path)
}

type progressWriter struct {
	writer io.Writer
	done   int64
	update func(int64)
	last   time.Time
}

func (w *progressWriter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	w.done += int64(n)
	if time.Since(w.last) >= 250*time.Millisecond || err != nil {
		w.last = time.Now()
		w.update(w.done)
	}
	return n, err
}

type progressReader struct {
	reader io.Reader
	done   int64
	update func(int64)
	last   time.Time
}

func (r *progressReader) Read(data []byte) (int, error) {
	n, err := r.reader.Read(data)
	r.done += int64(n)
	if time.Since(r.last) >= 250*time.Millisecond || err != nil {
		r.last = time.Now()
		r.update(r.done)
	}
	return n, err
}

func localStat(rawPath string) (FileEntry, error) {
	cleaned, err := filepath.Abs(filepath.Clean(rawPath))
	if err != nil {
		return FileEntry{}, err
	}
	info, err := os.Lstat(cleaned)
	if err != nil {
		return FileEntry{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return FileEntry{}, errors.New("symbolic links are not supported")
	}
	return FileEntry{
		Name: info.Name(), Path: cleaned, Dir: info.IsDir(), Size: info.Size(),
		Mode: uint32(info.Mode().Perm()), ModTime: info.ModTime(),
	}, nil
}

func localTreeSize(root string) (int64, error) {
	var total int64
	err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic link %q is not supported", info.Name())
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func writeArchive(
	ctx context.Context,
	root string,
	output io.Writer,
	update func(int64),
) error {
	archive := tar.NewWriter(output)
	var done int64
	err := filepath.Walk(root, func(current string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic link %q is not supported", relative)
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("special file %q is not supported", relative)
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		if info.IsDir() {
			header.Name += "/"
		}
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		file, err := os.Open(current)
		if err != nil {
			return err
		}
		reader := &progressReader{reader: file, done: done, update: update}
		_, copyErr := io.Copy(archive, reader)
		closeErr := file.Close()
		done = reader.done
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		_ = archive.Close()
		return err
	}
	return archive.Close()
}

func extractArchive(
	ctx context.Context,
	input io.Reader,
	root string,
	update func(int64),
) error {
	archive := tar.NewReader(input)
	var done int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeArchiveTarget(root, header.Name)
		if err != nil {
			return err
		}
		if target == root {
			continue
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)&0o777); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(
				target,
				os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
				os.FileMode(header.Mode)&0o777,
			)
			if err != nil {
				return err
			}
			writer := &progressWriter{writer: file, done: done, update: update}
			_, copyErr := io.Copy(writer, archive)
			closeErr := file.Close()
			done = writer.done
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("archive entry %q has unsupported type", header.Name)
		}
	}
}

func safeArchiveTarget(root, name string) (string, error) {
	cleaned := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if cleaned == "." {
		return root, nil
	}
	if path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("archive entry %q escapes the destination", name)
	}
	target := filepath.Join(root, filepath.FromSlash(cleaned))
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive entry %q escapes the destination", name)
	}
	return target, nil
}

func sortEntries(items []FileEntry) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Dir != items[j].Dir {
			return items[i].Dir
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
}

func validateTarget(target Target) error {
	if target.Context == "" || target.Namespace == "" || target.Pod == "" || target.Container == "" {
		return errors.New("context, namespace, pod, and container are required")
	}
	return nil
}

func validateEntryName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || name == "." || name == ".." ||
		strings.ContainsAny(name, `/\`) {
		return "", errors.New("name must be a single non-empty path component")
	}
	return name, nil
}

func cleanLocalPath(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("local path is required")
	}
	return filepath.Abs(filepath.Clean(raw))
}

func isLocalRoot(cleaned string) bool {
	return filepath.Dir(cleaned) == cleaned
}

func (m *Manager) validatePodIdentity(ctx context.Context, target Target) error {
	if target.PodUID == "" || m.catalog == nil {
		return nil
	}
	pods, err := m.catalog.ListPods(ctx, target.Context, target.Namespace)
	if err != nil {
		return fmt.Errorf("verify Pod identity: %w", err)
	}
	for _, pod := range pods {
		if pod.Namespace == target.Namespace && pod.Name == target.Pod {
			if pod.UID != target.PodUID {
				return errors.New("Pod was replaced")
			}
			if !pod.Ready {
				return errors.New("Pod is not ready")
			}
			for _, container := range pod.Containers {
				if container == target.Container {
					return nil
				}
			}
			return errors.New("container no longer exists")
		}
	}
	return errors.New("Pod no longer exists")
}

func cleanRemotePath(raw string) (string, error) {
	cleaned := path.Clean(strings.TrimSpace(raw))
	if !path.IsAbs(cleaned) {
		return "", errors.New("container path must be absolute")
	}
	return cleaned, nil
}

func podTarget(target Target) podssh.Target {
	return podssh.Target{
		Context: target.Context, Namespace: target.Namespace,
		Pod: target.Pod, Container: target.Container,
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func sameModTime(left, right time.Time) bool {
	return left.Unix() == right.Unix()
}
