package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDir(t *testing.T) {
	dir := t.TempDir()
	s := Dir(dir)
	if _, err := s.Read("data/ui.json"); err == nil {
		t.Error("missing key read")
	}
	if err := s.Write("data/ui.json", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if b, err := s.Read("data/ui.json"); err != nil || string(b) != "{}" {
		t.Errorf("read back %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "ui.json")); err != nil {
		t.Errorf("not at its path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "ui.json.tmp")); err == nil {
		t.Error("temporary file left")
	}
}
