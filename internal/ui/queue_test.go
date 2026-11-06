package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/PatrickSteil/ytplay/internal/yt"
)

// queueModel has a playlist of n entries (ids e0..e{n-1}) with e0 playing, so
// the editable queue is e1..e{n-1} at mpv indices 1..n-1.
func queueModel(t *testing.T, n int) Model {
	t.Helper()
	m := newTestModel()
	ids := make([]string, n)
	for i := range ids {
		id := fmt.Sprintf("e%d", i)
		m.tracks[id] = yt.Result{ID: id, Title: "Title-" + id, Duration: 60}
		ids[i] = id
	}
	ids[0] = "*" + ids[0]
	m, _ = update(m, plEvent(t, ids...))
	m, _ = update(m, propEvent("playlist-pos", "0"))
	return m
}

func queueIDs(m Model) string {
	var ids []string
	for _, e := range m.playlist {
		ids = append(ids, strings.TrimPrefix(e.Filename, "https://www.youtube.com/watch?v="))
	}
	return strings.Join(ids, ",")
}

func lastCall(m Model) string {
	c := actionsOf(m).calls
	if len(c) == 0 {
		return ""
	}
	return c[len(c)-1]
}

// edit sends a key and runs the resulting cmd, so the busy flag is released.
func edit(m Model, k string) Model {
	m, cmd := send(m, k)
	return run(m, cmd)
}

func TestTabFocusesQueueAndTabLeavesAgain(t *testing.T) {
	m := queueModel(t, 4)
	m, _ = send(m, "tab")
	if m.mode != modeQueue || m.queueCursor != 0 {
		t.Fatalf("mode=%v cursor=%d", m.mode, m.queueCursor)
	}
	m, _ = send(m, "tab")
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want normal", m.mode)
	}
}

func TestQueueFocusReturnsToResultsWhenOpenedFromResults(t *testing.T) {
	m := withResults(t, 2)
	m, _ = update(m, plEvent(t, "*a", "b"))
	m, _ = update(m, propEvent("playlist-pos", "0"))
	m, _ = send(m, "tab")
	if m.mode != modeQueue {
		t.Fatalf("mode = %v", m.mode)
	}
	if !strings.Contains(m.View(), "Results for") {
		t.Error("results list should stay visible while editing the queue")
	}
	m, _ = send(m, "esc")
	if m.mode != modeResults {
		t.Errorf("mode = %v, want results", m.mode)
	}
}

func TestTabWithEmptyQueueSaysSo(t *testing.T) {
	m := queueModel(t, 1) // only the playing track
	m, _ = send(m, "tab")
	if m.mode != modeNormal || !strings.Contains(m.status, "empty") {
		t.Errorf("mode=%v status=%q", m.mode, m.status)
	}
	idle := newTestModel()
	idle, _ = send(idle, "tab")
	if idle.mode != modeNormal {
		t.Errorf("idle: mode = %v", idle.mode)
	}
}

func TestQueueCursorMovesWithJK(t *testing.T) {
	m := queueModel(t, 4) // 3 editable
	m, _ = send(m, "tab", "j", "j", "j")
	if m.queueCursor != 2 {
		t.Errorf("cursor = %d, want 2 (clamped)", m.queueCursor)
	}
	m, _ = send(m, "k", "k", "k")
	if m.queueCursor != 0 {
		t.Errorf("cursor = %d, want 0 (clamped)", m.queueCursor)
	}
}

func TestRemoveSelectedEntry(t *testing.T) {
	m := queueModel(t, 4)
	m, _ = send(m, "tab", "j") // cursor on e2, mpv index 2
	m = edit(m, "x")
	if lastCall(m) != "remove 2" {
		t.Errorf("call = %q, want remove 2", lastCall(m))
	}
	if got := queueIDs(m); got != "e0,e1,e3" {
		t.Errorf("playlist = %s", got)
	}
	if m.queueCursor != 1 {
		t.Errorf("cursor = %d, want 1 (now on e3)", m.queueCursor)
	}
	// e3 (mpv index 2) was the last entry: removing it pulls the cursor back.
	m = edit(m, "x")
	if lastCall(m) != "remove 2" || m.queueCursor != 0 || queueIDs(m) != "e0,e1" {
		t.Errorf("call=%q cursor=%d", lastCall(m), m.queueCursor)
	}
}

func TestRemovingLastEntryLeavesQueueMode(t *testing.T) {
	m := queueModel(t, 2)
	m, _ = send(m, "tab")
	m = edit(m, "x")
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want normal", m.mode)
	}
	if !strings.Contains(m.View(), "Up next (0)") {
		t.Error("queue should show Up next (0)")
	}
}

func TestMoveUpAndDownReorderAndCursorFollows(t *testing.T) {
	m := queueModel(t, 4)
	m, _ = send(m, "tab", "j", "j") // cursor on e3 (index 3)
	m = edit(m, "up")
	if lastCall(m) != "moveup 3" || queueIDs(m) != "e0,e1,e3,e2" || m.queueCursor != 1 {
		t.Errorf("call=%q ids=%s cursor=%d", lastCall(m), queueIDs(m), m.queueCursor)
	}
	m = edit(m, "up")
	if lastCall(m) != "moveup 2" || queueIDs(m) != "e0,e3,e1,e2" || m.queueCursor != 0 {
		t.Errorf("call=%q ids=%s cursor=%d", lastCall(m), queueIDs(m), m.queueCursor)
	}
	m = edit(m, "down")
	if lastCall(m) != "movedown 1" || queueIDs(m) != "e0,e1,e3,e2" || m.queueCursor != 1 {
		t.Errorf("call=%q ids=%s cursor=%d", lastCall(m), queueIDs(m), m.queueCursor)
	}
}

func TestMoveAtBoundariesDoesNothing(t *testing.T) {
	m := queueModel(t, 3)
	m, _ = send(m, "tab")
	before := len(actionsOf(m).calls)
	m = edit(m, "up") // first editable entry must not pass the playing track
	if len(actionsOf(m).calls) != before || queueIDs(m) != "e0,e1,e2" {
		t.Errorf("moved above the first entry: calls=%q ids=%s", actionsOf(m).calls, queueIDs(m))
	}
	m, _ = send(m, "j")
	m = edit(m, "down") // already last
	if len(actionsOf(m).calls) != before || queueIDs(m) != "e0,e1,e2" {
		t.Errorf("moved below the last entry: calls=%q ids=%s", actionsOf(m).calls, queueIDs(m))
	}
}

func TestEditsAreIgnoredWhileAnotherEditIsInFlight(t *testing.T) {
	m := queueModel(t, 5)
	m, _ = send(m, "tab")
	m, cmd1 := send(m, "x")
	if cmd1 == nil {
		t.Fatal("first edit should dispatch")
	}
	m, cmd2 := send(m, "x")
	if cmd2 != nil {
		t.Error("second edit must wait for the first to finish")
	}
	m = run(m, cmd1)
	m, cmd3 := send(m, "x")
	if cmd3 == nil {
		t.Error("edits should work again after completion")
	}
}

func TestEditFailureShowsError(t *testing.T) {
	m := queueModel(t, 3)
	m, _ = update(m, queueEditDoneMsg{err: fmt.Errorf("boom")})
	if !strings.Contains(m.status, "boom") || !m.statusIsErr {
		t.Errorf("status=%q", m.status)
	}
}

func TestShrinkingPlaylistClampsCursorAndLeavesWhenEmpty(t *testing.T) {
	m := queueModel(t, 4)
	m, _ = send(m, "tab", "j", "j")
	m, _ = update(m, plEvent(t, "*e0", "e1"))
	if m.queueCursor != 0 || m.mode != modeQueue {
		t.Errorf("cursor=%d mode=%v", m.queueCursor, m.mode)
	}
	m, _ = update(m, plEvent(t, "*e0"))
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want normal once the queue is empty", m.mode)
	}
}

func TestGlobalKeysStillWorkInQueueMode(t *testing.T) {
	m := queueModel(t, 3)
	m, _ = send(m, "tab")
	m, cmd := send(m, "s")
	m = run(m, cmd)
	if lastCall(m) != "skip" {
		t.Errorf("call = %q", lastCall(m))
	}
	if _, cmd := send(m, "Q"); !isQuit(cmd) {
		t.Error("Q should quit from queue mode")
	}
}

func TestQueueViewMarksCursorAndScrolls(t *testing.T) {
	m := queueModel(t, 10) // 9 editable
	m, _ = send(m, "tab")
	out := m.View()
	if !strings.Contains(out, "> 1. Title-e1") {
		t.Errorf("cursor not marked on first entry:\n%s", out)
	}
	for i := 0; i < 6; i++ {
		m, _ = send(m, "j")
	}
	out = m.View()
	if !strings.Contains(out, "> 7. Title-e7") {
		t.Errorf("cursor row (7.) not visible/marked:\n%s", out)
	}
	if strings.Contains(out, "Title-e1 ") {
		t.Errorf("window did not scroll:\n%s", out)
	}
	if strings.Count(out, "> ") != 1 {
		t.Errorf("exactly one cursor expected:\n%s", out)
	}
}

func TestQueueModeShowsItsOwnHelp(t *testing.T) {
	m := queueModel(t, 3)
	m, _ = send(m, "tab")
	if out := m.View(); !strings.Contains(out, "x remove") {
		t.Errorf("queue help missing:\n%s", out)
	}
}
