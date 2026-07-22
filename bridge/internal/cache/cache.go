// Package cache persists the normalized PHC project to disk so the bridge starts
// instantly and keeps serving when the STM is briefly unreachable. The cache
// contains room and device names, so it is treated as sensitive: it is written
// mode 0600 and never logged.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jtschneider/PHCRemoteControl/bridge/internal/domain"
)

const schemaVersion = 1

type cacheFile struct {
	SchemaVersion int            `json:"schemaVersion"`
	STMKey        string         `json:"stmKey"`
	Project       domain.Project `json:"project"`
}

// Store reads and writes one project cache under a state directory. A nil *Store
// is a valid no-op, so callers can treat "caching disabled" uniformly.
type Store struct {
	path   string
	stmKey string
}

// New returns a Store writing <stateDir>/project.json, keyed to a hash of the STM
// address so a cache from a different installation is never loaded. An empty
// stateDir returns nil (caching disabled).
func New(stateDir, stmAddress string) *Store {
	if stateDir == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(stmAddress))
	return &Store{
		path:   filepath.Join(stateDir, "project.json"),
		stmKey: hex.EncodeToString(sum[:]),
	}
}

// Load returns the cached project when present, current-schema, and keyed to the
// same STM. A missing, corrupt, or incompatible cache returns (zero, false, nil)
// so the caller simply re-downloads.
func (s *Store) Load() (domain.Project, bool, error) {
	if s == nil {
		return domain.Project{}, false, nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return domain.Project{}, false, nil
	}
	if err != nil {
		return domain.Project{}, false, fmt.Errorf("cache: reading %s: %w", s.path, err)
	}
	var f cacheFile
	if err := json.Unmarshal(data, &f); err != nil {
		return domain.Project{}, false, nil // corrupt: ignore and re-download
	}
	if f.SchemaVersion != schemaVersion || f.STMKey != s.stmKey {
		return domain.Project{}, false, nil // different schema or STM: ignore
	}
	return f.Project, true, nil
}

// Save atomically writes the project to the cache: a temp file in the same
// directory, fsync, then rename, at mode 0600. A nil Store is a no-op.
func (s *Store) Save(project domain.Project) error {
	if s == nil {
		return nil
	}
	data, err := json.Marshal(cacheFile{SchemaVersion: schemaVersion, STMKey: s.stmKey, Project: project})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), "project-*.tmp")
	if err != nil {
		return fmt.Errorf("cache: creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
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
	return os.Rename(tmpName, s.path)
}
