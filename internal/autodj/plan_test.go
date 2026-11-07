package autodj

import (
	"math"
	"strings"
	"testing"

	"github.com/PatrickSteil/ytplay/internal/analyze"
)

func near(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.4f, want %.4f (+-%g)", what, got, want, tol)
	}
}

// edges builds the analysis of a song with sure grids at both ends.
func sureEdges(audioStart, audioEnd, startPeriod, startAnchor, endPeriod, endAnchor float64) analyze.Edges {
	return analyze.Edges{
		AudioStart: audioStart, AudioEnd: audioEnd,
		Start: analyze.BeatGrid{Period: startPeriod, Anchor: startAnchor, Sure: true},
		End:   analyze.BeatGrid{Period: endPeriod, Anchor: endAnchor, Sure: true},
	}
}

func songA() Song { return Song{Length: 230, Edges: sureEdges(0.1, 200.6, 0.5, 0.4, 0.5, 200.2)} }
func songB() Song { return Song{Length: 200, Edges: sureEdges(0.2, 195, 0.48, 0.3, 0.48, 194.8)} }

func TestBeatMatchWhenBothEndsAreSure(t *testing.T) {
	p := For(songA(), songB())
	if p.Kind != BeatMatch {
		t.Fatalf("kind = %v (%s), want a beat-matched transition", p.Kind, p.Why)
	}
	near(t, "AStart", p.AStart, 200.2-16*0.5, 1e-9)
	near(t, "BFrom", p.BFrom, 0.3, 1e-9)
	near(t, "BSpeed", p.BSpeed, 0.48/0.5, 1e-9)
	near(t, "Overlap", p.Overlap, 200.6-(200.2-16*0.5), 1e-9)
	near(t, "EaseBack", p.EaseBack, EaseSeconds, 1e-9)
	if !strings.Contains(p.Why, "beat") {
		t.Errorf("Why = %q, should say it is beat-matched", p.Why)
	}
}

func TestEqualTemposNeedNoEasing(t *testing.T) {
	b := songB()
	b.Edges.Start.Period = 0.5
	if p := For(songA(), b); p.Kind != BeatMatch || p.BSpeed != 1 || p.EaseBack != 0 {
		t.Errorf("plan = %+v, want a beat-match at speed 1 without easing back", p)
	}
}

func TestCrossfadeWhenEitherEndIsNotSure(t *testing.T) {
	a, b := songA(), songB()
	a.Edges.End.Sure = false
	if p := For(a, songB()); p.Kind != Crossfade || !strings.Contains(p.Why, "playing song") {
		t.Errorf("unsure end of the playing song: %+v", p)
	}
	b.Edges.Start.Sure = false
	if p := For(songA(), b); p.Kind != Crossfade || !strings.Contains(p.Why, "next song") {
		t.Errorf("unsure start of the next song: %+v", p)
	}
}

func TestCrossfadeWhenTemposDiffer(t *testing.T) {
	for name, period := range map[string]float64{
		"7% apart":                  0.5 * 1.07,
		"-7% apart":                 0.5 / 1.07,
		"double time, but 7% off":   0.25 * 1.07,
		"half time, but 7% off":     1.0 / 1.07,
		"three against two":         0.5 * 1.5,
		"two against three":         0.5 / 1.5,
		"four against three":        0.5 * 4 / 3,
		"between single and double": 0.35,
	} {
		b := songB()
		b.Edges.Start.Period = period
		if p := For(songA(), b); p.Kind != Crossfade || !strings.Contains(p.Why, "tempo") {
			t.Errorf("%s: plan = %+v, want a crossfade because of the tempo", name, p)
		}
	}
	b := songB()
	b.Edges.Start.Period = 0.5 * 1.059 // just inside the limit
	if p := For(songA(), b); p.Kind != BeatMatch {
		t.Errorf("5.9%% apart should still be beat-matched: %+v", p)
	}
}

// A song at 70 BPM can be mixed with one at 140: every beat of the slower one
// is met by every second beat of the faster one, so neither needs to be sped up
// to double or halved.
func TestHalfAndDoubleTimeAreBeatMatchedWithoutBigSpeedChanges(t *testing.T) {
	for name, tc := range map[string]struct {
		periodB   float64 // the next song's beat period; the playing song's is 0.5 s
		wantSpeed float64
		wantWhy   string
	}{
		"double time":                 {0.25, 1, "double time"},
		"half time":                   {1.0, 1, "half time"},
		"double time, next is faster": {0.25 / 1.04, 1 / 1.04, "double time"},
		"double time, next is slower": {0.25 * 1.04, 1.04, "double time"},
		"half time, next is faster":   {1.0 / 1.04, 1 / 1.04, "half time"},
		"half time, next is slower":   {1.0 * 1.04, 1.04, "half time"},
		"just inside, double time":    {0.25 * 1.059, 1.059, "double time"},
		"just inside, half time":      {1.0 / 1.059, 1 / 1.059, "half time"},
	} {
		b := songB()
		b.Edges.Start.Period = tc.periodB
		p := For(songA(), b)
		if p.Kind != BeatMatch {
			t.Errorf("%s: plan = %+v (%s), want a beat-match", name, p, p.Why)
			continue
		}
		near(t, name+": BSpeed", p.BSpeed, tc.wantSpeed, 1e-9)
		if !strings.Contains(p.Why, tc.wantWhy) {
			t.Errorf("%s: Why = %q, should mention %q", name, p.Why, tc.wantWhy)
		}
		// the song's own beat (the anchor, 16 of its beats from the end) is the
		// same as for a song at the same tempo: only the speed differs
		near(t, name+": AStart", p.AStart, 200.2-16*0.5, 1e-9)
		near(t, name+": BFrom", p.BFrom, 0.3, 1e-9)
	}
}

func TestSameTempoIsNotCalledDoubleOrHalfTime(t *testing.T) {
	if p := For(songA(), songB()); strings.Contains(p.Why, "double") || strings.Contains(p.Why, "half") {
		t.Errorf("Why = %q: songs at the same tempo are not in double or half time", p.Why)
	}
}

func TestDoubleTimeEasesBackToItsOwnSpeedLikeAnyOtherSpeedChange(t *testing.T) {
	b := songB()
	b.Edges.Start.Period = 0.25 * 1.04
	if p := For(songA(), b); p.EaseBack != EaseSeconds {
		t.Errorf("EaseBack = %v, want %v", p.EaseBack, EaseSeconds)
	}
	b.Edges.Start.Period = 0.25 // exactly double: nothing to ease
	if p := For(songA(), b); p.EaseBack != 0 {
		t.Errorf("EaseBack = %v, want none at speed 1", p.EaseBack)
	}
}

func TestCrossfadeWhenAnalysisIsMissing(t *testing.T) {
	a, b := Song{Length: 230}, Song{Length: 200}
	p := For(a, b)
	if p.Kind != Crossfade {
		t.Fatalf("kind = %v", p.Kind)
	}
	near(t, "AStart", p.AStart, 230-FadeSeconds, 1e-9)
	near(t, "BFrom", p.BFrom, 0, 1e-9)
	near(t, "Overlap", p.Overlap, FadeSeconds, 1e-9)
	near(t, "BSpeed", p.BSpeed, 1, 1e-9)
	near(t, "EaseBack", p.EaseBack, 0, 1e-9)
	if !strings.Contains(p.Why, "not analysed") {
		t.Errorf("Why = %q, should say there is no analysis", p.Why)
	}
}

func TestCrossfadeUsesTheAudibleEndAndStart(t *testing.T) {
	a := Song{Length: 205, Edges: analyze.Edges{AudioStart: 0, AudioEnd: 200}}
	b := Song{Length: 180, Edges: analyze.Edges{AudioStart: 1.3, AudioEnd: 178}}
	p := For(a, b)
	near(t, "AStart", p.AStart, 200-FadeSeconds, 1e-9)
	near(t, "BFrom", p.BFrom, 1.3, 1e-9)
}

func TestShortSongsGetAShorterCrossfade(t *testing.T) {
	a := Song{Length: 10, Edges: analyze.Edges{AudioStart: 0, AudioEnd: 10}}
	p := For(a, songB())
	near(t, "Overlap", p.Overlap, 4, 1e-9)
	near(t, "AStart", p.AStart, 6, 1e-9)
}

func TestNoBeatMatchWhenItWouldRunPastTheEnd(t *testing.T) {
	early := songA() // the playing song's beat ends before 16 beats have passed
	early.Edges.End.Anchor = 5
	if p := For(early, songB()); p.Kind != Crossfade {
		t.Errorf("anchor too early for %d beats: %+v", Beats, p)
	}
	short := songB() // the next song has too little left after its first beat
	short.Length, short.Edges.AudioEnd = 6, 6
	if p := For(songA(), short); p.Kind != Crossfade {
		t.Errorf("next song too short for the overlap: %+v", p)
	}
}

func TestBeatMatchEndsWhereTheBeatEndsWhenTheOutroIsLong(t *testing.T) {
	a := songA()
	a.Edges.AudioEnd = 215 // fifteen seconds of beatless outro after the last beat
	p := For(a, songB())
	if p.Kind != BeatMatch {
		t.Fatalf("plan = %+v", p)
	}
	// the overlap stops a beat after the last beat, it does not run through the outro
	near(t, "Overlap", p.Overlap, 16*0.5+0.5, 1e-9)

	// with thirty seconds of outro the overlap would start 37.6 s before the end,
	// outside the last 30 s
	a.Edges.AudioEnd = 230
	if p := For(a, songB()); p.Kind != Crossfade {
		t.Errorf("a 30 s outro: plan = %+v, want a crossfade", p)
	}
}

// The overlap lies inside the last LastSeconds of the playing song's music.
func TestBeatMatchStaysInsideTheLast30Seconds(t *testing.T) {
	a := songA() // AudioEnd 200.6, so the window opens at 170.6; the overlap starts 8 s before the anchor
	a.Edges.End.Anchor = 178.7
	if p := For(a, songB()); p.Kind != BeatMatch {
		t.Errorf("overlap starting at 170.7 s: plan = %+v (%s), want a beat-match", p, p.Why)
	}
	a.Edges.End.Anchor = 178.5
	p := For(a, songB())
	if p.Kind != Crossfade || !strings.Contains(p.Why, "last 30 s") {
		t.Errorf("overlap starting at 170.5 s: plan = %+v (%s), want a crossfade that says why", p, p.Why)
	}
}

// A song shorter than the window has no 30 s before its end: the window cannot
// open before the music does.
func TestAShortPlayingSongIsStillBeatMatched(t *testing.T) {
	a := Song{Length: 25, Edges: sureEdges(0.1, 24.5, 0.5, 0.4, 0.5, 24.1)}
	if p := For(a, songB()); p.Kind != BeatMatch {
		t.Errorf("plan = %+v (%s), want a beat-match", p, p.Why)
	}
}

func TestKindStrings(t *testing.T) {
	if Crossfade.String() != "crossfade" || BeatMatch.String() != "beat-match" {
		t.Errorf("strings: %q %q", Crossfade, BeatMatch)
	}
}

// ---- end to end, against known beats ----

// kicks is a kick on every beat of a track of `seconds` (a 55 Hz thump with a
// click) with a quiet pad under it; it returns the track and the kick times.
func kicks(bpm, first, seconds, silenceEnd float64) ([]float32, []float64) {
	sr := float64(analyze.SampleRate)
	n := int(seconds * sr)
	x := make([]float32, n)
	for i := range x {
		t := float64(i) / sr
		x[i] = float32(0.06 * (math.Sin(2*math.Pi*220*t) + math.Sin(2*math.Pi*277*t) + math.Sin(2*math.Pi*330*t)))
	}
	seed := uint32(11)
	var beats []float64
	for t := first; t < seconds-0.3; t += 60 / bpm {
		beats = append(beats, t)
		i0 := int(t * sr)
		for j := 0; j < int(0.25*sr) && i0+j < n; j++ {
			tt := float64(j) / sr
			v := 0.8 * math.Exp(-tt/0.06) * math.Sin(2*math.Pi*55*tt)
			if j < 110 {
				seed = seed*1664525 + 1013904223
				v += 0.3 * float64(int32(seed)) / float64(1<<31) * (1 - float64(j)/110)
			}
			x[i0+j] += float32(v)
		}
	}
	return append(x, make([]float32, int(silenceEnd*sr))...), beats
}

func TestEndToEndBeatsLineUp(t *testing.T) {
	for name, tc := range map[string]struct{ bpmA, bpmB, firstA, firstB float64 }{
		"B slower than A": {124, 120, 0.337, 0.52},
		"B faster than A": {120, 126, 0.41, 0.15},
		"same tempo":      {122, 122, 0.2, 0.45},
	} {
		xa, beatsA := kicks(tc.bpmA, tc.firstA, 100, 1.2)
		xb, beatsB := kicks(tc.bpmB, tc.firstB, 100, 1.2)
		ra, err := analyze.Analyze(xa)
		if err != nil {
			t.Fatal(err)
		}
		rb, err := analyze.Analyze(xb)
		if err != nil {
			t.Fatal(err)
		}
		p := For(
			Song{Length: float64(len(xa)) / analyze.SampleRate, Edges: ra.Edges},
			Song{Length: float64(len(xb)) / analyze.SampleRate, Edges: rb.Edges},
		)
		if p.Kind != BeatMatch {
			t.Errorf("%s: plan = %+v (%s)", name, p, p.Why)
			continue
		}
		// Every kick of B that sounds while A's last bars play, mapped to real
		// time, must fall on a kick of A.
		worst, n := 0.0, 0
		for _, tb := range beatsB {
			if tb < p.BFrom {
				continue
			}
			real := p.AStart + (tb-p.BFrom)/p.BSpeed // seconds on A's clock
			if real > beatsA[len(beatsA)-1]+0.03 {
				break // past A's last kick there is nothing of A left to line up with
			}
			nearest := math.Inf(1)
			for _, ta := range beatsA {
				nearest = math.Min(nearest, math.Abs(ta-real))
			}
			worst = math.Max(worst, nearest)
			n++
		}
		if n < 14 {
			t.Errorf("%s: only %d of B's beats fall in the overlap", name, n)
		}
		if worst > 0.025 {
			t.Errorf("%s: a beat of B misses a beat of A by %.0f ms, want within 25 ms", name, worst*1000)
		}
		t.Logf("%s: %d beats overlap, worst miss %.1f ms (speed %.4f)", name, n, worst*1000, p.BSpeed)
	}
}

// worstMiss is how far the kick of `of` that is furthest from any kick of `from`
// is from its nearest one, over the kicks of `of` that lie in [lo, hi].
func worstMiss(of, from []float64, lo, hi float64) (worst float64, n int) {
	for _, t := range of {
		if t < lo || t > hi {
			continue
		}
		nearest := math.Inf(1)
		for _, u := range from {
			nearest = math.Min(nearest, math.Abs(u-t))
		}
		worst = math.Max(worst, nearest)
		n++
	}
	return worst, n
}

// At 70 BPM against 140 the slower track's every kick must meet a kick of the
// faster one, with neither sped up or slowed down to double or half.
func TestEndToEndHalfAndDoubleTimeBeatsLineUp(t *testing.T) {
	for name, tc := range map[string]struct {
		bpmA, bpmB, firstA, firstB float64
		slowKicks                  int // how many kicks of the slower track fall in the overlap, at least
	}{
		// the playing song's last 16 beats are the overlap: 16 or 17 kicks if it
		// is the slower track, half as many if the next song is
		"next song twice as fast":                {70, 140, 0.337, 0.52, 14},
		"next song half as fast":                 {140, 70, 0.41, 0.15, 7},
		"next song twice as fast, a bit slower":  {70, 134, 0.2, 0.33, 14},
		"next song half as fast, a bit faster":   {138, 72, 0.3, 0.27, 7},
		"next song twice as fast, a bit quicker": {72, 148, 0.25, 0.1, 14},
	} {
		xa, beatsA := kicks(tc.bpmA, tc.firstA, 100, 1.2)
		xb, beatsB := kicks(tc.bpmB, tc.firstB, 100, 1.2)
		ra, err := analyze.Analyze(xa)
		if err != nil {
			t.Fatal(err)
		}
		rb, err := analyze.Analyze(xb)
		if err != nil {
			t.Fatal(err)
		}
		p := For(
			Song{Length: float64(len(xa)) / analyze.SampleRate, Edges: ra.Edges},
			Song{Length: float64(len(xb)) / analyze.SampleRate, Edges: rb.Edges},
		)
		if p.Kind != BeatMatch {
			t.Errorf("%s: plan = %+v (%s)", name, p, p.Why)
			continue
		}
		// B's kicks on A's clock, from where B starts to sound (a kick just before
		// that is the kick the start is on; it counts, as A's kick at AStart does)
		var onA []float64
		for _, tb := range beatsB {
			if tb >= p.BFrom-0.03 {
				onA = append(onA, p.AStart+(tb-p.BFrom)/p.BSpeed)
			}
		}
		lastA := beatsA[len(beatsA)-1] + 0.03
		var worst float64
		var n int
		if tc.bpmB > tc.bpmA { // B is the faster: each of A's kicks meets one of B's
			worst, n = worstMiss(beatsA, onA, p.AStart-0.03, lastA)
		} else { // B is the slower: each of B's kicks meets one of A's
			worst, n = worstMiss(onA, beatsA, p.AStart-0.03, lastA)
		}
		if n < tc.slowKicks {
			t.Errorf("%s: only %d of the slower track's kicks fall in the overlap, want at least %d", name, n, tc.slowKicks)
		}
		if worst > 0.025 {
			t.Errorf("%s: a kick misses by %.0f ms, want within 25 ms (speed %.4f, %s)", name, worst*1000, p.BSpeed, p.Why)
		}
		if math.Abs(p.BSpeed-1) > MaxTempoGap {
			t.Errorf("%s: speed %.3f is further than %.0f%% from 1", name, p.BSpeed, MaxTempoGap*100)
		}
		t.Logf("%s: %d kicks meet, worst miss %.1f ms (speed %.4f, %s)", name, n, worst*1000, p.BSpeed, p.Why)
	}
}
