package view

import (
	"os"
	"path/filepath"
	"testing"

	"ragnarok-of-building/internal/store"
)

func TestSavedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SavedKey)
	s := store.Dir(dir)
	if got := LoadSaved(s); len(got) != 0 {
		t.Errorf("missing file: %v", got)
	}
	// The web version's format: entries without id or code are dropped.
	os.WriteFile(path, []byte(`[{"id":"a","code":"zabc","name":"Minha","j":4252,"job":"Cavaleiro Draconiano","b":250,"jl":50,"t":1700000000000},{"id":"","code":"z"},{"name":"no code","id":"b"}]`), 0o644)
	got := LoadSaved(s)
	if len(got) != 1 || got[0].ID != "a" || got[0].JobID != 4252 || got[0].Base != 250 || got[0].At != 1700000000000 {
		t.Fatalf("loaded %+v", got)
	}
	got = append(got, SavedBuild{ID: "c", Name: "Outra", Code: "zdef"})
	if err := WriteSaved(s, got); err != nil {
		t.Fatal(err)
	}
	if again := LoadSaved(s); len(again) != 2 || again[1].Name != "Outra" {
		t.Errorf("round trip: %+v", again)
	}
	os.WriteFile(path, []byte(`{"not":"a list"}`), 0o644)
	if got := LoadSaved(s); len(got) != 0 {
		t.Errorf("broken file: %v", got)
	}
}
