package ui

import (
	"strings"
	"testing"

	"github.com/PatrickSteil/ytplay/internal/yt"
)

const w = "https://www.youtube.com/watch?v="

// doCommand types a command, runs what it started and returns the last call it
// made to the player ("" if it made none).
func doCommand(m *Model, text string) string {
	before := len(actionsOf(*m).calls)
	next, cmd := typeCommand(*m, text)
	*m = run(next, cmd)
	if c := actionsOf(*m).calls; len(c) > before {
		return c[len(c)-1]
	}
	return ""
}

// ---- seek, previous, play ----

func TestSeekCommand(t *testing.T) {
	for arg, want := range map[string]string{
		"90": "seekto 90", "1:30": "seekto 90", "1:02:03": "seekto 3723", "1.5": "seekto 1.5", "0": "seekto 0",
		"50%": "seek 50", "0%": "seek 0", "100%": "seek 100",
		"+10": "seekby 10", "-30": "seekby -30", "+1:30": "seekby 90", "-0:15": "seekby -15",
	} {
		m := queueModel(t, 2)
		if got := doCommand(&m, "seek "+arg); got != want {
			t.Errorf(":seek %s: call %q, want %q", arg, got, want)
		}
	}
	for _, arg := range []string{"", "abc", "50.5%", "101%", "-", "+", "1:75", "1:", ":30", "1:2:3:4", "%", "-%"} {
		m := queueModel(t, 2)
		got := doCommand(&m, "seek "+arg)
		if got != "" || !m.statusIsErr {
			t.Errorf(":seek %q: call %q status %q (error %v), want an error and no call", arg, got, m.status, m.statusIsErr)
		}
	}
	m := newTestModel()
	if got := doCommand(&m, "seek 30"); got != "" || !strings.Contains(m.status, "Nothing is playing") {
		t.Errorf("with nothing playing: call %q status %q", got, m.status)
	}
}

func TestArrowKeysSeekFiveSeconds(t *testing.T) {
	for key, want := range map[string]string{"left": "seekby -5", "right": "seekby 5"} {
		m, cmd := send(queueModel(t, 2), key)
		m = run(m, cmd)
		if got := lastCall(m); got != want {
			t.Errorf("%s: call %q, want %q", key, got, want)
		}
	}
}

func TestPrevCommand(t *testing.T) {
	for _, word := range []string{"prev", "p", "pr"} {
		m := queueModel(t, 2)
		if got := doCommand(&m, word); got != "prev" {
			t.Errorf(":%s: call %q, want prev", word, got)
		}
	}
	m := newTestModel()
	if got := doCommand(&m, "prev"); got != "" || !strings.Contains(m.status, "Nothing is playing") {
		t.Errorf("with nothing playing: call %q status %q", got, m.status)
	}
}

func TestPlayCommandPlaysTheHighlightedSongNow(t *testing.T) {
	m := withResults(t, 3)
	m, _ = send(m, "j")
	if got := doCommand(&m, "play"); got != "playnow "+w+"b" {
		t.Errorf("from the results: call %q, want the highlighted result played", got)
	}

	// from the queue the entry leaves its place and plays; the rest keep their order
	play := map[string]func(Model) Model{
		"the command": func(q Model) Model { doCommand(&q, "play"); return q },
		"Enter": func(q Model) Model {
			q, cmd := send(q, "enter")
			return run(q, cmd)
		},
	}
	for how, do := range play {
		q := queueModel(t, 4)
		q, _ = send(q, "tab", "j") // e2
		q = do(q)
		if got, want := strings.Join(actionsOf(q).calls, " | "), "remove 2 | playnow "+w+"e2"; got != want {
			t.Errorf("from the queue (%s): calls %q, want %q", how, got, want)
		}
	}

	n := queueModel(t, 3) // nothing is highlighted
	if got := doCommand(&n, "play"); got != "" || n.status == "" {
		t.Errorf("with nothing highlighted: call %q status %q, want a hint", got, n.status)
	}
}

// ---- shuffle ----

func reversed(n int) []int {
	p := make([]int, n)
	for i := range p {
		p[i] = n - 1 - i
	}
	return p
}

func TestShuffleCommand(t *testing.T) {
	old := shufflePerm
	shufflePerm = reversed
	t.Cleanup(func() { shufflePerm = old })
	m := queueModel(t, 5)
	doCommand(&m, "shuffle")
	if got := reorderCall(m); got != "e4,e3,e2,e1" {
		t.Errorf("reorder = %q, want the upcoming songs in the shuffled order", got)
	}
	if got := queueIDs(m); got != "e0,e4,e3,e2,e1" {
		t.Errorf("playlist = %q, the playing song must stay first", got)
	}
	for _, n := range []int{1, 2} { // no upcoming song, or only one
		q := queueModel(t, n)
		doCommand(&q, "shuffle")
		if reorderCall(q) != "" || !strings.Contains(q.status, "Nothing to shuffle") {
			t.Errorf("%d songs: reorder %q status %q, want nothing to shuffle", n, reorderCall(q), q.status)
		}
	}
}

// ---- undo ----

func setUpcomingCall(m Model) string {
	c := lastCall(m)
	if !strings.HasPrefix(c, "setupcoming ") {
		return ""
	}
	return strings.ReplaceAll(strings.TrimPrefix(c, "setupcoming "), w, "")
}

func TestUndoRestoresWhatTheLastQueueEditChanged(t *testing.T) {
	old := shufflePerm
	shufflePerm = reversed
	t.Cleanup(func() { shufflePerm = old })
	edits := map[string]func(t *testing.T) Model{
		"a removal": func(t *testing.T) Model { m, _ := send(queueModel(t, 4), "tab"); return edit(m, "x") },
		"a move":    func(t *testing.T) Model { m, _ := send(queueModel(t, 4), "tab"); return edit(m, "down") },
		"a sort": func(t *testing.T) Model {
			m := djModel(t, 4, map[string]float64{"e1": 140, "e2": 90, "e3": 120})
			return editCommand(m, "sort")
		},
		"a shuffle": func(t *testing.T) Model { m := queueModel(t, 4); doCommand(&m, "shuffle"); return m },
		"a clear": func(t *testing.T) Model {
			m, _ := typeCommand(queueModel(t, 4), "clear")
			m, cmd := send(m, "y")
			return run(m, cmd)
		},
	}
	for name, edited := range edits {
		for _, how := range []string{"u", "undo"} {
			m := edited(t)
			if queueIDs(m) == "e0,e1,e2,e3" {
				t.Fatalf("%s: setup changed nothing", name)
			}
			if how == "u" {
				m = edit(m, "u")
			} else {
				m = editCommand(m, "undo")
			}
			if got := queueIDs(m); got != "e0,e1,e2,e3" {
				t.Errorf("undo (%s) after %s: playlist %q, want e0,e1,e2,e3", how, name, got)
			}
			if got := setUpcomingCall(m); got != "e1,e2,e3" {
				t.Errorf("undo (%s) after %s: call %q, want the old upcoming songs e1,e2,e3", how, name, got)
			}
			if !strings.Contains(m.status, "Undid") {
				t.Errorf("undo (%s) after %s: status %q", how, name, m.status)
			}
		}
	}
}

func TestUndoRemovesWhatAddAndRadioAdded(t *testing.T) {
	m := queueModel(t, 4)
	m.results, m.selected = results(2), 0
	doCommand(&m, "add")
	m = edit(m, "u")
	if got := setUpcomingCall(m); got != "e1,e2,e3" {
		t.Errorf("undo after :add: call %q, want the old upcoming songs", got)
	}

	r := queueModel(t, 4)
	next, cmd := update(r, radioDoneMsg{results: []yt.Result{{ID: "r0", Title: "R0"}, {ID: "r1", Title: "R1"}}})
	r = run(next, cmd)
	r = edit(r, "u")
	if got := setUpcomingCall(r); got != "e1,e2,e3" {
		t.Errorf("undo after the radio: call %q, want the old upcoming songs", got)
	}
}

func TestUndoWalksBackOneEditAtATime(t *testing.T) {
	m, _ := send(queueModel(t, 5), "tab")
	m = edit(m, "x") // e1
	m = edit(m, "x") // e2
	if got := queueIDs(m); got != "e0,e3,e4" {
		t.Fatalf("setup: %q", got)
	}
	m = edit(m, "u")
	if got := queueIDs(m); got != "e0,e2,e3,e4" {
		t.Errorf("first undo: %q, want e0,e2,e3,e4", got)
	}
	m = edit(m, "u")
	if got := queueIDs(m); got != "e0,e1,e2,e3,e4" {
		t.Errorf("second undo: %q, want e0,e1,e2,e3,e4", got)
	}
	n := len(actionsOf(m).calls)
	m = edit(m, "u")
	if !strings.Contains(m.status, "Nothing to undo") || len(actionsOf(m).calls) != n {
		t.Errorf("a third undo: status %q, calls %d -> %d, want nothing to undo and no call", m.status, n, len(actionsOf(m).calls))
	}
}

func TestNothingToUndoOnAFreshStart(t *testing.T) {
	for name, m := range map[string]Model{"playing": queueModel(t, 3), "idle": newTestModel()} {
		m = edit(m, "u")
		if !strings.Contains(m.status, "Nothing to undo") || len(actionsOf(m).calls) != 0 {
			t.Errorf("%s: status %q calls %q", name, m.status, actionsOf(m).calls)
		}
	}
}

func TestUndoIsForgottenWhenTheSongChanges(t *testing.T) {
	m, _ := send(queueModel(t, 4), "tab")
	m = edit(m, "x")
	m, _ = update(m, propEvent("playlist-pos", "1")) // the next song starts
	n := len(actionsOf(m).calls)
	m = edit(m, "u")
	if !strings.Contains(m.status, "Nothing to undo") || len(actionsOf(m).calls) != n {
		t.Errorf("status %q: an undo after the song changed must find nothing", m.status)
	}
}

func TestUndoWaitsForAnEditInFlight(t *testing.T) {
	m, _ := send(queueModel(t, 4), "tab")
	m, first := send(m, "x") // not run yet: the edit is in flight
	m, second := send(m, "u")
	if second != nil {
		t.Error("an undo started while an edit was still in flight")
	}
	m = run(m, first)
	m = edit(m, "u")
	if got := queueIDs(m); got != "e0,e1,e2,e3" {
		t.Errorf("after the edit finished: %q, want it undone", got)
	}
}

func TestUndoKeepsTheLastTwentyEdits(t *testing.T) {
	m, _ := send(queueModel(t, 30), "tab")
	for i := 0; i < 25; i++ {
		m = edit(m, "x")
	}
	if len(m.undo) != undoDepth {
		t.Errorf("%d edits remembered, want %d", len(m.undo), undoDepth)
	}
}

func TestUndoWorksInsideTheDJPopup(t *testing.T) {
	for _, how := range []string{"u", "undo"} {
		m := djModel(t, 4, map[string]float64{"e1": 140, "e2": 90, "e3": 120})
		m = editCommand(m, "sort")
		if queueIDs(m) == "e0,e1,e2,e3" {
			t.Fatal("setup: the sort changed nothing")
		}
		if how == "u" {
			m = edit(m, "u")
		} else {
			m = editCommand(m, "undo")
		}
		if got := queueIDs(m); got != "e0,e1,e2,e3" || m.mode != modeDJ {
			t.Errorf("undo (%s) in the popup: playlist %q mode %v, want the order back and the popup open", how, got, m.mode)
		}
	}
}

// ---- volume ----

func TestVolumeCommands(t *testing.T) {
	m := queueModel(t, 2)
	for _, c := range []struct{ text, want string }{
		{"vol 40", "volume 40"}, {"vol +10", "volume 50"}, {"vol -20", "volume 30"}, {"v 75", "volume 75"},
		{"vol 33.5", "volume 33.5"}, {"vol +50", "volume 83.5"}, {"vol +50", "volume 100"}, {"vol -500", "volume 0"},
		{"vo 60", "volume 60"},
	} {
		if got := doCommand(&m, c.text); got != c.want {
			t.Errorf(":%s: call %q, want %q", c.text, got, c.want)
		}
	}
	if got := doCommand(&m, "vol"); got != "" || !strings.Contains(m.status, "Volume 60%") {
		t.Errorf(":vol alone: call %q status %q, want the volume shown and no call", got, m.status)
	}
	for _, text := range []string{"vol 150", "vol abc", "vol 1.2.3", "vol +", "vol -", "vol 101"} {
		if got := doCommand(&m, text); got != "" || !m.statusIsErr {
			t.Errorf(":%s: call %q status %q (error %v), want an error and no call", text, got, m.status, m.statusIsErr)
		}
	}
}

func TestVolumeKeys(t *testing.T) {
	m := queueModel(t, 2)
	doCommand(&m, "vol 50")
	for _, c := range []struct{ key, want string }{{"+", "volume 55"}, {"-", "volume 50"}, {"-", "volume 45"}} {
		next, cmd := send(m, c.key)
		m = run(next, cmd)
		if got := lastCall(m); got != c.want {
			t.Errorf("%s: call %q, want %q", c.key, got, c.want)
		}
	}
}

func TestMuteAndUnmute(t *testing.T) {
	m := queueModel(t, 2)
	doCommand(&m, "vol 60")
	if got := doCommand(&m, "mute"); got != "volume 0" || !strings.Contains(m.status, "Muted") {
		t.Errorf(":mute: call %q status %q", got, m.status)
	}
	if got := doCommand(&m, "mute"); got != "volume 60" || !strings.Contains(m.status, "Volume 60%") || strings.Contains(m.status, "Muted") {
		t.Errorf("unmuting: call %q status %q, want the volume from before said as such", got, m.status)
	}
	doCommand(&m, "mute")
	if got := doCommand(&m, "vol +10"); got != "volume 70" {
		t.Errorf(":vol +10 while muted: call %q, want 70 (from the volume before muting)", got)
	}
	doCommand(&m, "mute")
	if got := doCommand(&m, "vol 20"); got != "volume 20" {
		t.Errorf(":vol 20 while muted: call %q", got)
	}
	if got := doCommand(&m, "m"); got != "volume 0" {
		t.Errorf(":m after setting a volume must mute again, got %q", got)
	}
}

// ---- J and K ----

func TestJAndKMoveTheQueueEntry(t *testing.T) {
	m, _ := send(queueModel(t, 4), "tab")
	m = edit(m, "J")
	if got := lastCall(m); got != "movedown 1" {
		t.Errorf("J: call %q, want movedown 1", got)
	}
	m = edit(m, "K")
	if got := lastCall(m); got != "moveup 2" {
		t.Errorf("K: call %q, want moveup 2", got)
	}
}

// ---- the popup, the help, the names ----

func TestVolumeUndoAndShuffleWorkInsideThePopup(t *testing.T) {
	old := shufflePerm
	shufflePerm = reversed
	t.Cleanup(func() { shufflePerm = old })
	m := djModel(t, 4, nil)
	if got := doCommand(&m, "vol 50"); got != "volume 50" || m.mode != modeDJ {
		t.Errorf(":vol in the popup: call %q mode %v", got, m.mode)
	}
	if got := doCommand(&m, "mute"); got != "volume 0" || m.mode != modeDJ {
		t.Errorf(":mute in the popup: call %q mode %v", got, m.mode)
	}
	doCommand(&m, "shuffle")
	if got := reorderCall(m); got != "e3,e2,e1" || m.mode != modeDJ {
		t.Errorf(":shuffle in the popup: reorder %q mode %v", got, m.mode)
	}
}

func TestSeekPrevAndPlayAreRefusedInsideThePopup(t *testing.T) {
	for _, word := range []string{"seek 10", "prev", "play"} {
		m := djModel(t, 4, nil)
		got := doCommand(&m, word)
		if got != "" || m.mode != modeDJ || !strings.Contains(m.status, "Close the popup first") {
			t.Errorf(":%s in the popup: call %q mode %v status %q", word, got, m.mode, m.status)
		}
	}
}

func TestHelpListsTheNewKeys(t *testing.T) {
	m := queueModel(t, 3)
	m.width, m.height = 100, 80
	m, _ = typeCommand(m, "help")
	out := helpText_(m)
	for _, k := range []string{"← / →", "+ / -", "J / K", "u  "} {
		if !strings.Contains(out, k) {
			t.Errorf("the help does not list the key %q:\n%s", k, out)
		}
	}
}

func TestNewCommandNamesShortFormsAndPrefixes(t *testing.T) {
	for word, want := range map[string]string{
		"seek": "seek", "prev": "prev", "play": "play", "undo": "undo", "shuffle": "shuffle", "vol": "vol", "mute": "mute",
		"p": "prev", "u": "undo", "v": "vol", "m": "mute",
		"se": "seek", "pr": "prev", "pl": "play", "un": "undo", "shu": "shuffle", "vo": "vol", "mu": "mute",
	} {
		if c, candidates := findCommand(word); c == nil || c.name != want {
			t.Errorf("findCommand(%q) = %v %v, want %q", word, c, candidates, want)
		}
	}
	// no two commands share a name or a short form, and no short form is the
	// start of... another command's name in a way that hides it
	seen := map[string]string{}
	for _, c := range commands {
		for _, spelling := range append([]string{c.name}, c.short...) {
			if other, dup := seen[spelling]; dup {
				t.Errorf("%q is spelled by both %s and %s", spelling, other, c.name)
			}
			seen[spelling] = c.name
		}
	}
}
