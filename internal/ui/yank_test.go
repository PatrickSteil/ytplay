package ui

import (
	"errors"
	"strings"
	"testing"
)

// yankModel copies into got.
func yankModel(t *testing.T, got *[]string) Model {
	t.Helper()
	m := at(queueModel(t, 3), 140, 30)
	m.deps.Copy = func(text string) error { *got = append(*got, text); return nil }
	return m
}

func TestYankCopiesTheLinkOfThePlayingSong(t *testing.T) {
	var got []string
	m := doCmd(yankModel(t, &got), "yank")
	if len(got) != 1 || got[0] != "https://www.youtube.com/watch?v=e0" {
		t.Errorf("copied %q", got)
	}
	if !strings.Contains(m.status, "Copied") || !strings.Contains(m.status, "Title-e0") || m.statusIsErr {
		t.Errorf("status %q", m.status)
	}
}

func TestYankCopiesTheHighlightedSong(t *testing.T) {
	var got []string
	m := yankModel(t, &got)
	m, _ = send(m, "tab", "j") // the queue, second entry
	doCmd(m, "yank")
	if len(got) != 1 || got[0] != "https://www.youtube.com/watch?v=e2" {
		t.Errorf("queue: copied %q", got)
	}

	got = nil
	r := yankModel(t, &got)
	r.results, r.mode, r.selected = mixedResults(), modeResults, 1
	doCmd(r, "yank")
	if len(got) != 1 || got[0] != "https://soundcloud.com/u/t" {
		t.Errorf("results: copied %q", got)
	}

	got = nil
	d := yankModel(t, &got)
	d, _ = typeCommand(d, "dj")
	d.djCursor = 2
	doCmd(d, "yank")
	if len(got) != 1 || got[0] != "https://www.youtube.com/watch?v=e2" {
		t.Errorf("DJ popup: copied %q", got)
	}
}

func TestYankShortFormAndFailures(t *testing.T) {
	var got []string
	m := doCmd(yankModel(t, &got), "y")
	if len(got) != 1 {
		t.Errorf(":y copied %q", got)
	}
	m.deps.Copy = func(string) error { return errors.New("no clipboard tool") }
	m = doCmd(m, "yank")
	if !m.statusIsErr || !strings.Contains(m.status, "no clipboard tool") {
		t.Errorf("status %q (error %v)", m.status, m.statusIsErr)
	}
	m.deps.Copy = nil
	m = doCmd(m, "yank")
	if !m.statusIsErr || !strings.Contains(m.status, "not available") {
		t.Errorf("without a clipboard: %q", m.status)
	}
	idleModel := at(newTestModel(), 140, 30)
	idleModel.deps.Copy = func(string) error { return nil }
	idle := doCmd(idleModel, "yank")
	if !strings.Contains(idle.status, "Nothing to copy") {
		t.Errorf("nothing playing: %q", idle.status)
	}
}
