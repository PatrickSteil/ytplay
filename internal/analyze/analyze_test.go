package analyze

import (
	"math"
	"math/cmplx"
	"testing"
)

// clickTrack is a deterministic click train: a short decaying noise burst at
// every beat.
func clickTrack(bpm float64, seconds int) []float32 {
	x := make([]float32, seconds*SampleRate)
	period := 60 / bpm * SampleRate
	seed := uint32(12345)
	for beat := 0.0; int(beat) < len(x); beat += period {
		for i := 0; i < 440 && int(beat)+i < len(x); i++ {
			seed = seed*1664525 + 1013904223
			noise := float64(int32(seed)) / float64(1<<31)
			x[int(beat)+i] = float32(noise * math.Exp(-float64(i)/80))
		}
	}
	return x
}

// chords renders chord progressions as sums of harmonic tones. Each chord is
// a list of MIDI notes; every chord lasts perChord seconds and the sequence
// repeats until total seconds. cents detunes everything.
func chords(prog [][]int, perChord float64, total int, cents float64) []float32 {
	x := make([]float32, total*SampleRate)
	n := int(perChord * SampleRate)
	for pos, ci := 0, 0; pos < len(x); pos, ci = pos+n, ci+1 {
		chord := prog[ci%len(prog)]
		for i := 0; i < n && pos+i < len(x); i++ {
			t := float64(pos+i) / SampleRate
			var v float64
			for _, note := range chord {
				f := 440 * math.Pow(2, (float64(note)-69+cents/100)/12)
				for h := 1; h <= 4; h++ {
					v += math.Sin(2*math.Pi*f*float64(h)*t) / float64(h)
				}
			}
			x[pos+i] = float32(v / 12)
		}
	}
	return x
}

func TestFFTMatchesNaiveDFT(t *testing.T) {
	const n = 64
	re := make([]float64, n)
	im := make([]float64, n)
	in := make([]float64, n)
	for i := range in {
		in[i] = math.Sin(2*math.Pi*5*float64(i)/n) + 0.5*math.Cos(2*math.Pi*12*float64(i)/n) + float64(i%3)
		re[i] = in[i]
	}
	newFFT(n).transform(re, im)
	for k := 0; k < n; k++ {
		var sum complex128
		for j := 0; j < n; j++ {
			sum += complex(in[j], 0) * cmplx.Exp(complex(0, -2*math.Pi*float64(k*j)/n))
		}
		if d := cmplx.Abs(sum - complex(re[k], im[k])); d > 1e-9 {
			t.Fatalf("bin %d differs by %g", k, d)
		}
	}
}

func TestTempoOfClickTracks(t *testing.T) {
	for _, bpm := range []float64{80, 100, 128, 150} {
		res, err := Analyze(clickTrack(bpm, 30))
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(res.BPM-bpm) > 2 {
			t.Errorf("click track at %v BPM detected as %.1f", bpm, res.BPM)
		}
	}
}

func TestKeyOfSimpleProgressions(t *testing.T) {
	const (
		C3, D3, E3, F3, G3, A3, B3 = 48, 50, 52, 53, 55, 57, 59
		C4, D4, E4, F4, G4, A4, B4 = 60, 62, 64, 65, 67, 69, 71
	)
	cMajor := [][]int{{C3, E3, G3, C4}, {F3, A3, C4}, {G3, B3, D4}, {C3, E3, G3, C4}}
	aMinor := [][]int{{A3, C4, E4}, {D3, F3, A3, D4}, {E3, 56, B3}, {A3, C4, E4}}
	tests := []struct {
		name  string
		pcm   []float32
		key   string
		camel string
	}{
		{"C major", chords(cMajor, 2, 40, 0), "C major", "8B"},
		{"A minor", chords(aMinor, 2, 40, 0), "A minor", "8A"},
		{"C major 30 cents sharp", chords(cMajor, 2, 40, 30), "C major", "8B"},
	}
	for _, tt := range tests {
		res, err := Analyze(tt.pcm)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if res.Key != tt.key || res.Camelot != tt.camel {
			t.Errorf("%s: got %q (%s), want %q (%s)", tt.name, res.Key, res.Camelot, tt.key, tt.camel)
		}
		if res.KeyScore <= 0 || res.KeyScore > 1 {
			t.Errorf("%s: score %v out of range", tt.name, res.KeyScore)
		}
	}
}

func TestCamelot(t *testing.T) {
	tests := map[string]string{
		"F# minor": "11A", "A minor": "8A", "C major": "8B", "C# major": "3B",
		"G# minor": "1A", "B major": "1B", "E major": "12B", "D minor": "7A",
	}
	for key, want := range tests {
		if got := camelot(key); got != want {
			t.Errorf("camelot(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestRejectsTooShortOrSilentAudio(t *testing.T) {
	if _, err := Analyze(make([]float32, 3*SampleRate)); err == nil {
		t.Error("3 seconds of audio should be rejected")
	}
	if _, err := Analyze(make([]float32, 30*SampleRate)); err == nil {
		t.Error("silence should be rejected")
	}
	if _, err := Analyze(nil); err == nil {
		t.Error("no audio should be rejected")
	}
}

func TestAnalysisIsDeterministic(t *testing.T) {
	x := clickTrack(120, 20)
	a, _ := Analyze(x)
	b, _ := Analyze(x)
	if a != b {
		t.Errorf("results differ: %+v vs %+v", a, b)
	}
}
