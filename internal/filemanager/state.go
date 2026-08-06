package filemanager

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/fsatomic"
)

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
	return fsatomic.WriteFile(m.path, raw, 0o700, 0o600)
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
