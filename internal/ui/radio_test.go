package ui

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PatrickSteil/ytplay/internal/yt"
)

func entries(ids ...string) []playlistEntry {
	var out []playlistEntry
	for _, id := range ids {
		out = append(out, playlistEntry{Filename: "https://www.youtube.com/watch?v=" + id})
	}
	return out
}

func TestRadioSeeds(t *testing.T) {
	tests := []struct {
		name string
		pl   []playlistEntry
		pos  int
		want []string
	}{
		{"only current", entries("c"), 0, []string{"c"}},
		{"one upcoming", entries("c", "u0"), 0, []string{"c", "u0"}},
		{"two upcoming", entries("c", "u0", "u1"), 0, []string{"c", "u1"}},
		{"five upcoming spreads", entries("c", "u0", "u1", "u2", "u3", "u4"), 0, []string{"c", "u2", "u4"}},
		{"played entries ignored", entries("p0", "p1", "c", "u0"), 2, []string{"c", "u0"}},
		{"duplicates removed", entries("c", "u0", "u1", "u2", "c"), 0, []string{"c", "u1"}},
		{"idle", entries("a", "b"), -1, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := radioSeeds(tt.pl, tt.pos); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

type similarCall struct {
	seeds   []string
	exclude map[string]bool
	n       int
}

func radioModel(t *testing.T, calls *[]similarCall, found []yt.Result, err error) Model {
	t.Helper()
	m := New(Deps{
		Player: &fakeActions{},
		Similar: func(ctx context.Context, seeds []string, exclude map[string]bool, n int) ([]yt.Result, error) {
			*calls = append(*calls, similarCall{seeds, exclude, n})
			return found, err
		},
	})
	m.width, m.height = 90, 40
	// playlist: p0 (played), c (playing), u0, u1
	m, _ = update(m, plEvent(t, "p0", "*c", "u0", "u1"))
	m, _ = update(m, propEvent("playlist-pos", "1"))
	return m
}

func songs(n int) []yt.Result {
	var out []yt.Result
	for i := 0; i < n; i++ {
		out = append(out, yt.Result{ID: fmt.Sprintf("r%d", i), Title: fmt.Sprintf("Similar %d", i)})
	}
	return out
}

func TestRadioNeedsSomethingPlaying(t *testing.T) {
	var calls []similarCall
	m := radioModel(t, &calls, nil, nil)
	m, _ = update(m, propEvent("playlist-pos", "-1"))
	m, _ = typeCommand(m, "radio")
	if m.radioBusy {
		t.Error("radio must not start without a seed")
	}
	if !strings.Contains(m.status, "Play or queue something first") {
		t.Errorf("status = %q", m.status)
	}
	if len(calls) != 0 {
		t.Error("Similar was called")
	}
}

func TestRadioRequestsTenSimilarByDefaultExcludingEverythingInThePlaylist(t *testing.T) {
	var calls []similarCall
	m := radioModel(t, &calls, songs(3), nil)
	m, cmd := typeCommand(m, "radio")
	if !strings.Contains(m.status, "Finding similar") {
		t.Errorf("status = %q", m.status)
	}
	done := findMsg[radioDoneMsg](cmd)
	if done == nil || len(calls) != 1 {
		t.Fatalf("done=%v calls=%d", done, len(calls))
	}
	c := calls[0]
	if !reflect.DeepEqual(c.seeds, []string{"c", "u1"}) || c.n != 10 {
		t.Errorf("seeds=%v n=%d", c.seeds, c.n)
	}
	for _, id := range []string{"p0", "c", "u0", "u1"} {
		if !c.exclude[id] {
			t.Errorf("%s (already in the playlist) must be excluded", id)
		}
	}
}

func TestRadioResultsAreQueued(t *testing.T) {
	var calls []similarCall
	m := radioModel(t, &calls, songs(3), nil)
	m, cmd := typeCommand(m, "radio")
	done := findMsg[radioDoneMsg](cmd)
	m, cmd = update(m, *done)
	m = run(m, cmd)
	got := actionsOf(m).calls
	want := "queuemany https://www.youtube.com/watch?v=r0,https://www.youtube.com/watch?v=r1,https://www.youtube.com/watch?v=r2"
	if len(got) != 1 || got[0] != want {
		t.Errorf("calls = %q, want %q", got, want)
	}
	if m.status != "Added 3 similar songs" {
		t.Errorf("status = %q", m.status)
	}
	if m.tracks["r1"].Title != "Similar 1" {
		t.Error("new tracks must be remembered for display")
	}
}

func TestRadioErrorAndEmptyResult(t *testing.T) {
	var calls []similarCall
	m := radioModel(t, &calls, nil, nil)
	m, _ = update(m, radioDoneMsg{err: errors.New("ERROR: nope")})
	if m.status != "Radio failed: ERROR: nope" || !m.statusIsErr {
		t.Errorf("status = %q err=%v", m.status, m.statusIsErr)
	}
	m, _ = update(m, radioDoneMsg{})
	if m.status != "No new similar songs found" {
		t.Errorf("status = %q", m.status)
	}
	if n := len(actionsOf(m).calls); n != 0 {
		t.Errorf("player called %d times", n)
	}
}

func TestRadioIgnoredWhileOneIsRunning(t *testing.T) {
	var calls []similarCall
	m := radioModel(t, &calls, songs(1), nil)
	m, cmd1 := typeCommand(m, "radio")
	if cmd1 == nil {
		t.Fatal("first radio should start")
	}
	_, cmd2 := typeCommand(m, "radio")
	if cmd2 != nil {
		t.Error("second radio while one is running must be ignored")
	}
	done := findMsg[radioDoneMsg](cmd1)
	m, _ = update(m, *done)
	_, cmd3 := typeCommand(m, "radio")
	if cmd3 == nil {
		t.Error("radio should work again once finished")
	}
}

func TestRadioWorksFromResultsAndQueueModesButNotWhileTyping(t *testing.T) {
	var calls []similarCall
	m := radioModel(t, &calls, songs(1), nil)
	m, _ = send(m, "tab")
	if m.mode != modeQueue {
		t.Fatalf("mode = %v", m.mode)
	}
	if _, cmd := typeCommand(m, "radio"); cmd == nil {
		t.Error(":radio should work in queue mode")
	}
	typing, _ := send(radioModel(t, &calls, songs(1), nil), "/")
	typing, _ = send(typing, "r")
	if typing.input.Value() != "r" || typing.radioBusy {
		t.Errorf("r must be typed into the search bar: input=%q busy=%v", typing.input.Value(), typing.radioBusy)
	}
}

// :radio takes how many songs to look for (at most 50); without a number it
// looks for 10.
func TestRadioCommandTakesTheNumberOfSongs(t *testing.T) {
	for text, want := range map[string]int{
		"radio": 10, "r": 10, "radio 5": 5, "r 7": 7, "ra 3": 3, "radio 1": 1, "radio 50": 50, "radio   12 ": 12,
	} {
		var calls []similarCall
		m := radioModel(t, &calls, songs(2), nil)
		_, cmd := typeCommand(m, text)
		if findMsg[radioDoneMsg](cmd) == nil || len(calls) != 1 || calls[0].n != want {
			t.Errorf(":%s asked for %v, want one request for %d", text, calls, want)
		}
	}
}

func TestRadioCommandRefusesABadNumber(t *testing.T) {
	for _, text := range []string{"radio 0", "radio -3", "radio abc", "radio 51", "radio 2.5", "radio 1e1", "radio +4", "r 100"} {
		var calls []similarCall
		m := radioModel(t, &calls, songs(2), nil)
		m, _ = typeCommand(m, text) // (the command it returns is only the status timer)
		if len(calls) != 0 || !m.statusIsErr || !strings.Contains(m.status, "1 to 50") || m.radioBusy {
			t.Errorf(":%s: calls %v status %q (error %v) busy %v, want an error that says 1 to 50 and no request", text, calls, m.status, m.statusIsErr, m.radioBusy)
		}
	}
}
