package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTool puts a script called name on PATH that writes its stdin to a file.
func fakeTool(t *testing.T, name string) (out string) {
	t.Helper()
	dir := t.TempDir()
	out = filepath.Join(dir, "copied")
	cat, err := exec.LookPath("cat") // the script runs with PATH set to its own directory
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" + cat + " > " + out + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return out
}

func TestCopyUsesTheSystemClipboardTool(t *testing.T) {
	out := fakeTool(t, "wl-copy")
	var term bytes.Buffer
	if err := copyToClipboard("https://example.com/x", &term); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(out); string(got) != "https://example.com/x" {
		t.Errorf("tool got %q", got)
	}
	if term.Len() != 0 {
		t.Errorf("the terminal was written to as well: %q", term.String())
	}
}

func TestCopyFallsBackToTheTerminalClipboard(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no tool at all
	var term bytes.Buffer
	if err := copyToClipboard("hello", &term); err != nil {
		t.Fatal(err)
	}
	// OSC 52: ESC ] 52 ; c ; base64 BEL (or ST)
	if s := term.String(); !strings.HasPrefix(s, "\x1b]52;c;aGVsbG8=") {
		t.Errorf("terminal got %q", s)
	}
}
