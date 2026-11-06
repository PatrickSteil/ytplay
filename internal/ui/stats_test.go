package ui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/PatrickSteil/ytplay/internal/analyze"
	"github.com/PatrickSteil/ytplay/internal/yt"
)

// collect runs cmd (expanding batches) and returns every message produced.
// Cmds that block on a timer are abandoned after a short wait.
func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		if b, ok := msg.(tea.BatchMsg); ok {
			var out []tea.Msg
			for _, c := range b {
				out = append(out, collect(c)...)
			}
			return out
		}
		if msg == nil {
			return nil
		}
		return []tea.Msg{msg}
	case <-time.After(300 * time.Millisecond):
		return nil
	}
}

func pick[T any](msgs []tea.Msg) *T {
	for _, m := range msgs {
		if v, ok := m.(T); ok {
			return &v
		}
	}
	return nil
}

type statsFakes struct {
	infoCalls, analysisCalls atomic.Int32
	lastDuration             float64
}

func statsModel(f *statsFakes) Model {
	m := New(Deps{
		Player: &fakeActions{},
		Info: func(ctx context.Context, id string) (yt.Info, error) {
			f.infoCalls.Add(1)
			return yt.Info{Description: "desc of " + id}, nil
		},
		Analyze: func(ctx context.Context, id string, duration float64) (analyze.Result, error) {
			f.analysisCalls.Add(1)
			f.lastDuration = duration
			return analyze.Result{BPM: 116, Key: "F# minor", Camelot: "11A", KeyScore: .9}, nil
		},
	})
	m.statsDelay = 0
	m.width, m.height = 140, 40
	m.tracks = map[string]yt.Result{
		"a": {ID: "a", Title: "Alpha", Channel: "ChanA", Duration: 200},
		"b": {ID: "b", Title: "Bravo", Channel: "ChanB", Duration: 100},
	}
	return m
}

// startStats makes track `id` current and returns the cmd from that event.
func startStats(t *testing.T, m Model, pos string, ids ...string) (Model, tea.Cmd) {
	t.Helper()
	m, _ = update(m, plEvent(t, ids...))
	return update(m, propEvent("playlist-pos", pos))
}

var fullInfo = yt.Info{
	Description: "Publication date: 19 April 2013\nAlbum: Random Access Memories\n\nLabel: Daft Life, Columbia",
	Views:       1234567, Likes: 8900, Year: 2013,
	Comments: []yt.Comment{
		{Author: "@alice", Text: "1993 to 2021", Likes: 48000},
		{Author: "@bob", Text: "It's 2023 and this song still feels incredibly fresh and new!", Likes: 8700},
		{Author: "@carol", Text: "Probably the coolest outfits Daft Punk wore", Likes: 2700},
	},
}

func playingWithStats(t *testing.T, w, h int) Model {
	t.Helper()
	var f statsFakes
	m := statsModel(&f)
	m.width, m.height = w, h
	m, _ = startStats(t, m, "0", "*a", "b")
	m, _ = update(m, infoMsg{id: "a", info: fullInfo})
	m, _ = update(m, analysisMsg{id: "a", res: analyze.Result{BPM: 116, Key: "F# minor", Camelot: "11A", KeyScore: .9}})
	return m
}

func TestStatsAreFetchedWhenTheTrackChanges(t *testing.T) {
	var f statsFakes
	m := statsModel(&f)
	_, cmd := startStats(t, m, "0", "*a", "b")
	msgs := collect(cmd)
	info, an := pick[infoMsg](msgs), pick[analysisMsg](msgs)
	if info == nil || an == nil || info.id != "a" || an.id != "a" {
		t.Fatalf("expected both fetches for a, got %#v", msgs)
	}
	if f.infoCalls.Load() != 1 || f.analysisCalls.Load() != 1 || f.lastDuration != 200 {
		t.Errorf("calls info=%d analysis=%d duration=%v", f.infoCalls.Load(), f.analysisCalls.Load(), f.lastDuration)
	}
}

func TestStatsAreNotFetchedTwiceForTheSameTrack(t *testing.T) {
	var f statsFakes
	m := statsModel(&f)
	m, cmd := startStats(t, m, "0", "*a", "b")
	collect(cmd)
	m, _ = update(m, infoMsg{id: "a", info: fullInfo})
	m, _ = update(m, analysisMsg{id: "a"})
	m, cmd = update(m, propEvent("playlist-pos", "1")) // b
	collect(cmd)
	_, cmd = update(m, propEvent("playlist-pos", "0")) // back to a
	if pick[infoMsg](collect(cmd)) != nil || pick[analysisMsg](collect(cmd)) != nil {
		t.Error("a was fetched again")
	}
	if f.infoCalls.Load() != 2 || f.analysisCalls.Load() != 2 { // a once, b once
		t.Errorf("calls info=%d analysis=%d, want 2 each", f.infoCalls.Load(), f.analysisCalls.Load())
	}
}

func TestCancelledFetchIsRetriedLater(t *testing.T) {
	var f statsFakes
	m := statsModel(&f)
	m, _ = startStats(t, m, "0", "*a", "b")
	// The user skipped away before the lookup finished.
	m, _ = update(m, infoMsg{id: "a", err: context.Canceled})
	m, _ = update(m, analysisMsg{id: "a", err: context.Canceled})
	m, _ = update(m, propEvent("playlist-pos", "1"))
	_, cmd := update(m, propEvent("playlist-pos", "0"))
	if pick[infoMsg](collect(cmd)) == nil {
		t.Error("a cancelled lookup should be retried when the track comes back")
	}
}

func TestStatsFailureIsSilentAndNotRetried(t *testing.T) {
	var f statsFakes
	m := statsModel(&f)
	m, _ = startStats(t, m, "0", "*a", "b")
	m, _ = update(m, infoMsg{id: "a", err: errors.New("ERROR: unavailable")})
	m, _ = update(m, analysisMsg{id: "a", err: errors.New("no audio")})
	if m.status != "" {
		t.Errorf("a failed lookup must not show an error, got %q", m.status)
	}
	out := m.View()
	if strings.Contains(out, "analysing") || strings.Contains(out, "BPM") {
		t.Errorf("failed stats should leave no trace:\n%s", out)
	}
	m, _ = update(m, propEvent("playlist-pos", "1"))
	_, cmd := update(m, propEvent("playlist-pos", "0"))
	if pick[infoMsg](collect(cmd)) != nil {
		t.Error("failed lookup was retried")
	}
}

func TestWideLeftColumnShowsTheStats(t *testing.T) {
	out := playingWithStats(t, 140, 44).View()
	for _, want := range []string{
		"116 BPM", "F# minor", "11A",
		"1.2M views", "8.9K likes", "2013",
		"Album: Random Access Memories", "Label: Daft Life",
		"48K", "1993 to 2021", "8.7K", "It's 2023",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
}

func TestAnalysingIsShownWhileTheAnalysisRuns(t *testing.T) {
	var f statsFakes
	m := statsModel(&f)
	m, _ = startStats(t, m, "0", "*a", "b")
	if out := m.View(); !strings.Contains(out, "analysing") {
		t.Errorf("expected a placeholder while analysing:\n%s", out)
	}
}

func TestStatsNeverOverflowTheColumns(t *testing.T) {
	for _, size := range [][2]int{{100, 14}, {100, 18}, {120, 22}, {140, 30}, {200, 60}, {100, 8}} {
		m := playingWithStats(t, size[0], size[1])
		ls := viewLines(m)
		if len(ls) != size[1] {
			t.Errorf("%dx%d: %d lines", size[0], size[1], len(ls))
		}
		for i, l := range ls {
			if w := lipgloss.Width(l); w > size[0] {
				t.Errorf("%dx%d: line %d is %d wide", size[0], size[1], i, w)
			}
			if i < len(ls)-1 && lipgloss.Width(l) != size[0] {
				t.Errorf("%dx%d: box line %d is %d wide, want %d", size[0], size[1], i, lipgloss.Width(l), size[0])
			}
		}
	}
}

func TestStackedLayoutShowsTheSummaryLines(t *testing.T) {
	m := playingWithStats(t, 90, 40)
	out := m.View()
	for _, want := range []string{"116 BPM", "F# minor", "11A", "1.2M views"} {
		if !strings.Contains(out, want) {
			t.Errorf("stacked view missing %q:\n%s", want, out)
		}
	}
}

func TestStatsAreForTheCurrentTrackOnly(t *testing.T) {
	m := playingWithStats(t, 140, 44)
	m, _ = update(m, propEvent("playlist-pos", "1")) // b has no stats yet
	if out := m.View(); strings.Contains(out, "desc") || strings.Contains(out, "1.2M views") {
		t.Errorf("stats of the previous track are still shown:\n%s", out)
	}
}

func TestCompactNumbers(t *testing.T) {
	tests := map[int]string{
		0: "0", 7: "7", 999: "999", 1000: "1K", 1500: "1.5K", 8700: "8.7K", 9999: "10K",
		10000: "10K", 48000: "48K", 999999: "1M", 1234567: "1.2M", 25_000_000: "25M",
		1_400_000_000: "1.4B",
	}
	for n, want := range tests {
		if got := compact(n); got != want {
			t.Errorf("compact(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestWrapText(t *testing.T) {
	got := wrapText("one two three four five six\n\n  \nseven", 10, 10)
	want := []string{"one two", "three four", "five six", "seven"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q, want %q", got, want)
	}
	got = wrapText(strings.Repeat("word ", 40), 20, 3)
	if len(got) != 3 || !strings.HasSuffix(got[2], "…") {
		t.Errorf("truncation: %q", got)
	}
	got = wrapText("supercalifragilisticexpialidocious", 10, 5)
	for _, l := range got {
		if lipgloss.Width(l) > 10 {
			t.Errorf("long word not broken: %q", l)
		}
	}
	got = wrapText("日本語のテキストを折り返す", 10, 5)
	for _, l := range got {
		if lipgloss.Width(l) > 10 {
			t.Errorf("wide characters overflow: %q (%d)", l, lipgloss.Width(l))
		}
	}
	if got := wrapText("  \n\n", 10, 5); len(got) != 0 {
		t.Errorf("blank text: %q", got)
	}
	if got := wrapText("tab\tand \x1b[31mescape\x1b[0m", 30, 5); strings.ContainsAny(strings.Join(got, ""), "\t\x1b") {
		t.Errorf("control characters must be stripped: %q", got)
	}
}

func TestQuitCancelsPendingLookups(t *testing.T) {
	started := make(chan context.Context, 1)
	m := New(Deps{
		Player: &fakeActions{},
		Info: func(ctx context.Context, id string) (yt.Info, error) {
			started <- ctx
			<-ctx.Done() // a slow yt-dlp call
			return yt.Info{}, ctx.Err()
		},
	})
	m.statsDelay = 0
	m.tracks = map[string]yt.Result{"a": {ID: "a", Title: "Alpha"}}
	m, cmd := startStats(t, m, "0", "*a")
	go collect(cmd)
	var ctx context.Context
	select {
	case ctx = <-started:
	case <-time.After(time.Second):
		t.Fatal("lookup did not start")
	}
	_, quit := send(m, "Q")
	if !isQuit(quit) {
		t.Fatal("Q should quit")
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Error("quitting did not cancel the running lookup")
	}
}
