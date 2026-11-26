//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"ragnarok-of-building/internal/ui"
	"runtime"
	"strings"
)

// Linux and macOS pieces: messages on stderr and the system's file and
// folder pickers (zenity or kdialog on Linux, osascript on macOS).

func fatal(msg string) { fmt.Fprintln(os.Stderr, "ERRO:", msg) }

func singleInstance() bool { return true }

func focusExisting() {}

// darkTitleBar: the desktop draws the title bar with its own theme.
func darkTitleBar(bool) {}

// filterPair is one "name|pattern" entry of a Windows-style filter.
type filterPair struct{ name, patterns string }

// parseFilter reads "Replays (*.rrf)|*.rrf|All|*.*" into pairs, with the
// patterns separated by spaces ("*.a;*.b" -> "*.a *.b").
func parseFilter(filter string) []filterPair {
	parts := strings.Split(filter, "|")
	var out []filterPair
	for i := 0; i+1 < len(parts); i += 2 {
		out = append(out, filterPair{parts[i], strings.ReplaceAll(parts[i+1], ";", " ")})
	}
	return out
}

// openFileDialog asks for a file to open; filter is pairs of name and
// pattern separated by "|" ("Replays (*.rrf)|*.rrf").
func openFileDialog(title, filter string) (string, error) {
	pairs := parseFilter(filter)
	if runtime.GOOS == "darwin" {
		return runPicker("osascript", "-e", `POSIX path of (choose file with prompt "`+escapeAS(title)+`")`)
	}
	if _, err := exec.LookPath("zenity"); err == nil {
		args := []string{"--file-selection", "--title=" + title}
		for _, p := range pairs {
			args = append(args, "--file-filter="+p.name+" | "+p.patterns)
		}
		return runPicker("zenity", args...)
	}
	if _, err := exec.LookPath("kdialog"); err == nil {
		var kf []string
		for _, p := range pairs {
			kf = append(kf, p.patterns+"|"+p.name)
		}
		return runPicker("kdialog", "--title", title, "--getopenfilename", ".", strings.Join(kf, "\n"))
	}
	return "", errNoPicker
}

// pickFolder opens the system's folder picker.
var pickFolder = func() (string, error) {
	const title = "Escolha a pasta do Ragnarok (a que tem o data.grf)"
	if runtime.GOOS == "darwin" {
		return runPicker("osascript", "-e", `POSIX path of (choose folder with prompt "`+escapeAS(title)+`")`)
	}
	if _, err := exec.LookPath("zenity"); err == nil {
		return runPicker("zenity", "--file-selection", "--directory", "--title="+title)
	}
	if _, err := exec.LookPath("kdialog"); err == nil {
		return runPicker("kdialog", "--title", title, "--getexistingdirectory", ".")
	}
	return "", errNoPicker
}

var errNoPicker = errors.New("instale o zenity ou o kdialog para escolher arquivos")

// runPicker runs a dialog program and returns the path it printed; a
// cancelled dialog exits non-zero (taken as no file chosen).
func runPicker(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	path := strings.TrimRight(string(out), "\r\n")
	if err != nil || path == "" {
		return "", ui.ErrNoFile // cancelled
	}
	return path, nil
}

// escapeAS quotes a string for an AppleScript literal.
func escapeAS(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}
