package ui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCompletePath(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"afrika_toto.mp3", "afrika_other.mp3"} {
		os.WriteFile(filepath.Join(dir, n), nil, 0o644)
	}
	os.Mkdir(filepath.Join(dir, "Music"), 0o755)
	got, opts := completePath(dir + "/af")
	if got != dir+"/afrika_" || len(opts) != 2 {
		t.Errorf("got %q %v", got, opts)
	}
	got, _ = completePath(dir + "/Mu")
	if got != dir+"/Music/" {
		t.Errorf("dir completion: %q", got)
	}
}

func TestDefaultDownloadPath(t *testing.T) {
	if got := defaultDownloadPath("Toto - Africa (Official HD)"); got != "~/Music/toto_africa_official_hd.mp3" {
		t.Errorf("got %q", got)
	}
}

func TestDownloadPopup(t *testing.T) {
	dir := t.TempDir()
	var gotID, gotPath string
	m := withResults(t, 2)
	m.deps.Download = func(_ context.Context, id, path string) error {
		gotID, gotPath = id, path
		return nil
	}
	m, _ = send(m, "d")
	if m.mode != modeDownload {
		t.Fatalf("mode = %v, want download", m.mode)
	}
	m.dlInput.SetValue(filepath.Join(dir, "sub", "song.mp3"))
	m, cmd := send(m, "enter")
	if m.mode != modeResults || cmd == nil {
		t.Fatalf("mode = %v cmd = %v", m.mode, cmd)
	}
	msg := cmd()
	if _, ok := msg.(downloadDoneMsg); !ok && msg != nil {
		// tea.Batch is not used here, so the cmd yields the message directly.
		t.Fatalf("unexpected msg %T", msg)
	}
	if gotID != m.results[0].ID || gotPath != filepath.Join(dir, "sub", "song.mp3") {
		t.Errorf("download(%q, %q)", gotID, gotPath)
	}
}

func TestDownloadPopupEscAndOverwrite(t *testing.T) {
	m := withResults(t, 1)
	m.deps.Download = func(context.Context, string, string) error { return nil }
	m, _ = send(m, "d", "esc")
	if m.mode != modeResults {
		t.Errorf("esc: mode = %v", m.mode)
	}
	f := filepath.Join(t.TempDir(), "a.mp3")
	os.WriteFile(f, nil, 0o644)
	m, _ = send(m, "d")
	m.dlInput.SetValue(f)
	m, cmd := send(m, "enter")
	if m.mode != modeDownload || cmd != nil || m.dlWarn == "" {
		t.Errorf("existing file must ask first: mode=%v warn=%q", m.mode, m.dlWarn)
	}
	m, cmd = send(m, "enter")
	if m.mode != modeResults || cmd == nil {
		t.Errorf("second Enter should download")
	}
}
