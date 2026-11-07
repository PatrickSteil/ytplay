package analyze

import (
	"reflect"
	"testing"
)

func TestLightKeepsTempoAndKeyAndSkipsTheGrids(t *testing.T) {
	x := drumTrack(124, 0.337, 60, drumOpts{})
	full, err := Analyze(x)
	if err != nil {
		t.Fatal(err)
	}
	light, err := AnalyzeWith(x, Options{Level: Light})
	if err != nil {
		t.Fatal(err)
	}
	if full.Edges.AudioEnd == 0 || !full.Edges.Start.Sure {
		t.Fatalf("the full analysis must have grids for this comparison to mean anything: %+v", full.Edges)
	}
	if light.Level != Light {
		t.Errorf("Level = %v, want Light", light.Level)
	}
	if light.Edges != (Edges{}) {
		t.Errorf("a light analysis has no edges, got %+v", light.Edges)
	}
	if light.BPM != full.BPM || light.TempoScore != full.TempoScore || light.Key != full.Key || light.Camelot != full.Camelot || light.KeyScore != full.KeyScore {
		t.Errorf("tempo and key differ between light and full:\n light %+v\n full  %+v", light, full)
	}
}

func TestFullIsTheDefault(t *testing.T) {
	x := drumTrack(124, 0.337, 60, drumOpts{})
	a, err := Analyze(x)
	if err != nil {
		t.Fatal(err)
	}
	b, err := AnalyzeWith(x, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("Analyze and AnalyzeWith(Options{}) differ:\n %+v\n %+v", a, b)
	}
	if a.Level != Full {
		t.Errorf("Level = %v, want Full", a.Level)
	}
}

// The key is computed beside the tempo on a pool; that changes when things are
// computed, not what.
func TestAnalyzeWithAPoolEqualsWithout(t *testing.T) {
	pool := NewPool(8)
	for name, x := range map[string][]float32{
		"drum track": drumTrack(124, 0.337, 60, drumOpts{}),
		"noise":      noiseTrack(60, 9),
	} {
		for _, level := range []Level{Full, Light} {
			seq, err1 := AnalyzeWith(x, Options{Level: level})
			par, err2 := AnalyzeWith(x, Options{Level: level, Pool: pool})
			if err1 != nil || err2 != nil {
				t.Fatalf("%s: %v %v", name, err1, err2)
			}
			if !reflect.DeepEqual(seq, par) {
				t.Errorf("%s, level %v: the pool changed the result:\n seq %+v\n par %+v", name, level, seq, par)
			}
		}
	}
}

// The ends of an excerpt are not the ends of the song. A song that was only
// partly fetched, or of which only the middle was, must not tell the planner
// that it ends where the excerpt does (it would be cut off there).
func TestAnEdgeOfAnExcerptIsNotAnEdgeOfTheSong(t *testing.T) {
	x := drumTrack(124, 0.337, 60, drumOpts{})
	whole, err := AnalyzeWith(x, Options{})
	if err != nil || whole.Edges.AudioEnd == 0 || !whole.Edges.End.Sure {
		t.Fatalf("setup: %+v %v", whole.Edges, err)
	}
	cases := []struct {
		name           string
		x              Excerpt
		wantStart, end bool // does the start grid survive, does the end
	}{
		{"the whole song", Excerpt{Offset: 0, Duration: 60}, true, true},
		{"the whole song, duration a second off", Excerpt{Offset: 0, Duration: 61}, true, true},
		{"duration unknown", Excerpt{Offset: 0, Duration: 0}, true, true},
		{"the first minute of a ten minute track", Excerpt{Offset: 0, Duration: 600}, true, false},
		{"the middle of a long track", Excerpt{Offset: 20, Duration: 700}, false, false},
	}
	for _, c := range cases {
		res, err := analyzeExcerpt(x, c.x, Options{})
		if err != nil {
			t.Fatal(err)
		}
		e := res.Edges
		if got := e.Start.Period != 0; got != c.wantStart {
			t.Errorf("%s: start grid kept = %v, want %v", c.name, got, c.wantStart)
		}
		if got := e.End.Period != 0 && e.AudioEnd > 0; got != c.end {
			t.Errorf("%s: end kept = %v, want %v (AudioEnd %.1f)", c.name, got, c.end, e.AudioEnd)
		}
		if c.wantStart && e.AudioStart != whole.Edges.AudioStart {
			t.Errorf("%s: AudioStart %.2f, want %.2f", c.name, e.AudioStart, whole.Edges.AudioStart)
		}
		if res.BPM != whole.BPM {
			t.Errorf("%s: the tempo must not change, got %.2f want %.2f", c.name, res.BPM, whole.BPM)
		}
	}
}
