package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/PatrickSteil/ytplay/internal/yt"
)

const (
	youtubeRed      = "\x1b[38;5;196m"
	soundcloudAmber = "\x1b[38;5;208m"
)

func TestSourceIconsAreOneCellInTheirOwnColours(t *testing.T) {
	withColours(t)
	y, s := sourceIcon(yt.YouTube), sourceIcon(yt.SoundCloud)
	if lipgloss.Width(y) != 1 || lipgloss.Width(s) != 1 {
		t.Errorf("icons are %d and %d cells wide, want 1", lipgloss.Width(y), lipgloss.Width(s))
	}
	if !strings.Contains(y, youtubeRed) || !strings.Contains(s, soundcloudAmber) || y == s {
		t.Errorf("icons %q %q: want red for YouTube, orange for SoundCloud", y, s)
	}
}

func mixedResults() []yt.Result {
	return []yt.Result{
		{ID: "abc", Title: "A YouTube song", Channel: "Chan", Duration: 200},
		{ID: "sc:u/t", Title: "A SoundCloud song", Channel: "uploader", Duration: 3600, Source: yt.SoundCloud},
	}
}

func TestResultRowsShowTheSourceAndKeepTheirWidth(t *testing.T) {
	withColours(t)
	m := at(newTestModel(), 140, 30)
	m.results, m.mode = mixedResults(), modeResults
	for w := 30; w <= 120; w += 5 {
		for i, r := range m.results {
			for _, sel := range []int{0, 1} {
				m.selected = sel
				for _, focused := range []bool{true, false} {
					row := m.resultRow(w, i, r, focused)
					if got := lipgloss.Width(row); got != w {
						t.Errorf("width %d row %d selected %d focused %v: %d cells", w, i, sel, focused, got)
					}
					want := map[yt.Source]string{yt.YouTube: youtubeRed, yt.SoundCloud: soundcloudAmber}[r.Source]
					if !strings.Contains(row, want) {
						t.Errorf("width %d row %d: no %q icon", w, i, want)
					}
				}
			}
		}
	}
	out := m.View()
	if !strings.Contains(out, youtubeRed) || !strings.Contains(out, soundcloudAmber) {
		t.Error("the screen lacks an icon")
	}
}

func TestQueueRowsAndTheDJPopupShowTheSourceToo(t *testing.T) {
	withColours(t)
	m := at(queueModel(t, 3), 140, 30)
	m.playlist[1].Filename = "https://soundcloud.com/u/t"
	rows := strings.Join(m.queueRows(60), "\n")
	if !strings.Contains(rows, soundcloudAmber) || !strings.Contains(rows, youtubeRed) {
		t.Errorf("the queue lacks an icon:\n%s", rows)
	}
	for _, l := range m.queueRows(60)[:2] {
		if got := lipgloss.Width(l); got != 60 {
			t.Errorf("a queue row is %d cells wide, want 60", got)
		}
	}
	m.djCursor = 1
	box := m.djBox()
	if popup := strings.Join(box, "\n"); !strings.Contains(popup, soundcloudAmber) || !strings.Contains(popup, youtubeRed) {
		t.Errorf("the DJ popup lacks an icon:\n%s", popup)
	}
	for i, l := range box {
		if got := lipgloss.Width(l); got != lipgloss.Width(box[0]) {
			t.Errorf("popup line %d is %d wide, the top is %d", i, got, lipgloss.Width(box[0]))
		}
	}
}

func TestOneSourceFailingKeepsTheOthersResultsAndNamesTheFailure(t *testing.T) {
	m := at(newTestModel(), 140, 30)
	m.searchSeq = 1
	next, _ := m.Update(searchDoneMsg{seq: 1, query: "x", results: mixedResults()[:1],
		failed: map[yt.Source]error{yt.SoundCloud: errors.New("no client id")}})
	got := next.(Model)
	if len(got.results) != 1 || got.mode != modeResults {
		t.Fatalf("results %v mode %v", got.results, got.mode)
	}
	if !got.statusIsErr || !strings.Contains(got.status, "SoundCloud") || !strings.Contains(got.status, "no client id") {
		t.Errorf("status %q (error %v), want the failure named", got.status, got.statusIsErr)
	}
	clean, _ := m.Update(searchDoneMsg{seq: 1, query: "x", results: mixedResults()})
	if s := clean.(Model).status; s != "" {
		t.Errorf("status %q after a search that worked", s)
	}
}

func TestSearchPassesBothSourcesAndTheFailuresOn(t *testing.T) {
	m := New(Deps{Search: func(ctx context.Context, q string) ([]yt.Result, map[yt.Source]error, error) {
		return mixedResults(), map[yt.Source]error{yt.YouTube: errors.New("offline")}, nil
	}})
	cmd := m.startSearch("daft punk")
	msg := cmd().(searchDoneMsg)
	if len(msg.results) != 2 || msg.failed[yt.YouTube] == nil {
		t.Errorf("msg %+v", msg)
	}
}
