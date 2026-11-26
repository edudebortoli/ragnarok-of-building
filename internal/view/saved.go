package view

import (
	"encoding/json"
	"errors"

	"ragnarok-of-building/internal/store"
)

// Saved builds live under SavedKey in the store: builds.json next to the
// program on the desktop (the same file and format the old web version
// used, so builds saved there still open).

// SavedKey is the saved builds' key in the store.
const SavedKey = "builds.json"

const maxSavedEntries = 2000

// SavedBuild is one saved build.
type SavedBuild struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Code  string `json:"code"`
	JobID int    `json:"j"`
	Job   string `json:"job"`
	Base  int    `json:"b"`
	JobLv int    `json:"jl"`
	At    int64  `json:"t"` // saved at, Unix ms
}

// LoadSaved reads the saved builds; a missing or broken file is an empty
// list. Entries without an id or a code are dropped.
func LoadSaved(s store.Store) []SavedBuild {
	if s == nil {
		return nil
	}
	b, err := s.Read(SavedKey)
	if err != nil {
		return nil
	}
	var raw []SavedBuild
	if json.Unmarshal(b, &raw) != nil {
		return nil
	}
	out := raw[:0]
	for _, e := range raw {
		if e.ID == "" || len(e.ID) > 64 || e.Code == "" || len(e.Code) > maxBuildBody*2 {
			continue
		}
		if len(e.Name) > 200 {
			e.Name = e.Name[:200]
		}
		out = append(out, e)
		if len(out) == maxSavedEntries {
			break
		}
	}
	return out
}

// WriteSaved replaces the saved builds.
func WriteSaved(s store.Store, list []SavedBuild) error {
	if s == nil {
		return errors.New("saved builds: no store")
	}
	if list == nil {
		list = []SavedBuild{}
	}
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return s.Write(SavedKey, b)
}
