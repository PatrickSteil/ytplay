package autodj

import (
	"reflect"
	"testing"
)

func bpms(v ...float64) []Track {
	t := make([]Track, len(v))
	for i, b := range v {
		t[i] = Track{BPM: b}
	}
	return t
}

func TestArrangeFollowsTheCurrentSongNotTheSlowest(t *testing.T) {
	// 155 plays; sorting by tempo would put 138 next, and 160 last.
	tr := bpms(155, 138, 150, 160, 145, 140)
	got := Arrange(tr, 0, []int{1, 2, 3, 4, 5}, Flow)
	if got[0] != 2 && got[0] != 3 { // 150 or 160 follows 155, never 138
		t.Errorf("order %v: first follower is %v BPM", got, tr[got[0]].BPM)
	}
	// No transition is worse than the worst one of a plain sort.
	if c, plain := pathCost(tr, 0, got, Flow), pathCost(tr, 0, []int{1, 5, 4, 2, 3}, Flow); c > plain {
		t.Errorf("cost %v is worse than the sorted queue's %v", c, plain)
	}
}

func TestArrangeKeepsEverySongOnce(t *testing.T) {
	tr := bpms(120, 0, 128, 90, 0, 126, 124)
	got := Arrange(tr, 0, []int{1, 2, 3, 4, 5, 6}, Flow)
	seen := map[int]bool{}
	for _, i := range got {
		if seen[i] {
			t.Fatalf("%v repeats %d", got, i)
		}
		seen[i] = true
	}
	if len(got) != 6 {
		t.Fatalf("got %v", got)
	}
	if got[4] != 1 || got[5] != 4 {
		t.Errorf("songs without a tempo should be last in their order: %v", got)
	}
}

func TestArrangeFreeOpenerStartsLow(t *testing.T) {
	tr := bpms(128, 124, 126, 130)
	got := Arrange(tr, -1, []int{0, 1, 2, 3}, Flow)
	if !reflect.DeepEqual(got, []int{1, 2, 0, 3}) {
		t.Errorf("got %v, want the tempos to climb from 124", got)
	}
}

func TestDoubleTimeIsNoGap(t *testing.T) {
	if g := tempoGap(70, 140); g > 1e-9 {
		t.Errorf("gap = %v", g)
	}
	if Cost(Track{BPM: 140}, Track{BPM: 70}, Flow) > Cost(Track{BPM: 140}, Track{BPM: 100}, Flow) {
		t.Error("half time should be cheaper than 100 BPM")
	}
}

func TestKeysThatMixWellAreCheaper(t *testing.T) {
	same, rel, near, far := keyCost("8A", "8A"), keyCost("8A", "8B"), keyCost("8A", "9A"), keyCost("8A", "2A")
	if same != 0 || rel != 0 || near != 0 || far <= near {
		t.Errorf("costs: %v %v %v %v", same, rel, near, far)
	}
	if keyCost("12A", "1A") != 0 {
		t.Error("12 and 1 are neighbours on the wheel")
	}
}

func TestArrangePrefersMatchingKeysAtEqualTempo(t *testing.T) {
	tr := []Track{{BPM: 125, Camelot: "8A"}, {BPM: 125, Camelot: "2B"}, {BPM: 125, Camelot: "9A"}}
	got := Arrange(tr, 0, []int{1, 2}, Flow)
	if got[0] != 2 {
		t.Errorf("got %v, want the song in a neighbouring key first", got)
	}
}

func withEnergy(bpm float64, energies ...float64) []Track {
	t := make([]Track, len(energies))
	for i, e := range energies {
		t[i] = Track{BPM: bpm, Energy: e}
	}
	return t
}

func TestFlowKeepsEnergySteady(t *testing.T) {
	tr := withEnergy(125, 0.5, 0.9, 0.55, 0.2, 0.6)
	got := Arrange(tr, 0, []int{1, 2, 3, 4}, Flow)
	prev := 0
	for _, i := range got {
		if d := tr[i].Energy - tr[prev].Energy; d > 0.4 || d < -0.4 {
			t.Errorf("got %v: energy jumps by %.2f", got, d)
		}
		prev = i
	}
	if c, given := pathCost(tr, 0, got, Flow), pathCost(tr, 0, []int{1, 2, 3, 4}, Flow); c > given {
		t.Errorf("arranged cost %v is worse than the order given, %v", c, given)
	}
}

func TestBuildRisesAndWindDownFalls(t *testing.T) {
	tr := withEnergy(125, 0.5, 0.9, 0.6, 0.2, 0.7)
	up := Arrange(tr, 0, []int{1, 2, 3, 4}, Build)
	if !reflect.DeepEqual(up, []int{3, 2, 4, 1}) {
		t.Errorf("build = %v, want energy 0.2, 0.6, 0.7, 0.9", up)
	}
	down := Arrange(tr, 0, []int{1, 2, 3, 4}, WindDown)
	if !reflect.DeepEqual(down, []int{1, 4, 2, 3}) {
		t.Errorf("wind down = %v, want energy 0.9, 0.7, 0.6, 0.2", down)
	}
}

func TestUnknownEnergyCostsNothing(t *testing.T) {
	if energyCost(Track{Energy: 0.9}, Track{}, Build) != 0 {
		t.Error("a song without energy must not be priced by it")
	}
}
