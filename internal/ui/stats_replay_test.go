package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// A song that stops and starts again at once (the queue is replaced with it)
// has its running lookups cancelled by the second start. They must be asked
// again then, or its tempo, key and waveform stay missing for good.
func TestStatsOfASongThatRestartsAtOnceAreNotLost(t *testing.T) {
	var f statsFakes
	m := statsModel(&f)
	m, first := startStats(t, m, "0", "*a")
	m, gap := update(m, propEvent("playlist-pos", "null")) // nothing plays for a moment
	m, again := update(m, propEvent("playlist-pos", "0"))  // and a plays again
	// the commands only run now: the first ones were cancelled by the next event
	msgs := append(append(collect(first), collect(gap)...), collect(again)...)
	for _, msg := range msgs {
		var next tea.Cmd
		m, next = update(m, msg)
		msgs = append(msgs, collect(next)...)
	}
	if st := m.stats["a"]; !st.hasRes || !st.hasInfo {
		t.Errorf("a was left without its stats: analysis %v, info %v", st.hasRes, st.hasInfo)
	}
	if st := m.stats["a"]; st.analysisFailed || st.infoFailed {
		t.Errorf("a was marked as failed: %+v", st)
	}
}
