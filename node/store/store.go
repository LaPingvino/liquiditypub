// Package store is the persistence seam for a node's state. A node serializes
// its full snapshot to a Store after each state change and reloads it on start.
// The interface is deliberately a single opaque blob so the Cloudflare-D1
// (joop-n7j) and GAE (joop-3mu) profiles can back it with a KV row, a D1 blob,
// or Firestore document without the node caring how bytes are stored.
package store

import (
	"os"
	"path/filepath"
)

// Store persists and retrieves a node's serialized snapshot.
type Store interface {
	// Save atomically replaces the stored snapshot.
	Save(data []byte) error
	// Load returns the stored snapshot, or (nil, nil) if none exists yet.
	Load() ([]byte, error)
}

// Nop is a Store that discards writes — the default when persistence is off.
type Nop struct{}

func (Nop) Save([]byte) error     { return nil }
func (Nop) Load() ([]byte, error) { return nil, nil }

// File persists the snapshot to a single JSON file, writing via a temp file +
// rename so a crash mid-write never leaves a torn snapshot.
type File struct {
	Path string
}

func NewFile(path string) *File { return &File{Path: path} }

func (f *File) Save(data []byte) error {
	dir := filepath.Dir(f.Path)
	tmp, err := os.CreateTemp(dir, ".lpnode-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
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
	return os.Rename(tmpName, f.Path)
}

func (f *File) Load() ([]byte, error) {
	data, err := os.ReadFile(f.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return data, err
}
