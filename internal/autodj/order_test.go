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
	got := Arrange(tr, 0, []int{1, 2, 3, 4, 5})
	if got[0] != 2 && got[0] != 3 { // 150 or 160 follows 155, never 138
		t.Errorf("order %v: first follower is %v BPM", got, tr[got[0]].BPM)
	}
	// No transition is worse than the worst one of a plain sort.
	if c, plain := pathCost(tr, 0, got), pathCost(tr, 0, []int{1, 5, 4, 2, 3}); c > plain {
		t.Errorf("cost %v is worse than the sorted queue's %v", c, plain)
	}
}

func TestArrangeKeepsEverySongOnce(t *testing.T) {
	tr := bpms(120, 0, 128, 90, 0, 126, 124)
	got := Arrange(tr, 0, []int{1, 2, 3, 4, 5, 6})
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
	got := Arrange(tr, -1, []int{0, 1, 2, 3})
	if !reflect.DeepEqual(got, []int{1, 2, 0, 3}) {
		t.Errorf("got %v, want the tempos to climb from 124", got)
	}
}

func TestDoubleTimeIsNoGap(t *testing.T) {
	if g := tempoGap(70, 140); g > 1e-9 {
		t.Errorf("gap = %v", g)
	}
	if Cost(Track{BPM: 140}, Track{BPM: 70}) > Cost(Track{BPM: 140}, Track{BPM: 100}) {
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
	tr := []Track{{125, "8A"}, {125, "2B"}, {125, "9A"}}
	got := Arrange(tr, 0, []int{1, 2})
	if got[0] != 2 {
		t.Errorf("got %v, want the song in a neighbouring key first", got)
	}
}
