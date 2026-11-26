package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

// maxOpenFile caps what a file dialog reads (the replay limit is 16 MB;
// one byte more lets the window say it is too big).
const maxOpenFile = 16<<20 + 1

// openFile asks for a file with the system's dialog and reads it.
func openFile(title, filter string) (string, []byte, error) {
	path, err := openFileDialog(title, filter)
	if err != nil {
		return "", nil, err
	}
	return readFile(path)
}

func readFile(path string) (string, []byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxOpenFile))
	if err != nil {
		return "", nil, err
	}
	return filepath.Base(path), b, nil
}

// saveFile writes a file the player asked for next to the program and
// returns its full path.
func saveFile(name string, data []byte) (string, error) {
	if name != filepath.Base(name) {
		return "", errors.New("nome de arquivo inválido")
	}
	if err := os.WriteFile(name, data, 0o644); err != nil {
		return "", err
	}
	return filepath.Abs(name)
}
