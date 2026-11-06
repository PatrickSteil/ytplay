package ui

import (
	"testing"

	"github.com/PatrickSteil/ytplay/internal/yt"
)

func publishModel(got *[]NowPlaying) Model {
	m := New(Deps{
		Player:  &fakeActions{},
		Publish: func(np NowPlaying) { *got = append(*got, np) },
	})
	m.width, m.height = 90, 40
	m.tracks = map[string]yt.Result{
		"a": {ID: "a", Title: "Alpha", Channel: "ChanA", Duration: 100},
		"b": {ID: "b", Title: "Bravo", Channel: "ChanB", Duration: 200},
	}
	return m
}

func TestPublishesNowPlayingWhenTrackStarts(t *testing.T) {
	var got []NowPlaying
	m := publishModel(&got)
	m, _ = update(m, plEvent(t, "*a", "b"))
	m, _ = update(m, propEvent("playlist-pos", "0"))
	want := NowPlaying{Active: true, ID: "a", Title: "Alpha", Channel: "ChanA", Duration: 100, HasNext: true}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("published %+v, want [%+v]", got, want)
	}
	_ = m
}

func TestPublishOnlyOnChange(t *testing.T) {
	var got []NowPlaying
	m := publishModel(&got)
	m, _ = update(m, plEvent(t, "*a"))
	m, _ = update(m, propEvent("playlist-pos", "0"))
	n := len(got)
	// The 500 ms position poll must not spam the bus.
	m, _ = update(m, positionMsg{elapsed: 5, total: 100})
	m, _ = update(m, positionMsg{elapsed: 6, total: 100})
	m, _ = update(m, tickMsg{})
	if len(got) != n {
		t.Errorf("position updates published %d extra snapshots", len(got)-n)
	}
	m, _ = update(m, propEvent("pause", "true"))
	if len(got) != n+1 || !got[len(got)-1].Paused {
		t.Errorf("pause not published: %+v", got)
	}
}

func TestPublishTracksNextAndIdle(t *testing.T) {
	var got []NowPlaying
	m := publishModel(&got)
	m, _ = update(m, plEvent(t, "a", "*b"))
	m, _ = update(m, propEvent("playlist-pos", "1"))
	last := got[len(got)-1]
	if last.ID != "b" || last.HasNext {
		t.Errorf("last track: %+v (HasNext must be false)", last)
	}
	m, _ = update(m, propEvent("playlist-pos", "-1"))
	if last := got[len(got)-1]; last.Active || last.Title != "" {
		t.Errorf("idle: %+v", last)
	}
}

func TestPublishIsOptional(t *testing.T) {
	m := newTestModel() // no Publish
	m, _ = update(m, plEvent(t, "*a"))
	_, _ = update(m, propEvent("playlist-pos", "0")) // must not panic
}
