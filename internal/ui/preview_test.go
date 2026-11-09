package ui

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/PatrickSteil/ytplay/internal/yt"
)

type previewFakes struct{ info, cover atomic.Int32 }

// previewModel is a wide terminal showing 20 search results, with fakes that
// count the cover and description lookups.
func previewModel(t *testing.T, f *previewFakes) Model {
	t.Helper()
	m := newTestModel()
	m.deps.Info = func(ctx context.Context, id string, _ int) (yt.Info, error) {
		f.info.Add(1)
		return yt.Info{Description: "about " + id, Views: 1500, Year: 2020}, nil
	}
	m.deps.Cover = func(ctx context.Context, id, _ string) (string, error) {
		f.cover.Add(1)
		return strings.TrimSpace(strings.Repeat("COVER-"+id+"\n", coverRows)), nil
	}
	m.previewDelay = 0
	m.width, m.height = 140, 40
	m, _ = send(m, "/", "x", "enter")
	m, cmd := update(m, searchDoneMsg{seq: m.searchSeq, query: "x", results: results(20)})
	return settle(m, cmd)
}

func TestSearchColumnIsSplitIntoResultsAndPreview(t *testing.T) {
	var f previewFakes
	m := previewModel(t, &f)
	out := m.View()
	for _, want := range []string{"Search", "Preview", "COVER-a", "about a", "1.5K views", "Song A"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}
	top := viewLines(m)[0]
	if strings.Count(top, "┌") != 3 {
		t.Errorf("only the top of the search column starts on the first line: %q", top)
	}
	if n := strings.Count(out, "┌"); n != 4 {
		t.Errorf("want 4 frames (3 columns, one split in two), got %d", n)
	}
}

func TestPreviewFollowsTheSelection(t *testing.T) {
	var f previewFakes
	m := previewModel(t, &f)
	for _, k := range []string{"j", "j"} {
		var cmd tea.Cmd
		m, cmd = send(m, k)
		m = settle(m, cmd)
	}
	out := m.View()
	if !strings.Contains(out, "COVER-c") || !strings.Contains(out, "about c") {
		t.Errorf("preview does not show the third result:\n%s", out)
	}
	m, cmd := send(m, "k")
	m = settle(m, cmd)
	if out := m.View(); !strings.Contains(out, "COVER-b") || !strings.Contains(out, "about b") {
		t.Errorf("preview does not show the second result:\n%s", out)
	}
}

func TestPreviewIsFetchedOnlyOnce(t *testing.T) {
	var f previewFakes
	m := previewModel(t, &f)
	for _, k := range []string{"j", "k", "j", "k"} {
		var cmd tea.Cmd
		m, cmd = send(m, k)
		m = settle(m, cmd)
	}
	if f.info.Load() != 2 || f.cover.Load() != 2 {
		t.Errorf("info=%d cover=%d lookups for two results, want 2 each", f.info.Load(), f.cover.Load())
	}
}

func TestPreviewWaitsForTheSelectionToRest(t *testing.T) {
	var f previewFakes
	m := previewModel(t, &f)
	// Pass over b and c quickly; their delayed messages arrive after the
	// selection has moved on to d.
	m, c1 := send(m, "j")
	m, c2 := send(m, "j")
	m, c3 := send(m, "j")
	m = settle(m, c1)
	m = settle(m, c2)
	if f.info.Load() != 1 || f.cover.Load() != 1 {
		t.Fatalf("stale previews were fetched: info=%d cover=%d", f.info.Load(), f.cover.Load())
	}
	m = settle(m, c3)
	if f.info.Load() != 2 || !strings.Contains(m.View(), "about d") {
		t.Errorf("settled selection not fetched: info=%d", f.info.Load())
	}
}

func TestPreviewShowsPlaceholderWhileLoading(t *testing.T) {
	var f previewFakes
	m := previewModel(t, &f)
	m, _ = send(m, "j") // do not run the fetch
	out := m.View()
	if !strings.Contains(out, "░") || !strings.Contains(out, "Loading description") || !strings.Contains(out, "Song B") {
		t.Errorf("loading state wrong:\n%s", out)
	}
}

func TestNoPreviewInStackedLayout(t *testing.T) {
	var f previewFakes
	m := previewModel(t, &f)
	m.width = 90
	if strings.Contains(m.View(), "Preview") {
		t.Error("stacked layout must not show a preview")
	}
	if _, cmd := send(m, "j"); cmd != nil {
		t.Error("nothing to fetch when the preview is not visible")
	}
}

func TestNoPreviewWithoutResults(t *testing.T) {
	m := at(newTestModel(), 140, 40)
	if strings.Contains(m.View(), "Preview") {
		t.Error("no results, so no preview box")
	}
}

func TestPreviewLayoutFillsTheScreen(t *testing.T) {
	var f previewFakes
	for _, size := range [][2]int{{108, 16}, {108, 22}, {120, 30}, {140, 40}, {200, 60}, {120, 12}} {
		w, h := size[0], size[1]
		m := at(previewModel(t, &f), w, h)
		m.scrollIntoView()
		ls := viewLines(m)
		if len(ls) != h {
			t.Errorf("%dx%d: %d lines", w, h, len(ls))
		}
		for i, l := range ls {
			if lw := lipgloss.Width(l); lw > w || (i < h-1 && lw != w) {
				t.Errorf("%dx%d: line %d is %d wide: %q", w, h, i, lw, l)
			}
		}
		m, _ = send(m, "j", "j")
		if top, _ := m.searchSplit(); m.selected < m.offset || m.selected >= m.offset+max(top-2, 1) {
			t.Errorf("%dx%d: selection %d outside the visible rows from %d", w, h, m.selected, m.offset)
		}
	}
}

func TestLeavingTheSelectionCancelsTheRunningLookup(t *testing.T) {
	canceled := make(chan struct{})
	m := newTestModel()
	m.width, m.height = 140, 40
	m.previewDelay = 0
	m.deps.Info = func(ctx context.Context, id string, _ int) (yt.Info, error) {
		if id == "a" {
			<-ctx.Done()
			close(canceled)
			return yt.Info{}, ctx.Err()
		}
		return yt.Info{}, nil
	}
	m.results = results(5)
	m, cmd := update(m, previewMsg{id: "a"})
	go cmd() // starts the lookup of a and blocks on it
	m.selected = 1
	m, _ = update(m, previewMsg{id: "b"})
	select {
	case <-canceled:
	case <-timeAfterShort():
		t.Fatal("the lookup for the result that was left was not cancelled")
	}
	if !m.infoAsked["b"] {
		t.Error("b should be marked as requested")
	}
}

func timeAfterShort() <-chan time.Time { return time.After(2 * time.Second) }
