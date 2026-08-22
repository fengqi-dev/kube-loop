package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/fengqi-dev/kube-loop/internal/fsatomic"
)

const maxStateBytes = 1 << 20

func (store *Store) load() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	state, err := readState(store.path)
	if err == nil {
		store.state = state
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return err
	}
	backup, backupErr := readState(store.path + ".bak")
	if backupErr != nil {
		return fmt.Errorf("load Server Profile store: %w", err)
	}
	store.state = backup
	store.recovered = true
	return nil
}

func (store *Store) saveLocked(next State) error {
	normalized, err := normalizeState(next)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return errors.New("encode Server Profile store")
	}
	raw = append(raw, '\n')
	if existing, err := os.ReadFile(store.path); err == nil {
		if _, decodeErr := decodeState(existing); decodeErr == nil {
			if err := fsatomic.WriteFile(store.path+".bak", existing, 0o700, 0o600); err != nil {
				return fmt.Errorf("backup Server Profile store: %w", err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("read existing Server Profile store")
	}
	if err := fsatomic.WriteFile(store.path, raw, 0o700, 0o600); err != nil {
		return fmt.Errorf("save Server Profile store: %w", err)
	}
	store.state = normalized
	store.recovered = false
	return nil
}

func readState(path string) (_ State, resultErr error) {
	file, err := os.Open(path)
	if err != nil {
		return State{}, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close Server Profile store: %w", err))
		}
	}()
	raw, err := io.ReadAll(io.LimitReader(file, maxStateBytes+1))
	if err != nil {
		return State{}, errors.New("read Server Profile store")
	}
	if len(raw) > maxStateBytes {
		return State{}, errors.New("server Profile store exceeds 1 MiB")
	}
	return decodeState(raw)
}

func decodeState(raw []byte) (State, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var state State
	if err := decoder.Decode(&state); err != nil {
		return State{}, errors.New("decode Server Profile store")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return State{}, errors.New("server Profile store must contain one JSON document")
	}
	return normalizeState(state)
}
