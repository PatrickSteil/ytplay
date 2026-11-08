package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PatrickSteil/ytplay/internal/yt"
)

const (
	playlistLink = "https://www.youtube.com/playlist?list=PLabc123_-xyzABCDEFGHIJ"
	videoLink    = "https://youtu.be/dQw4w9WgXcQ"
)

// loadFake stands in for yt.LoadLink.
type loadFake struct {
	mu     sync.Mutex
	calls  []yt.Link
	loaded yt.Loaded
	err    error
}

func (f *loadFake) load(ctx context.Context, l yt.Link) (yt.Loaded, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, l)
	return f.loaded, f.err
}

func (f *loadFake) asked() []yt.Link {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]yt.Link(nil), f.calls...)
}

func listOf(n int) yt.Loaded {
	l := yt.Loaded{Title: "My mix"}
	for i := 1; i <= n; i++ {
		l.Entries = append(l.Entries, yt.Result{ID: fmt.Sprintf("v%03d", i), Title: fmt.Sprintf("Song %d", i), Channel: "C", Duration: 100})
	}
	return l
}

// loadModel is a playing queue whose player can load links.
func loadModel(t *testing.T, loaded yt.Loaded) (Model, *loadFake) {
	t.Helper()
	f := &loadFake{loaded: loaded}
	m := queueModel(t, 3)
	m.deps.Load = f.load
	return m, f
}

// settle runs a command and then the commands that handling its answer starts,
// until nothing is left (a load's answer queues the songs).
func settle(m Model, cmd tea.Cmd) Model {
	for round := 0; cmd != nil && round < 5; round++ {
		var next tea.Cmd
		for _, msg := range collect(cmd) {
			var c tea.Cmd
			m, c = update(m, msg)
			if c != nil {
				next = tea.Batch(next, c)
			}
		}
		cmd = next
	}
	return m
}

func finishLoad(m Model, text string) Model {
	next, cmd := typeCommand(m, text)
	return settle(next, cmd)
}

func TestLoadCommandAddsAWholePlaylistToTheQueue(t *testing.T) {
	for _, word := range []string{"load", "l", "lo"} {
		m, f := loadModel(t, listOf(3))
		m = finishLoad(m, word+" "+playlistLink)
		if got := f.asked(); len(got) != 1 || got[0] != (yt.Link{Kind: yt.PlaylistLink, ID: "PLabc123_-xyzABCDEFGHIJ"}) {
			t.Fatalf(":%s: loaded %v, want the playlist", word, got)
		}
		want := "queuemany " + w + "v001," + w + "v002," + w + "v003"
		if got := lastCall(m); got != want {
			t.Errorf(":%s: call %q, want %q", word, got, want)
		}
		if !strings.Contains(m.status, "Added 3 songs from My mix") {
			t.Errorf(":%s: status %q", word, m.status)
		}
		if m.loadBusy {
			t.Errorf(":%s: still busy after the answer", word)
		}
		if m.tracks["v002"].Title != "Song 2" {
			t.Errorf(":%s: the songs' titles were not remembered: %+v", word, m.tracks["v002"])
		}
	}
}

func TestLoadCommandSaysItIsLoadingAndIgnoresAnotherUntilDone(t *testing.T) {
	m, f := loadModel(t, listOf(2))
	m, cmd := typeCommand(m, "load "+playlistLink)
	if !m.loadBusy || !strings.Contains(m.status, "Loading the playlist") {
		t.Errorf("busy %v status %q, want it to say it is loading", m.loadBusy, m.status)
	}
	second, c2 := typeCommand(m, "load "+playlistLink) // while the first is running
	if c2 != nil && len(collect(c2)) != 0 {
		t.Errorf("a second load started while the first was running (status %q)", second.status)
	}
	run(m, cmd)
	if got := f.asked(); len(got) != 1 {
		t.Errorf("loads = %v, want exactly one", got)
	}
}

func TestLoadCommandForAVideoQueuesIt(t *testing.T) {
	m, f := loadModel(t, yt.Loaded{Entries: []yt.Result{{ID: "dQw4w9WgXcQ", Title: "Never Gonna", Channel: "Rick", Duration: 212}}})
	m = finishLoad(m, "load "+videoLink)
	if got := f.asked(); len(got) != 1 || got[0] != (yt.Link{Kind: yt.VideoLink, ID: "dQw4w9WgXcQ"}) {
		t.Fatalf("loaded %v, want the video", got)
	}
	if got := lastCall(m); got != "queuemany "+w+"dQw4w9WgXcQ" || !strings.Contains(m.status, "Queued: Never Gonna") {
		t.Errorf("call %q status %q", got, m.status)
	}
}

func TestLoadCommandSaysWhenThePlaylistWasCutShort(t *testing.T) {
	l := listOf(500)
	l.Truncated = true
	m, _ := loadModel(t, l)
	m = finishLoad(m, "load "+playlistLink)
	if got := strings.Count(lastCall(m), w); got != 500 {
		t.Errorf("queued %d songs, want all 500 in one call", got)
	}
	if !strings.Contains(m.status, "Added 500 songs") || !strings.Contains(m.status, "first 500") {
		t.Errorf("status %q, want it to say these are the first 500", m.status)
	}
	one := listOf(1)
	m, _ = loadModel(t, one)
	m = finishLoad(m, "load "+playlistLink)
	if !strings.Contains(m.status, "Added 1 song ") {
		t.Errorf("status %q, want the singular", m.status)
	}
}

func TestLoadCommandRefusesWhatIsNotAYouTubeLink(t *testing.T) {
	for _, text := range []string{"load", "load daft punk", "load https://example.com/playlist?list=PLabc123456", "load -o /tmp/x", "load https://www.youtube.com/@channel"} {
		m, f := loadModel(t, listOf(2))
		m = finishLoad(m, text)
		if len(f.asked()) != 0 || !m.statusIsErr || m.loadBusy || len(actionsOf(m).calls) != 0 {
			t.Errorf(":%s: loads %v status %q (error %v), want an error and nothing loaded", text, f.asked(), m.status, m.statusIsErr)
		}
	}
	m, _ := loadModel(t, listOf(2))
	m = finishLoad(m, "load daft punk")
	if !strings.Contains(m.status, "YouTube") {
		t.Errorf("status %q, want it to say what a link has to be", m.status)
	}
}

func TestLoadCommandAcceptsAPastedLinkInQuotesOrBrackets(t *testing.T) {
	for _, text := range []string{"load '" + playlistLink + "'", "load <" + playlistLink + ">", `load "` + playlistLink + `"`, "load   " + playlistLink + "  "} {
		m, f := loadModel(t, listOf(2))
		finishLoad(m, text)
		if len(f.asked()) != 1 {
			t.Errorf(":%s loaded %v", text, f.asked())
		}
	}
}

func TestAFailedLoadSaysWhyAndCanBeTriedAgain(t *testing.T) {
	m, f := loadModel(t, yt.Loaded{})
	f.err = errors.New("ERROR: Private playlist")
	m = finishLoad(m, "load "+playlistLink)
	if !m.statusIsErr || !strings.Contains(m.status, "Private playlist") || m.loadBusy {
		t.Errorf("status %q (error %v) busy %v, want the reason and not busy", m.status, m.statusIsErr, m.loadBusy)
	}
	if len(actionsOf(m).calls) != 0 {
		t.Errorf("a failed load queued %q", actionsOf(m).calls)
	}
	f.err, f.loaded = nil, listOf(2)
	m = finishLoad(m, "load "+playlistLink)
	if got := lastCall(m); !strings.HasPrefix(got, "queuemany ") {
		t.Errorf("after trying again: call %q", got)
	}
}

func TestLoadingNeedsThePlayerSideToSupportIt(t *testing.T) {
	m := queueModel(t, 2) // no Load in the deps
	m = finishLoad(m, "load "+playlistLink)
	if !m.statusIsErr || !strings.Contains(m.status, "not available") || m.loadBusy {
		t.Errorf("status %q (error %v) busy %v", m.status, m.statusIsErr, m.loadBusy)
	}
}

// ---- a link where a search is expected ----

func TestALinkInTheSearchBarLoadsInsteadOfSearching(t *testing.T) {
	m, f := loadModel(t, listOf(2))
	searched := 0
	m.deps.Search = func(ctx context.Context, q string) ([]yt.Result, map[yt.Source]error, error) {
		searched++
		return nil, nil, nil
	}
	m, _ = send(m, "/")
	for _, r := range playlistLink {
		m, _ = send(m, string(r))
	}
	m, cmd := send(m, "enter")
	m = settle(m, cmd)
	if searched != 0 || m.query != "" {
		t.Errorf("the link was searched for (searches %d, query %q)", searched, m.query)
	}
	if got := f.asked(); len(got) != 1 || got[0].Kind != yt.PlaylistLink {
		t.Errorf("loads = %v, want the playlist", got)
	}
	if m.mode == modeSearch {
		t.Errorf("the search bar is still open after the link was taken")
	}
	if !strings.HasPrefix(lastCall(m), "queuemany ") {
		t.Errorf("call %q, want the songs queued", lastCall(m))
	}

	// words are still searched for
	m2, f2 := loadModel(t, listOf(2))
	m2, _ = send(m2, "/", "d", "a", "f", "t")
	m2, cmd = send(m2, "enter")
	if m2.query != "daft" || cmd == nil || len(f2.asked()) != 0 {
		t.Errorf("a search term: query %q loads %v, want a search", m2.query, f2.asked())
	}
}

func TestFindWithALinkLoadsItToo(t *testing.T) {
	m, f := loadModel(t, listOf(2))
	m = finishLoad(m, "find "+playlistLink)
	if len(f.asked()) != 1 || m.query != "" {
		t.Errorf(":find <link>: loads %v query %q, want a load and no search", f.asked(), m.query)
	}
	m2, f2 := loadModel(t, listOf(2))
	m2, _ = typeCommand(m2, "find daft punk")
	if m2.query != "daft punk" || len(f2.asked()) != 0 {
		t.Errorf(":find daft punk: query %q loads %v", m2.query, f2.asked())
	}
}

func TestALoadCanBeUndone(t *testing.T) {
	m, _ := loadModel(t, listOf(3))
	m = finishLoad(m, "load "+playlistLink)
	m = edit(m, "u")
	if got := setUpcomingCall(m); got != "e1,e2" || !strings.Contains(m.status, "Undid the playlist") {
		t.Errorf("undo: call %q status %q, want the old queue back", got, m.status)
	}
}

func TestLoadIsRefusedInsideThePopup(t *testing.T) {
	f := &loadFake{loaded: listOf(2)}
	m := djModel(t, 3, nil)
	m.deps.Load = f.load
	m = finishLoad(m, "load "+playlistLink)
	if len(f.asked()) != 0 || m.mode != modeDJ || !strings.Contains(m.status, "Close the popup first") {
		t.Errorf("in the popup: loads %v mode %v status %q", f.asked(), m.mode, m.status)
	}
}

func TestLoadNamesAndShortForms(t *testing.T) {
	for word, want := range map[string]string{"load": "load", "l": "load", "lo": "load", "loa": "load"} {
		if c, _ := findCommand(word); c == nil || c.name != want {
			t.Errorf("findCommand(%q) = %v, want %q", word, c, want)
		}
	}
}
