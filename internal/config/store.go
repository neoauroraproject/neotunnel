package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Dir is where NeoTunnel keeps its state. Overridable for tests and dev runs.
var Dir = envOr("NEOTUNNEL_DIR", "/etc/neotunnel")

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func statePath() string { return filepath.Join(Dir, "state.json") }

// Load reads the state, returning an empty state if none exists yet.
func Load() (*State, error) {
	b, err := os.ReadFile(statePath())
	if errors.Is(err, fs.ErrNotExist) {
		return &State{Schema: SchemaVersion}, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", statePath(), err)
	}
	if s.Schema > SchemaVersion {
		return nil, fmt.Errorf("state schema %d is newer than this binary (%d); upgrade neotunnel", s.Schema, SchemaVersion)
	}
	return &s, nil
}

// Save writes the state atomically (write temp, fsync, rename).
func Save(s *State) error {
	s.Schema = SchemaVersion
	if err := os.MkdirAll(Dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(Dir, ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), statePath())
}

// WithLock runs fn under the state lock, for other files kept in Dir.
func WithLock(fn func() error) error {
	unlock, err := lock()
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

// Update runs fn on the current state under an exclusive lock and saves the
// result if fn succeeds. The CLI and the daemon both go through here.
func Update(fn func(*State) error) (*State, error) {
	unlock, err := lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	s, err := Load()
	if err != nil {
		return nil, err
	}
	if err := fn(s); err != nil {
		return nil, err
	}
	if err := Save(s); err != nil {
		return nil, err
	}
	return s, nil
}
