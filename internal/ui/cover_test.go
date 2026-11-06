package ui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/PatrickSteil/ytplay/internal/yt"
)

func coverModel(calls *atomic.Int32, fail bool) Model {
	m := New(Deps{
		Player: &fakeActions{},
		Cover: func(ctx context.Context, id string) (string, error) {
			calls.Add(1)
			if fail {
				return "", errors.New("404")
			}
			return strings.TrimSpace(strings.Repeat("COVER-"+id+"\n", 9)), nil
		},
	})
	m.width, m.height = 90, 40
	m.tracks = map[string]yt.Result{"a": {ID: "a", Title: "Alpha"}, "b": {ID: "b", Title: "Bravo"}}
	return m
}

func startTrack(t *testing.T, m Model, ids ...string) (Model, *coverMsg) {
	t.Helper()
	cur := "*" + ids[0]
	m, _ = update(m, plEvent(t, append([]string{cur}, ids[1:]...)...))
	m, cmd := update(m, propEvent("playlist-pos", "0"))
	if cmd == nil {
		return m, nil
	}
	msg, _ := cmd().(coverMsg)
	if msg.id == "" {
		return m, nil
	}
	return m, &msg
}

func TestCoverFetchedOnTrackChangeAndShown(t *testing.T) {
	var calls atomic.Int32
	m := coverModel(&calls, false)
	if !strings.Contains(m.View(), "░") {
		t.Fatal("placeholder expected before any cover")
	}
	m, msg := startTrack(t, m, "a")
	if msg == nil || msg.id != "a" || calls.Load() != 1 {
		t.Fatalf("expected one cover fetch for a, got msg=%v calls=%d", msg, calls.Load())
	}
	// While loading, the placeholder keeps the layout.
	if !strings.Contains(m.View(), "░") {
		t.Error("placeholder expected while the cover loads")
	}
	m, _ = update(m, *msg)
	out := m.View()
	if !strings.Contains(out, "COVER-a") {
		t.Errorf("cover not shown:\n%s", out)
	}
	if strings.Contains(out, "░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░") {
		t.Error("placeholder still shown next to a loaded cover")
	}
}

func TestCoverLayoutDoesNotShift(t *testing.T) {
	var calls atomic.Int32
	m := coverModel(&calls, false)
	m, msg := startTrack(t, m, "a")
	before := strings.Count(m.View(), "\n")
	m, _ = update(m, *msg)
	after := strings.Count(m.View(), "\n")
	if before != after {
		t.Errorf("view height changed from %d to %d lines when the cover arrived", before, after)
	}
	if h := len(m.renderTop()); h != 9 {
		t.Errorf("top panel height = %d, want 9", h)
	}
}

func TestCoverNotRefetchedWhileLoadingOrAfterLoading(t *testing.T) {
	var calls atomic.Int32
	m := coverModel(&calls, false)
	m, msg := startTrack(t, m, "a", "b")
	// Same track announced again (another event) while the fetch is in flight.
	m, cmd := update(m, propEvent("playlist-pos", "0"))
	if cmd != nil {
		t.Error("no new fetch expected for an unchanged track")
	}
	m, _ = update(m, *msg)
	// Go to b and back to a: a is already cached.
	m, cmd = update(m, propEvent("playlist-pos", "1"))
	if cmd == nil {
		t.Fatal("expected fetch for b")
	}
	_, cmd = update(m, propEvent("playlist-pos", "0"))
	if cmd != nil {
		t.Error("cover for a fetched twice")
	}
}

func TestCoverFailureKeepsPlaceholderAndIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	m := coverModel(&calls, true)
	m, msg := startTrack(t, m, "a", "b")
	if msg == nil || msg.err == nil {
		t.Fatalf("expected a failed coverMsg, got %v", msg)
	}
	m, _ = update(m, *msg)
	if !strings.Contains(m.View(), "░") {
		t.Error("placeholder expected after a failed download")
	}
	if m.status != "" {
		t.Errorf("a failed cover must not show an error, got %q", m.status)
	}
	m, _ = update(m, propEvent("playlist-pos", "1"))
	_, cmd := update(m, propEvent("playlist-pos", "0"))
	if cmd != nil {
		t.Error("failed cover was retried")
	}
}
