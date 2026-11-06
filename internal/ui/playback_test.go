package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PatrickSteil/ytplay/internal/mpv"
	"github.com/PatrickSteil/ytplay/internal/yt"
)

type fakeActions struct {
	calls   []string
	seekErr error
}

func (f *fakeActions) rec(s string) error { f.calls = append(f.calls, s); return nil }
func (f *fakeActions) Queue(ctx context.Context, url string) error {
	return f.rec("queue " + url)
}
func (f *fakeActions) PlayNow(ctx context.Context, url string) error {
	return f.rec("playnow " + url)
}
func (f *fakeActions) Skip(ctx context.Context) error { return f.rec("skip") }
func (f *fakeActions) SeekPercent(ctx context.Context, n int) error {
	f.rec(fmt.Sprintf("seek %d", n))
	return f.seekErr
}
func (f *fakeActions) TogglePause(ctx context.Context) error { return f.rec("pause") }
func (f *fakeActions) QueueMany(ctx context.Context, urls []string) error {
	return f.rec("queuemany " + strings.Join(urls, ","))
}
func (f *fakeActions) ClearQueue(ctx context.Context) error { return f.rec("clear") }
func (f *fakeActions) Remove(ctx context.Context, i int) error {
	return f.rec(fmt.Sprintf("remove %d", i))
}
func (f *fakeActions) MoveUp(ctx context.Context, i int) error {
	return f.rec(fmt.Sprintf("moveup %d", i))
}
func (f *fakeActions) MoveDown(ctx context.Context, i int) error {
	return f.rec(fmt.Sprintf("movedown %d", i))
}

func actionsOf(m Model) *fakeActions { return m.deps.Player.(*fakeActions) }

// run executes a cmd, expanding batches, and feeds the resulting messages back
// to the model. Cmds that block on a timer (tea.Tick) are abandoned after a
// short wait.
func run(m Model, cmd tea.Cmd) Model {
	if cmd == nil {
		return m
	}
	msgCh := make(chan tea.Msg, 1)
	go func() { msgCh <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-msgCh:
	case <-time.After(100 * time.Millisecond):
		return m
	}
	switch msg := msg.(type) {
	case nil:
		return m
	case tea.BatchMsg:
		for _, c := range msg {
			m = run(m, c)
		}
		return m
	default:
		next, _ := m.Update(msg)
		return next.(Model)
	}
}

func plEvent(t *testing.T, entries ...string) MpvEventMsg {
	t.Helper()
	// entries are "id" or "*id" (current)
	var parts []string
	for _, e := range entries {
		cur := ""
		if strings.HasPrefix(e, "*") {
			cur = `,"current":true,"playing":true`
			e = e[1:]
		}
		parts = append(parts, fmt.Sprintf(`{"filename":"https://www.youtube.com/watch?v=%s"%s}`, e, cur))
	}
	return MpvEventMsg{mpv.Event{Name: "property-change", Property: "playlist", Data: json.RawMessage("[" + strings.Join(parts, ",") + "]")}}
}

func propEvent(name, data string) MpvEventMsg {
	return MpvEventMsg{mpv.Event{Name: "property-change", Property: name, Data: json.RawMessage(data)}}
}

func update(m Model, msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

func TestEnterPlaysSelectedAndKeepsResultsOpen(t *testing.T) {
	m := withResults(t, 3)
	m, _ = send(m, "j")
	m, cmd := send(m, "enter")
	m = run(m, cmd)
	if got := actionsOf(m).calls; len(got) != 1 || got[0] != "playnow https://www.youtube.com/watch?v=b" {
		t.Errorf("calls = %q", got)
	}
	if m.mode != modeResults {
		t.Errorf("mode = %v, results should stay open", m.mode)
	}
	if !strings.Contains(m.status, "Song B") {
		t.Errorf("status = %q", m.status)
	}
	if _, ok := m.tracks["b"]; !ok {
		t.Error("track not remembered in tracks map")
	}
}

func TestQKeyQueuesSeveralSongsInARow(t *testing.T) {
	m := withResults(t, 3)
	var cmd tea.Cmd
	m, cmd = send(m, "q")
	m = run(m, cmd)
	m, cmd = send(m, "j", "q")
	m = run(m, cmd)
	want := []string{"queue https://www.youtube.com/watch?v=a", "queue https://www.youtube.com/watch?v=b"}
	got := actionsOf(m).calls
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("calls = %q, want %q", got, want)
	}
	if m.mode != modeResults {
		t.Errorf("mode = %v", m.mode)
	}
	if !strings.HasPrefix(m.status, "Queued: Song B") {
		t.Errorf("status = %q", m.status)
	}
}

func TestGlobalKeysInNormalAndResultsModes(t *testing.T) {
	for name, start := range map[string]Model{"normal": newTestModel(), "results": withResults(t, 2)} {
		t.Run(name, func(t *testing.T) {
			cases := map[string]string{"s": "skip", " ": "pause", "0": "seek 0", "5": "seek 50", "9": "seek 90", "=": "seek 100"}
			for k, want := range cases {
				m := start
				m, cmd := send(m, k)
				m = run(m, cmd)
				got := actionsOf(m).calls
				if len(got) == 0 || got[len(got)-1] != want {
					t.Errorf("key %q: calls = %q, want last %q", k, got, want)
				}
			}
		})
	}
}

func TestGlobalKeysDoNothingInSearchBar(t *testing.T) {
	m := newTestModel()
	m, cmd := send(m, "/", "s", "5", " ", "=")
	m = run(m, cmd)
	if n := len(actionsOf(m).calls); n != 0 {
		t.Errorf("player was called %d times from the search bar", n)
	}
}

func TestSeekErrorsFromMpvAreIgnored(t *testing.T) {
	m := newTestModel()
	actionsOf(m).seekErr = fmt.Errorf("seek: %w", &mpv.CommandError{Msg: "error running command"})
	m, cmd := send(m, "5")
	m = run(m, cmd)
	if m.status != "" {
		t.Errorf("status = %q, want none", m.status)
	}
}

func TestActionErrorShownInStatusLine(t *testing.T) {
	m := newTestModel()
	m, _ = update(m, actionErrMsg{err: errors.New("boom")})
	if !strings.Contains(m.status, "boom") || !m.statusIsErr {
		t.Errorf("status = %q err=%v", m.status, m.statusIsErr)
	}
}

func TestStatusClearsAfterTTL(t *testing.T) {
	m := newTestModel()
	cmd := m.setStatus("hello", false)
	if cmd == nil {
		t.Fatal("expected a clear timer")
	}
	m, _ = update(m, statusClearMsg{at: m.statusAt})
	if m.status != "" {
		t.Errorf("status = %q", m.status)
	}
	// A stale timer must not clear a newer message.
	old := m.statusAt
	m.setStatus("newer", false)
	m, _ = update(m, statusClearMsg{at: old})
	if m.status != "newer" {
		t.Errorf("stale timer cleared newer status: %q", m.status)
	}
}

func TestPlaylistEventsDriveNowPlayingAndQueue(t *testing.T) {
	m := newTestModel()
	m.tracks = map[string]yt.Result{
		"a": {ID: "a", Title: "Alpha", Channel: "ChanA", Duration: 100},
		"b": {ID: "b", Title: "Bravo", Channel: "ChanB", Duration: 200},
		"c": {ID: "c", Title: "Charlie", Channel: "ChanC", Duration: 300},
	}
	m, _ = update(m, plEvent(t, "a", "*b", "c"))
	m, _ = update(m, propEvent("playlist-pos", "1"))
	out := m.View()
	for _, want := range []string{"Bravo", "ChanB", "Charlie", "Up next (1)"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Alpha") {
		t.Errorf("already played track must not be shown:\n%s", out)
	}
}

func TestQueuePanelCapsAtFiveEntries(t *testing.T) {
	m := newTestModel()
	m.tracks = map[string]yt.Result{}
	var ids []string
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("v%d", i)
		ids = append(ids, id)
		m.tracks[id] = yt.Result{ID: id, Title: "Track" + id}
	}
	ids[0] = "*" + ids[0]
	m, _ = update(m, plEvent(t, ids...))
	m, _ = update(m, propEvent("playlist-pos", "0"))
	out := m.View()
	if !strings.Contains(out, "… and 5 more") {
		t.Errorf("expected '… and 5 more':\n%s", out)
	}
	if strings.Contains(out, "Trackv7") {
		t.Errorf("entry beyond the cap is shown:\n%s", out)
	}
}

func TestIdleShowsNothingPlaying(t *testing.T) {
	m := newTestModel()
	m, _ = update(m, plEvent(t, "a", "b"))
	m, _ = update(m, propEvent("playlist-pos", "-1"))
	m, _ = update(m, propEvent("idle-active", "true"))
	if out := m.View(); !strings.Contains(out, "Nothing playing") {
		t.Errorf("idle view:\n%s", out)
	}
}

func TestPositionDisplayedWithProgress(t *testing.T) {
	m := newTestModel()
	m.tracks = map[string]yt.Result{"a": {ID: "a", Title: "Alpha"}}
	m, _ = update(m, plEvent(t, "*a"))
	m, _ = update(m, propEvent("playlist-pos", "0"))
	m, _ = update(m, positionMsg{elapsed: 83, total: 232})
	out := m.View()
	for _, want := range []string{"01:23 / 03:52", "36%", "▶"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
	m, _ = update(m, propEvent("pause", "true"))
	if out := m.View(); !strings.Contains(out, "⏸") {
		t.Errorf("paused view missing ⏸:\n%s", out)
	}
}

func TestLoadingShownWhilePositionUnavailable(t *testing.T) {
	m := newTestModel()
	m.tracks = map[string]yt.Result{"a": {ID: "a", Title: "Alpha"}}
	m, _ = update(m, plEvent(t, "*a"))
	m, _ = update(m, propEvent("playlist-pos", "0"))
	m, _ = update(m, positionMsg{err: errors.New("property unavailable")})
	out := m.View()
	if !strings.Contains(out, "Loading…") || !strings.Contains(out, "Alpha") {
		t.Errorf("view:\n%s", out)
	}
}

func TestTickPollsAndPositionSchedulesNextTick(t *testing.T) {
	m := New(Deps{
		Player: &fakeActions{},
		Position: func(ctx context.Context) (float64, float64, error) {
			return 10, 20, nil
		},
	})
	_, cmd := update(m, tickMsg{})
	if cmd == nil {
		t.Fatal("tick must start a poll")
	}
	pm, ok := cmd().(positionMsg)
	if !ok || pm.elapsed != 10 || pm.total != 20 {
		t.Fatalf("poll result = %#v", cmd())
	}
	m, next := update(m, pm)
	if next == nil {
		t.Error("positionMsg must schedule the next tick")
	}
	if m.elapsed != 10 || m.total != 20 {
		t.Errorf("elapsed/total = %v/%v", m.elapsed, m.total)
	}
}

func TestEndFileErrorShowsCouldNotPlay(t *testing.T) {
	m := newTestModel()
	m.tracks = map[string]yt.Result{"a": {ID: "a", Title: "Alpha"}}
	m, _ = update(m, plEvent(t, "*a"))
	m, _ = update(m, propEvent("playlist-pos", "0"))
	m, _ = update(m, MpvEventMsg{mpv.Event{Name: "end-file", Reason: "error"}})
	if m.status != "Could not play: Alpha" || !m.statusIsErr {
		t.Errorf("status = %q err=%v", m.status, m.statusIsErr)
	}
	// A normal end must not show an error.
	m.status = ""
	m, _ = update(m, MpvEventMsg{mpv.Event{Name: "end-file", Reason: "eof"}})
	if m.status != "" {
		t.Errorf("status = %q", m.status)
	}
}

func TestMpvExitedQuits(t *testing.T) {
	m := newTestModel()
	_, cmd := update(m, MpvExitedMsg{})
	if !isQuit(cmd) {
		t.Error("MpvExitedMsg should quit")
	}
}

func TestCoverHiddenWhenNarrow(t *testing.T) {
	m := newTestModel()
	m.width = 59
	if strings.Contains(m.View(), "░") {
		t.Error("cover shown below 60 columns")
	}
	m.width = 60
	if !strings.Contains(m.View(), "░") {
		t.Error("cover missing at 60 columns")
	}
}
