package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileStore persists state to a JSON file on disk. It is simple but durable enough
// for the MVP and can be swapped with SQLite later without touching callers.
type FileStore struct {
	path string
	mu   sync.RWMutex
}

func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

func (f *FileStore) Load(ctx context.Context) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	data, err := os.ReadFile(f.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return NewState(), nil
		}
		return State{}, fmt.Errorf("read state file: %w", err)
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, fmt.Errorf("decode state file: %w", err)
	}

	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = s.CreatedAt
	}
	if s.Settings.ComposeProjectName == "" {
		s.Settings.ComposeProjectName = "tinyserve"
	}

	return s, nil
}

func (f *FileStore) Save(ctx context.Context, s State) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	s.Touch()

	if err := s.Validate(); err != nil {
		return err
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return fmt.Errorf("ensure state dir: %w", err)
	}

	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write temp state: %w", err)
	}
	if err := os.Rename(tmp, f.path); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}

	return nil
}
