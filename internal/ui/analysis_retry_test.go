package ui

import (
	"slices"
	"strings"
	"testing"
)

func TestAFailedAnalysisIsTriedOnceMoreBeforeItIsGivenUp(t *testing.T) {
	f := &bpmFakes{fail: map[string]bool{"a": true}}
	m := bpmModel(f)
	m, cmd := bpmPlaying(t, m, 0, "a")
	msgs := collect(cmd)
	am := pick[analysisMsg](msgs)
	if am == nil || am.err == nil {
		t.Fatalf("expected the failed analysis of a, got %#v", msgs)
	}
	m, cmd = update(m, *am)
	if st := m.stats["a"]; st.analysisFailed || st.hasRes {
		t.Fatalf("the first failure is not final: %+v", st)
	}
	if cmd == nil {
		t.Fatal("a retry should have been scheduled")
	}
	if got := m.bpmKeyLine(); !strings.Contains(got, "analysing") {
		t.Errorf("while waiting to retry the line says %q", got)
	}

	// the retry works this time
	f.mu.Lock()
	f.fail["a"], f.bpm = false, map[string]float64{"a": 128}
	f.mu.Unlock()
	m, cmd = update(m, retryAnalysisMsg{id: "a"})
	m = settle(m, cmd)
	if st := m.stats["a"]; !st.hasRes || st.res.BPM != 128 || st.analysisFailed {
		t.Errorf("the retry did not give a result: %+v", st)
	}
	if got := f.asked(); !slices.Equal(got, []string{"a", "a"}) {
		t.Errorf("analysed %v, want a twice", got)
	}
}

func TestAnalysisThatFailsTwiceSaysWhy(t *testing.T) {
	f := &bpmFakes{fail: map[string]bool{"a": true}}
	m := bpmModel(f)
	m, cmd := bpmPlaying(t, m, 0, "a")
	m = settle(m, cmd)
	m, cmd = update(m, retryAnalysisMsg{id: "a"})
	m = settle(m, cmd)
	st := m.stats["a"]
	if !st.analysisFailed || st.analysisErr != "no audio" {
		t.Fatalf("a should be given up on with its reason: %+v", st)
	}
	if got := m.bpmKeyLine(); !strings.Contains(got, "analysis failed") || !strings.Contains(got, "no audio") {
		t.Errorf("the line says %q, want the failure and its reason", got)
	}
	// nothing asks again
	m, cmd = update(m, retryAnalysisMsg{id: "a"})
	collect(cmd)
	if got := f.asked(); len(got) != 2 {
		t.Errorf("analysed %v, want exactly two attempts", got)
	}
}

func TestRetryIsForgottenWhenTheSongIsGone(t *testing.T) {
	f := &bpmFakes{fail: map[string]bool{"a": true}}
	m := bpmModel(f)
	m, cmd := bpmPlaying(t, m, 0, "a", "b")
	m = settle(m, cmd)
	m, cmd = bpmPlaying(t, m, 0, "b") // a is no longer in the queue
	collect(cmd)
	n := len(f.asked())
	_, cmd = update(m, retryAnalysisMsg{id: "a"})
	collect(cmd)
	if len(f.asked()) != n {
		t.Error("a song that is no longer playing or queued must not be analysed again")
	}
}
