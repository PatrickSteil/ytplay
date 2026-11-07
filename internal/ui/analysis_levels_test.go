package ui

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/PatrickSteil/ytplay/internal/analyze"
	"github.com/PatrickSteil/ytplay/internal/yt"
)

// levelFakes is an analyzer that records what it was asked and, like the real
// one, labels its result with the level asked for: a Light result has no edges,
// a Full one has some.
type levelFakes struct {
	mu    sync.Mutex
	calls []levelCall
	fail  map[string]bool // songs whose every analysis fails
	// failFull are songs whose full analysis fails but whose light one works
	failFull map[string]bool
	bpm      map[string]float64
}

type levelCall struct {
	id    string
	level analyze.Level
}

func (f *levelFakes) analyze(ctx context.Context, id string, duration float64, level analyze.Level) (analyze.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, levelCall{id, level})
	fail, bpm := f.fail[id] || (level == analyze.Full && f.failFull[id]), f.bpm[id]
	f.mu.Unlock()
	if fail {
		return analyze.Result{}, errors.New("no audio")
	}
	res := analyze.Result{BPM: bpm, Level: level}
	if level == analyze.Full {
		res.Edges = analyze.Edges{AudioStart: 0.2, AudioEnd: 190}
	}
	return res, nil
}

// levels is the levels song id was analysed at, in order.
func (f *levelFakes) levels(id string) []analyze.Level {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []analyze.Level
	for _, c := range f.calls {
		if c.id == id {
			out = append(out, c.level)
		}
	}
	return out
}

// full is the sorted ids that were analysed at the Full level.
func (f *levelFakes) full() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if c.level == analyze.Full {
			out = append(out, c.id)
		}
	}
	slices.Sort(out)
	return out
}

func levelModel(f *levelFakes) Model {
	m := New(Deps{Player: &fakeActions{}, Analyze: f.analyze})
	m.statsDelay = 0
	m.width, m.height = 140, 40
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		m.tracks[id] = yt.Result{ID: id, Title: strings.ToUpper(id), Channel: "Chan", Duration: 200}
	}
	return m
}

func closePopup(m Model) Model {
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	return m
}

func TestWithoutTheDJPopupOnlyThePlayingSongIsAnalysedLight(t *testing.T) {
	f := &levelFakes{}
	m := levelModel(f)
	m, cmd := bpmPlaying(t, m, 0, "a", "b", "c", "d")
	collect(cmd)
	m, cmd = update(m, plEvent(t, "*a", "b", "c", "d", "e")) // the queue changes too
	collect(cmd)
	if want := []levelCall{{"a", analyze.Light}}; !reflect.DeepEqual(f.calls, want) {
		t.Errorf("analysed %v, want only the playing song, light, until someone opens the DJ popup", f.calls)
	}
}

func TestOpeningTheDJPopupAnalysesFourSongsAtATimeFull(t *testing.T) {
	f := &levelFakes{}
	m := levelModel(f)
	m, cmd := bpmPlaying(t, m, 0, "a", "b", "c", "d", "e", "f")
	msgs := collect(cmd)
	m, _ = bpmDeliver(t, m, msgs, "a") // the playing song has its light result
	m, cmd = openDJ(t, m)
	msgs = collect(cmd)
	// the playing song is upgraded and the next three queued: four in flight
	if got := f.full(); !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("analysed full: %v, want the playing song and the next three", got)
	}
	m, cmd = bpmDeliver(t, m, msgs, "b")
	msgs = append(msgs, collect(cmd)...)
	if got := f.full(); !slices.Equal(got, []string{"a", "b", "c", "d", "e"}) {
		t.Fatalf("after one finished: %v, want e to have started", got)
	}
	m, cmd = bpmDeliver(t, m, msgs, "c")
	collect(cmd)
	if got := f.full(); !slices.Equal(got, []string{"a", "b", "c", "d", "e", "f"}) {
		t.Errorf("after two finished: %v, want f to have started", got)
	}
}

func TestOpeningTheDJPopupUpgradesThePlayingSongOnce(t *testing.T) {
	f := &levelFakes{}
	m := levelModel(f)
	m, cmd := bpmPlaying(t, m, 0, "a", "b")
	m, _ = bpmDeliver(t, m, collect(cmd), "a")
	m, cmd = openDJ(t, m)
	msgs := collect(cmd)
	if got := f.levels("a"); !slices.Equal(got, []analyze.Level{analyze.Light, analyze.Full}) {
		t.Fatalf("the playing song was analysed at %v, want light then full", got)
	}
	for i := 0; i < 2; i++ { // reopening while the full analysis is still running asks for nothing more
		m = closePopup(m)
		m, cmd = openDJ(t, m)
		collect(cmd)
	}
	if got := f.levels("a"); len(got) != 2 {
		t.Fatalf("reopening the popup while the full analysis runs: a analysed at %v, want it not asked a third time", got)
	}
	m, cmd = bpmDeliver(t, m, msgs, "a")
	collect(cmd)
	if song, ok := m.songs.lookup("https://www.youtube.com/watch?v=a"); !ok || song.Edges.AudioEnd != 190 {
		t.Errorf("the planner's lookup for the playing song = %+v, %v, want the edges of the full analysis", song, ok)
	}
	for i := 0; i < 3; i++ { // opening the popup again, or a second result, starts nothing more
		m = closePopup(m)
		m, cmd = openDJ(t, m)
		collect(cmd)
	}
	if got := f.levels("a"); len(got) != 2 {
		t.Errorf("the playing song was analysed %d times (%v), want twice: light, then full", len(got), got)
	}
}

func TestUpgradeWhileTheLightAnalysisIsStillRunning(t *testing.T) {
	f := &levelFakes{}
	m := levelModel(f)
	m, cmd := bpmPlaying(t, m, 0, "a", "b")
	msgs := collect(cmd) // the light result is on its way, not delivered yet
	m, cmd = openDJ(t, m)
	collect(cmd)
	if got := f.levels("a"); !slices.Equal(got, []analyze.Level{analyze.Light}) {
		t.Fatalf("while the light analysis runs, a was analysed at %v, want it not asked again yet", got)
	}
	m, cmd = bpmDeliver(t, m, msgs, "a") // now it arrives
	collect(cmd)
	if got := f.levels("a"); !slices.Equal(got, []analyze.Level{analyze.Light, analyze.Full}) {
		t.Errorf("after the light result arrived, a was analysed at %v, want exactly one full analysis to follow", got)
	}
}

func TestAFailedOrBeatlessSongIsNotReanalysedInALoop(t *testing.T) {
	// a failed analysis is not retried, however often the popup is opened
	f := &levelFakes{fail: map[string]bool{"a": true}}
	m := levelModel(f)
	m, cmd := bpmPlaying(t, m, 0, "a", "b")
	m, _ = bpmDeliver(t, m, collect(cmd), "a")
	for i := 0; i < 3; i++ {
		m, cmd = openDJ(t, m)
		collect(cmd)
		m = closePopup(m)
	}
	if got := f.levels("a"); len(got) != 1 {
		t.Errorf("a failed song was analysed %d times (%v), want once", len(got), got)
	}

	// a song without a beat (tempo 0) is upgraded once, for where its music ends,
	// and then left alone
	f = &levelFakes{}
	m = levelModel(f)
	m, cmd = bpmPlaying(t, m, 0, "a", "b")
	m, _ = bpmDeliver(t, m, collect(cmd), "a")
	m, cmd = openDJ(t, m)
	msgs := collect(cmd)
	m, cmd = bpmDeliver(t, m, msgs, "a")
	collect(cmd)
	for i := 0; i < 3; i++ {
		m = closePopup(m)
		m, cmd = openDJ(t, m)
		collect(cmd)
	}
	if got := f.levels("a"); !slices.Equal(got, []analyze.Level{analyze.Light, analyze.Full}) {
		t.Errorf("a beatless song was analysed at %v, want light, then full, then never again", got)
	}
}

func TestClearingTheQueueStartsNoAnalysesForRemovedSongs(t *testing.T) {
	f := &levelFakes{}
	m := levelModel(f)
	m, cmd := bpmPlaying(t, m, 0, "a", "b", "c", "d", "e", "f")
	m, _ = bpmDeliver(t, m, collect(cmd), "a")
	m, cmd = openDJ(t, m)
	msgs := collect(cmd)
	if got := f.full(); !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("setup: analysed full %v", got)
	}
	m, cmd = update(m, plEvent(t, "*a")) // the queue is cleared
	collect(cmd)
	m, cmd = bpmDeliver(t, m, msgs, "b") // a slot frees up
	collect(cmd)
	if got := f.full(); !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Errorf("analysed full %v, want nothing more for the songs that were removed from the queue", got)
	}
}

// A full analysis that fails must not take away the tempo the light one found.
func TestAFailedUpgradeKeepsTheLightResult(t *testing.T) {
	f := &levelFakes{failFull: map[string]bool{"a": true}, bpm: map[string]float64{"a": 120}}
	m := levelModel(f)
	m, cmd := bpmPlaying(t, m, 0, "a", "b")
	m, _ = bpmDeliver(t, m, collect(cmd), "a")
	m, cmd = openDJ(t, m)
	m, _ = bpmDeliver(t, m, collect(cmd), "a") // the upgrade fails
	if got := m.tempoOf("a"); got != 120 {
		t.Errorf("tempo after the failed upgrade = %v, want the light analysis' 120", got)
	}
	if line := ansi.Strip(m.bpmKeyLine()); !strings.Contains(line, "120 BPM") {
		t.Errorf("the header says %q after the failed upgrade, want it to keep showing 120 BPM", line)
	}
	if got := f.levels("a"); len(got) != 2 {
		t.Errorf("a was analysed %d times (%v), want light then one failed full, not a retry", len(got), got)
	}
}
