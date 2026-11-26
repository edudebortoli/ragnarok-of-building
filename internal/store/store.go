// Package store keeps the program's small files (window layout, saved
// builds) by name, so the same code runs where there is a folder (the
// desktop program) and where there is none (the browser).
package store

import (
	"os"
	"path/filepath"
)

// Store reads and writes whole files by key ("builds.json",
// "data/ui.json"). Read of a missing key returns an error.
type Store interface {
	Read(key string) ([]byte, error)
	Write(key string, b []byte) error
}

// Dir keeps each key as a file under dir; writes go through a temporary
// file, so a crash never leaves half a file.
func Dir(dir string) Store { return dirStore(dir) }

type dirStore string

func (d dirStore) path(key string) string { return filepath.Join(string(d), filepath.FromSlash(key)) }

func (d dirStore) Read(key string) ([]byte, error) { return os.ReadFile(d.path(key)) }

func (d dirStore) Write(key string, b []byte) error {
	p := d.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
