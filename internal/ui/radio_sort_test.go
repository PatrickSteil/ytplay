package ui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PatrickSteil/ytplay/internal/analyze"
	"github.com/PatrickSteil/ytplay/internal/yt"
)

const watchURL = "https://www.youtube.com/watch?v="

// sortedRadioModel is a queue c (playing), u0 (100 BPM), u1 (140 BPM) that was
// sorted by BPM in the DJ popup, with autodj on and a radio that finds r0 (120
// BPM) and r1 (90 BPM).
func sortedRadioModel(t *testing.T, autoDJ, sorted bool) Model {
	t.Helper()
	var calls []similarCall
	m := radioModel(t, &calls, []yt.Result{{ID: "r0", Title: "R0"}, {ID: "r1", Title: "R1"}}, nil)
	m.bpmSet["u0"], m.bpmSet["u1"], m.bpmSet["r0"], m.bpmSet["r1"] = 100, 140, 120, 90
	// analyses only finish when a test says so
	m.deps.Analyze = func(ctx context.Context, id string, _ float64, _ analyze.Level) (analyze.Result, error) {
		<-ctx.Done()
		return analyze.Result{}, ctx.Err()
	}
	m.autoDJ, m.djActive = autoDJ, true
	if sorted {
		m, _ = typeCommand(m, "dj")
		m = editCommand(m, "sort")
		m, _ = send(m, "esc")
	}
	return m
}

// radioArrives runs :radio and lets the player's playlist show the new songs.
func radioArrives(t *testing.T, m Model) Model {
	t.Helper()
	m, cmd := typeCommand(m, "radio")
	m = run(m, cmd)
	return feed(m, plEvent(t, "p0", "*c", "u0", "u1", "r0", "r1"))
}

// feed delivers a message and runs the commands it starts.
func feed(m Model, msg tea.Msg) Model {
	m, cmd := update(m, msg)
	return run(m, cmd)
}

func lastReorder(m Model) string {
	calls := actionsOf(m).calls
	for i := len(calls) - 1; i >= 0; i-- {
		if strings.HasPrefix(calls[i], "reorder ") {
			return strings.ReplaceAll(strings.TrimPrefix(calls[i], "reorder "), watchURL, "")
		}
	}
	return ""
}

func analysed(t *testing.T, m Model, id string) Model {
	t.Helper()
	return feed(m, analysisMsg{id: id, res: analyze.Result{BPM: m.bpmSet[id]}})
}

func TestRadioSongsAreSortedInWhenTheQueueWasSortedAndAutoDJIsOn(t *testing.T) {
	m := sortedRadioModel(t, true, true)
	if got := lastReorder(m); got != "u0,u1" {
		t.Fatalf("after the first sort: %q", got)
	}
	m = radioArrives(t, m)
	m = analysed(t, m, "r0")
	if got := lastReorder(m); got != "u0,u1" {
		t.Errorf("sorted before every new song was analysed: %q", got)
	}
	m = analysed(t, m, "r1")
	if got := lastReorder(m); got != "r1,u0,r0,u1" {
		t.Errorf("after the radio the queue is %q, want r1,u0,r0,u1 (slowest first, as before)", got)
	}
	// sorting again changes nothing
	m = editCommand(m, "dj")
	m = editCommand(m, "sort")
	if got := lastReorder(m); got != "r1,u0,r0,u1" {
		t.Errorf("the next sort gave %q, want the same arrangement", got)
	}
}

func TestRadioSongsAreSortedInAfterTheyAreAnalysedEvenIfTheyArriveFirst(t *testing.T) {
	m := sortedRadioModel(t, true, true)
	m = analysed(t, m, "r0") // analysed before the player lists them
	m = analysed(t, m, "r1")
	m, cmd := typeCommand(m, "radio")
	m = run(m, cmd)
	m = feed(m, plEvent(t, "p0", "*c", "u0", "u1", "r0", "r1"))
	if got := lastReorder(m); got != "r1,u0,r0,u1" {
		t.Errorf("queue %q", got)
	}
}

func TestRadioDoesNotSortWithoutAutoDJOrWithoutAnEarlierSort(t *testing.T) {
	for name, m := range map[string]Model{
		"autodj off":   sortedRadioModel(t, false, true),
		"never sorted": sortedRadioModel(t, true, false),
	} {
		before := lastReorder(m)
		m = radioArrives(t, m)
		m = analysed(t, m, "r0")
		m = analysed(t, m, "r1")
		if got := lastReorder(m); got != before {
			t.Errorf("%s: the queue was reordered: %q", name, got)
		}
	}
}

func TestAHandEditStopsTheQueueFromBeingKeptSorted(t *testing.T) {
	m := sortedRadioModel(t, true, true)
	m = editCommand(m, "shuffle")
	before := lastReorder(m)
	m = radioArrives(t, m)
	m = analysed(t, m, "r0")
	m = analysed(t, m, "r1")
	if got := lastReorder(m); got != before {
		t.Errorf("a shuffled queue was sorted again: %q", got)
	}
}

func TestRadioSongsAreNotSortedInBeforeThePlayerListsThem(t *testing.T) {
	m := sortedRadioModel(t, true, true)
	m, cmd := typeCommand(m, "radio")
	m = run(m, cmd)
	m = analysed(t, m, "r0") // analysed, but not in the queue yet
	m = analysed(t, m, "r1")
	if got := lastReorder(m); got != "u0,u1" {
		t.Fatalf("sorted before the songs were in the queue: %q", got)
	}
	m = feed(m, plEvent(t, "p0", "*c", "u0", "u1", "r0", "r1"))
	if got := lastReorder(m); got != "r1,u0,r0,u1" {
		t.Errorf("queue %q", got)
	}
}

func TestSwitchingAutoDJOffBeforeTheAnalysesAreDoneStopsTheSort(t *testing.T) {
	m := radioArrives(t, sortedRadioModel(t, true, true))
	m.autoDJ = false
	m = analysed(t, m, "r0")
	m = analysed(t, m, "r1")
	if got := lastReorder(m); got != "u0,u1" {
		t.Errorf("sorted with autodj off: %q", got)
	}
}

func TestMovingASongByHandStopsTheQueueFromBeingKeptSorted(t *testing.T) {
	m := sortedRadioModel(t, true, true)
	m, _ = send(m, "tab")
	m, cmd := send(m, "J")
	m = run(m, cmd)
	before := lastReorder(m)
	m = radioArrives(t, m)
	m = analysed(t, m, "r0")
	m = analysed(t, m, "r1")
	if got := lastReorder(m); got != before {
		t.Errorf("a hand-ordered queue was sorted again: %q", got)
	}
}
