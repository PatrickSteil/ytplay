package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PatrickSteil/ytplay/internal/yt"
)

func newTestModel() Model {
	m := New(Deps{
		Search: func(ctx context.Context, q string) ([]yt.Result, error) { return nil, nil },
		Player: &fakeActions{},
	})
	m.width, m.height = 90, 40
	return m
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func send(m Model, keys ...string) (Model, tea.Cmd) {
	var cmd tea.Cmd
	for _, k := range keys {
		var next tea.Model
		next, cmd = m.Update(key(k))
		m = next.(Model)
	}
	return m, cmd
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func results(n int) []yt.Result {
	var r []yt.Result
	for i := 0; i < n; i++ {
		r = append(r, yt.Result{ID: string(rune('a' + i)), Title: "Song " + string(rune('A'+i)), Channel: "Chan", Duration: 100})
	}
	return r
}

func withResults(t *testing.T, n int) Model {
	t.Helper()
	m := newTestModel()
	m, _ = send(m, "/", "x", "enter")
	next, _ := m.Update(searchDoneMsg{seq: m.searchSeq, query: "x", results: results(n)})
	m = next.(Model)
	if m.mode != modeResults {
		t.Fatalf("mode = %v, want results", m.mode)
	}
	return m
}

func TestSearchBarAcceptsDigitsQSAndSpaces(t *testing.T) {
	m := newTestModel()
	m, _ = send(m, "/")
	if m.mode != modeSearch {
		t.Fatalf("mode = %v, want search", m.mode)
	}
	m, _ = send(m, "q", " ", "s", "5", "=")
	if got := m.input.Value(); got != "q s5=" {
		t.Errorf("input = %q, want %q", got, "q s5=")
	}
	if m.mode != modeSearch {
		t.Errorf("mode = %v, want search", m.mode)
	}
}

func TestSearchBarQuitKeyIsTypable(t *testing.T) {
	m := newTestModel()
	m, cmd := send(m, "/", "Q")
	if isQuit(cmd) {
		t.Fatal("Q must not quit from the search bar")
	}
	if m.input.Value() != "Q" {
		t.Errorf("input = %q", m.input.Value())
	}
}

func TestCtrlCQuitsEverywhere(t *testing.T) {
	m := newTestModel()
	if _, cmd := send(m, "ctrl+c"); !isQuit(cmd) {
		t.Error("ctrl+c should quit in normal mode")
	}
	if _, cmd := send(m, "/", "ctrl+c"); !isQuit(cmd) {
		t.Error("ctrl+c should quit in search mode")
	}
	r := withResults(t, 3)
	if _, cmd := send(r, "ctrl+c"); !isQuit(cmd) {
		t.Error("ctrl+c should quit in results mode")
	}
}

func TestCapitalQQuitsInNormalAndResults(t *testing.T) {
	m := newTestModel()
	if _, cmd := send(m, "Q"); !isQuit(cmd) {
		t.Error("Q should quit in normal mode")
	}
	if _, cmd := send(withResults(t, 2), "Q"); !isQuit(cmd) {
		t.Error("Q should quit in results mode")
	}
}

func TestEnterWithEmptyQueryDoesNothing(t *testing.T) {
	m := newTestModel()
	m, _ = send(m, "/")
	m, cmd := send(m, " ", " ", "enter")
	if cmd != nil {
		t.Error("blank query must not start a search")
	}
	if m.mode != modeSearch {
		t.Errorf("mode = %v", m.mode)
	}
}

func TestEnterRunsSearchAndShowsSearching(t *testing.T) {
	var gotQuery string
	m := New(Deps{Search: func(ctx context.Context, q string) ([]yt.Result, error) {
		gotQuery = q
		return results(2), nil
	}})
	m.width, m.height = 90, 40
	m, _ = send(m, "/", "d", "a", "f", "t")
	m, cmd := send(m, "enter")
	if cmd == nil {
		t.Fatal("expected a search cmd")
	}
	if !strings.Contains(m.status, "Searching") {
		t.Errorf("status = %q", m.status)
	}
	// Execute the batch/cmd chain looking for searchDoneMsg.
	msg := findMsg[searchDoneMsg](cmd)
	if msg == nil {
		t.Fatal("no searchDoneMsg produced")
	}
	if gotQuery != "daft" || msg.query != "daft" || len(msg.results) != 2 {
		t.Errorf("query=%q msg=%+v", gotQuery, msg)
	}
}

// findMsg runs cmd (expanding batches) and returns the first message of type T.
func findMsg[T any](cmd tea.Cmd) *T {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if v, ok := msg.(T); ok {
		return &v
	}
	if b, ok := msg.(tea.BatchMsg); ok {
		for _, c := range b {
			if v := findMsg[T](c); v != nil {
				return v
			}
		}
	}
	return nil
}

func TestSearchDoneFillsResultsAndSwitchesMode(t *testing.T) {
	m := withResults(t, 3)
	if len(m.results) != 3 || m.selected != 0 {
		t.Errorf("results=%d selected=%d", len(m.results), m.selected)
	}
}

func TestStaleSearchResultIgnored(t *testing.T) {
	m := newTestModel()
	m, _ = send(m, "/", "a", "enter")
	oldSeq := m.searchSeq
	m, _ = send(m, "b", "enter")
	next, _ := m.Update(searchDoneMsg{seq: oldSeq, query: "a", results: results(2)})
	m = next.(Model)
	if len(m.results) != 0 || m.mode != modeSearch {
		t.Errorf("stale result applied: results=%d mode=%v", len(m.results), m.mode)
	}
}

func TestEmptyResultsStaysInSearch(t *testing.T) {
	m := newTestModel()
	m, _ = send(m, "/", "zzz", "enter")
	next, _ := m.Update(searchDoneMsg{seq: m.searchSeq, query: "zzz"})
	m = next.(Model)
	if m.mode != modeSearch {
		t.Errorf("mode = %v, want search", m.mode)
	}
	if !strings.Contains(m.status, "No results for") || !strings.Contains(m.status, "zzz") {
		t.Errorf("status = %q", m.status)
	}
}

func TestSearchErrorKeepsOldResults(t *testing.T) {
	m := withResults(t, 3)
	m, _ = send(m, "/", "backspace", "y", "enter")
	next, _ := m.Update(searchDoneMsg{seq: m.searchSeq, query: "y", err: errors.New("yt-dlp: boom")})
	m = next.(Model)
	if len(m.results) != 3 {
		t.Errorf("old results lost: %d", len(m.results))
	}
	if !strings.Contains(m.status, "Search failed: yt-dlp: boom") {
		t.Errorf("status = %q", m.status)
	}
}

func TestEscInSearchBar(t *testing.T) {
	m := newTestModel()
	m, _ = send(m, "/", "esc")
	if m.mode != modeNormal {
		t.Errorf("no results: mode = %v, want normal", m.mode)
	}
	r := withResults(t, 2)
	r, _ = send(r, "/", "esc")
	if r.mode != modeResults {
		t.Errorf("with results: mode = %v, want results", r.mode)
	}
	if len(r.results) != 2 {
		t.Error("results should be kept")
	}
}

func TestResultsNavigationAndEsc(t *testing.T) {
	m := withResults(t, 3)
	m, _ = send(m, "j", "j", "j")
	if m.selected != 2 {
		t.Errorf("selected = %d, want 2 (clamped)", m.selected)
	}
	m, _ = send(m, "k")
	if m.selected != 1 {
		t.Errorf("selected = %d, want 1", m.selected)
	}
	m, _ = send(m, "k", "k", "k")
	if m.selected != 0 {
		t.Errorf("selected = %d, want 0 (clamped)", m.selected)
	}
	m, _ = send(m, "down", "up")
	if m.selected != 0 {
		t.Errorf("arrow keys must not move the results, selected = %d", m.selected)
	}
	m, _ = send(m, "esc")
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want normal", m.mode)
	}
}

func TestSlashReopensSearchWithPreviousQueryAndFirstKeyReplaces(t *testing.T) {
	m := withResults(t, 2)
	m, _ = send(m, "/")
	if m.input.Value() != "x" {
		t.Fatalf("prefill = %q, want x", m.input.Value())
	}
	m, _ = send(m, "n", "e", "w")
	if m.input.Value() != "new" {
		t.Errorf("typing over the pre-filled query: input = %q, want new", m.input.Value())
	}
	// Backspace on the pre-filled query clears it entirely.
	m, _ = send(m, "enter") // run search for "new"
	next, _ := m.Update(searchDoneMsg{seq: m.searchSeq, query: "new", results: results(1)})
	m = next.(Model)
	m, _ = send(m, "/", "backspace")
	if m.input.Value() != "" {
		t.Errorf("backspace on selected text: input = %q, want empty", m.input.Value())
	}
}

func TestViewDoesNotExceedTerminalSize(t *testing.T) {
	m := withResults(t, 15)
	for _, size := range [][2]int{{100, 40}, {80, 24}, {50, 20}, {30, 10}} {
		m.width, m.height = size[0], size[1]
		out := m.View()
		lines := strings.Split(out, "\n")
		if len(lines) > size[1] {
			t.Errorf("%dx%d: view has %d lines", size[0], size[1], len(lines))
		}
	}
}
