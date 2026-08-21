package trafficinspect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/fengqi-dev/kube-loop/internal/fsatomic"
	"github.com/fengqi-dev/kube-loop/internal/userpaths"
)

const (
	protobufStoreVersion   = 1
	protobufMaxSourceSize  = 2 << 20
	protobufMaxSourceAll   = 16 << 20
	protobufMaxSourceFiles = 1_000
)

type persistedProtobufSources struct {
	Version int               `json:"version"`
	Sources map[string]string `json:"sources"`
}

// ProtobufSchemaStore persists the source set as one profile-scoped document.
// Keeping import paths as map keys lets protocompile resolve nested imports.
type ProtobufSchemaStore struct {
	mu      sync.RWMutex
	path    string
	decoder *ProtobufDecoder
	sources map[string]string
}

func NewProtobufSchemaStore(path string, decoder *ProtobufDecoder) (*ProtobufSchemaStore, error) {
	if decoder == nil {
		return nil, errors.New("protobuf decoder is unavailable")
	}
	if strings.TrimSpace(path) == "" {
		layout, err := userpaths.Default()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(layout.DataDir(), "traffic-inspection", "protobuf.json")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve protobuf schema path: %w", err)
	}
	return &ProtobufSchemaStore{path: absolute, decoder: decoder, sources: make(map[string]string)}, nil
}

func (s *ProtobufSchemaStore) Load(ctx context.Context) error {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read protobuf schemas: %w", err)
	}
	if len(raw) > protobufMaxSourceAll+(1<<20) {
		return errors.New("protobuf schema store exceeds 17 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var persisted persistedProtobufSources
	if err := decoder.Decode(&persisted); err != nil {
		return fmt.Errorf("decode protobuf schema store: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("protobuf schema store must contain one JSON document")
	}
	if persisted.Version != protobufStoreVersion {
		return fmt.Errorf("unsupported protobuf schema store version %d", persisted.Version)
	}
	return s.replace(ctx, persisted.Sources, false)
}

func (s *ProtobufSchemaStore) ReplaceDirectory(ctx context.Context, root string) error {
	sources, err := readProtoDirectory(root)
	if err != nil {
		return err
	}
	return s.replace(ctx, sources, true)
}

func (s *ProtobufSchemaStore) Files() []string {
	s.mu.RLock()
	files := make([]string, 0, len(s.sources))
	for path := range s.sources {
		files = append(files, path)
	}
	s.mu.RUnlock()
	sort.Strings(files)
	return files
}

func (s *ProtobufSchemaStore) replace(ctx context.Context, sources map[string]string, persist bool) error {
	validated := NewProtobufDecoder()
	if err := validated.ReplaceSources(ctx, sources); err != nil {
		return err
	}
	if persist {
		raw, err := json.MarshalIndent(persistedProtobufSources{Version: protobufStoreVersion, Sources: sources}, "", "  ")
		if err != nil {
			return errors.New("encode protobuf schemas")
		}
		raw = append(raw, '\n')
		if err := fsatomic.WriteFile(s.path, raw, 0o700, 0o600); err != nil {
			return fmt.Errorf("save protobuf schemas: %w", err)
		}
	}
	if err := s.decoder.ReplaceSources(ctx, sources); err != nil {
		return fmt.Errorf("activate protobuf schemas: %w", err)
	}
	s.mu.Lock()
	s.sources = cloneStringMap(sources)
	s.mu.Unlock()
	return nil
}

func readProtoDirectory(root string) (map[string]string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("protobuf source directory is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve protobuf source directory: %w", err)
	}
	sources := make(map[string]string)
	total := 0
	err = filepath.WalkDir(absolute, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".proto") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if len(sources) >= protobufMaxSourceFiles {
			return errors.New("protobuf source directory exceeds 1000 files")
		}
		if info.Size() > protobufMaxSourceSize {
			return fmt.Errorf("protobuf source %s exceeds 2 MiB", entry.Name())
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read protobuf source %s: %w", entry.Name(), err)
		}
		total += len(raw)
		if total > protobufMaxSourceAll {
			return errors.New("protobuf sources exceed 16 MiB")
		}
		relative, err := filepath.Rel(absolute, path)
		if err != nil {
			return fmt.Errorf("resolve protobuf import path: %w", err)
		}
		sources[filepath.ToSlash(relative)] = string(raw)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan protobuf source directory: %w", err)
	}
	if len(sources) == 0 {
		return nil, errors.New("protobuf source directory contains no .proto files")
	}
	return sources, nil
}

func cloneStringMap(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	maps.Copy(cloned, source)
	return cloned
}
