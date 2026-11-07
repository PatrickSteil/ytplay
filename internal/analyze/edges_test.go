package analyze

import (
	"math"
	"testing"
	"time"
)

// tone is a sine of the given amplitude and length.
func tone(seconds, amp, freq float64) []float32 {
	x := make([]float32, int(seconds*SampleRate))
	for i := range x {
		x[i] = float32(amp * math.Sin(2*math.Pi*freq*float64(i)/SampleRate))
	}
	return x
}

func concat(parts ...[]float32) []float32 {
	var out []float32
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func silence(seconds float64) []float32 { return make([]float32, int(seconds*SampleRate)) }

func TestAudibleRangeTrimsSilence(t *testing.T) {
	x := concat(silence(2), tone(10, 0.5, 440), silence(3))
	start, end := audibleRange(x)
	if math.Abs(start-2) > 0.03 || math.Abs(end-12) > 0.03 {
		t.Errorf("audible range = %.3f..%.3f, want about 2..12", start, end)
	}
}

func TestAudibleRangeOfAllSilence(t *testing.T) {
	if start, end := audibleRange(silence(5)); start != 0 || end != 0 {
		t.Errorf("audible range of silence = %v..%v, want 0..0", start, end)
	}
	if start, end := audibleRange(nil); start != 0 || end != 0 {
		t.Errorf("audible range of nothing = %v..%v, want 0..0", start, end)
	}
}

func TestAudibleRangeIgnoresATinyClickInTheSilence(t *testing.T) {
	x := concat(silence(2), tone(10, 0.5, 440), silence(3))
	for i := 0; i < 50; i++ {
		x[int(0.5*SampleRate)+i] = 0.002 // far under 1% of the music's loudest moment
	}
	if start, _ := audibleRange(x); math.Abs(start-2) > 0.03 {
		t.Errorf("start = %.3f, a tiny click moved it from 2", start)
	}
}

// ---- synthetic drum tracks with known beat times ----

type drumOpts struct {
	offbeatHats  float64 // amplitude of a hat on every off-beat, relative to the kick (0: none)
	hatsFrom     float64 // the hats only start this many seconds into the track
	intro        float64 // seconds before the first kick that has only a pad
	outro        float64 // seconds at the end that have only the pad again
	tempoRamp    float64 // the period grows by this fraction over the track (0.03: 3% slower at the end)
	silenceStart float64 // seconds of silence before everything
	silenceEnd   float64 // and after
}

// lcg is a tiny deterministic noise source in [-1, 1).
type lcg uint32

func (s *lcg) next() float64 {
	*s = *s*1664525 + 1013904223
	return float64(int32(*s)) / float64(1<<31)
}

// drumBeats are the times (seconds into the returned track) of the kicks of a
// track made by drumTrack: one per beat from firstBeat on, seconds long.
func drumBeats(bpm, firstBeat, seconds float64, o drumOpts) []float64 {
	var beats []float64
	p0 := 60 / bpm
	for t := firstBeat; t < seconds; {
		if t >= o.intro && t < seconds-o.outro {
			beats = append(beats, o.silenceStart+t)
		}
		t += p0 * (1 + o.tempoRamp*t/seconds)
	}
	return beats
}

// drumTrack is a kick on every beat (a 55 Hz thump with a sharp click), optional
// hats on the off-beats, and a quiet pad under all of it. The kicks before
// `intro` seconds are left out.
func drumTrack(bpm, firstBeat, seconds float64, o drumOpts) []float32 {
	sr := float64(SampleRate)
	n := int(seconds * sr)
	x := make([]float32, n)
	for i := range x {
		t := float64(i) / SampleRate
		x[i] = float32(0.06 * (math.Sin(2*math.Pi*220*t) + math.Sin(2*math.Pi*277*t) + math.Sin(2*math.Pi*330*t)))
	}
	noise := lcg(7)
	beats := drumBeats(bpm, firstBeat, seconds, o)
	for _, bt := range beats {
		t := bt - o.silenceStart
		if t < o.intro {
			continue
		}
		i0 := int(t * SampleRate)
		for j := 0; j < int(0.25*sr) && i0+j < n; j++ {
			tt := float64(j) / SampleRate
			v := 0.8 * math.Exp(-tt/0.06) * math.Sin(2*math.Pi*55*tt)
			if j < 110 { // 5 ms click
				v += 0.3 * noise.next() * (1 - float64(j)/110)
			}
			x[i0+j] += float32(v)
		}
		if o.offbeatHats > 0 && t >= o.hatsFrom {
			h0 := int((t + 60/bpm/2) * SampleRate)
			prev := 0.0
			for j := 0; j < int(0.05*sr) && h0+j < n && h0+j >= 0; j++ {
				cur := noise.next()
				v := (cur - prev) * 0.5 * o.offbeatHats * 0.8 * math.Exp(-float64(j)/(0.012*sr))
				prev = cur
				x[h0+j] += float32(v)
			}
		}
	}
	return concat(silence(o.silenceStart), x, silence(o.silenceEnd))
}

// beatOffset is how far the grid's beat is from the nearest true beat, seconds.
func beatOffset(grid, truth, period float64) float64 {
	d := math.Mod(grid-truth, period)
	if d > period/2 {
		d -= period
	} else if d < -period/2 {
		d += period
	}
	return d
}

func TestGridOfADrumTrack(t *testing.T) {
	x := drumTrack(124, 0.337, 40, drumOpts{})
	g := gridOf(x, 124, nil)
	if want := 60.0 / 124; math.Abs(g.period-want) > 0.001*want {
		t.Errorf("period = %.5f, want %.5f within 0.1%%", g.period, want)
	}
	if d := beatOffset(g.phase, 0.337, g.period); math.Abs(d) > 0.008 {
		t.Errorf("beat phase is %.1f ms from the true beat, want within 8 ms", d*1000)
	}
	if g.sharp < 5 {
		t.Errorf("sharp = %.1f for a clean drum track, want at least 5", g.sharp)
	}
}

func TestGridPhaseBiasIsCalibrated(t *testing.T) {
	// clickTrack puts a click at exactly k*period from time 0.
	g := gridOf(clickTrack(120, 40), 120, nil)
	if d := beatOffset(g.phase, 0, g.period); math.Abs(d) > 0.005 {
		t.Errorf("beat phase is %.1f ms from the exact clicks, want within 5 ms", d*1000)
	}
}

func TestGridIgnoresOffBeatHats(t *testing.T) {
	x := drumTrack(124, 0.2, 40, drumOpts{offbeatHats: 5})
	g := gridOf(x, 124, nil)
	if d := beatOffset(g.phase, 0.2, g.period); math.Abs(d) > 0.008 {
		t.Errorf("beat phase is %.1f ms from the kick, want within 8 ms (half a beat would be %.0f ms)", d*1000, g.period*500)
	}
	if g.margin < 1.5 {
		t.Errorf("margin = %.2f, want at least 1.5 (the beat clearly beats the off-beat)", g.margin)
	}
}

func TestGridOfNoiseIsNotSharp(t *testing.T) {
	noise := lcg(3)
	x := make([]float32, 40*SampleRate)
	for i := range x {
		x[i] = float32(0.3 * noise.next())
	}
	if g := gridOf(x, 124, nil); g.sharp >= 3 {
		t.Errorf("sharp = %.1f for noise, want under 3", g.sharp)
	}
}

func TestGridWobbleGrowsWhenTheTempoRamps(t *testing.T) {
	steady := gridOf(drumTrack(124, 0.3, 40, drumOpts{}), 124, nil)
	ramp := gridOf(drumTrack(124, 0.3, 40, drumOpts{tempoRamp: 0.03}), 124, nil)
	if steady.wobble > 0.010 {
		t.Errorf("steady track wobbles %.1f ms, want under 10", steady.wobble*1000)
	}
	if ramp.wobble < 0.020 {
		t.Errorf("a 3%% tempo ramp wobbles only %.1f ms, want over 20", ramp.wobble*1000)
	}
}

func TestGridWithTempoOutsideTheSearchRange(t *testing.T) {
	if g := gridOf(drumTrack(124, 0.3, 40, drumOpts{}), 135, nil); g.sharp >= 5 {
		t.Errorf("sharp = %.1f for a hint 9%% off, want under 5", g.sharp)
	}
}

func TestGridOfTooLittleAudio(t *testing.T) {
	if g := gridOf(drumTrack(124, 0.3, 3, drumOpts{}), 124, nil); g.period != 0 {
		t.Errorf("3 s of audio gave a grid %+v, want none", g)
	}
}

func TestWobbleIgnoresASwitchToTheOffBeat(t *testing.T) {
	// Loud hats on the off-beat only in the second half: there the strongest
	// onset moves half a beat. That is not drift of the beat.
	x := drumTrack(124, 0.2, 40, drumOpts{offbeatHats: 3, hatsFrom: 20})
	g := gridOf(x, 124, nil)
	if d := beatOffset(g.phase, 0.2, g.period); math.Abs(d) > 0.008 {
		t.Errorf("beat phase is %.1f ms from the kick, want within 8 ms", d*1000)
	}
	if g.wobble > 0.020 {
		t.Errorf("wobble = %.0f ms; a switch to the off-beat must not count as drift (want under 20 ms)", g.wobble*1000)
	}
}

func TestBeatOffsetLooksOnlyAQuarterBeatAway(t *testing.T) {
	// A weak peak on the beat and a strong one on the off-beat: the beat is the
	// one within a quarter beat of where the whole song put it.
	p := 0.5
	env := make([]float64, 3600)
	for k := 0; k < 40; k++ {
		kick := 0.1 + float64(k)*p
		env[int(math.Round(kick*SampleRate/gridHop))-1] = 1
		env[int(math.Round((kick+p/2)*SampleRate/gridHop))-1] = 3
	}
	centre := int(0.1 / p * float64(gridBins))
	if d := beatOffsetIn(env, p, centre, 0, len(env)); math.Abs(d) > 0.02 {
		t.Errorf("offset = %.0f ms, want about 0 (the off-beat peak is half a beat = 250 ms away)", d*1000)
	}
}

// ---- edges of a song ----

func lastBeatAtOrBefore(beats []float64, t float64) float64 {
	last := beats[0]
	for _, b := range beats {
		if b <= t+1e-9 {
			last = b
		}
	}
	return last
}

func TestEdgesOfADrumTrack(t *testing.T) {
	o := drumOpts{silenceStart: 1.5, silenceEnd: 1.5}
	x := drumTrack(124, 0.337, 120, o)
	e := edgesOf(x, 124, nil)
	if math.Abs(e.AudioStart-1.5) > 0.05 || math.Abs(e.AudioEnd-121.5) > 0.05 {
		t.Errorf("audible %.2f..%.2f, want about 1.5..121.5", e.AudioStart, e.AudioEnd)
	}
	if !e.Start.Sure || !e.End.Sure {
		t.Fatalf("a clean drum track must be sure at both ends: start %+v end %+v", e.Start, e.End)
	}
	beats := drumBeats(124, 0.337, 120, o)
	if want := beats[0]; math.Abs(e.Start.Anchor-want) > 0.010 {
		t.Errorf("start anchor %.3f, want the first beat %.3f within 10 ms", e.Start.Anchor, want)
	}
	if want := lastBeatAtOrBefore(beats, e.AudioEnd); math.Abs(e.End.Anchor-want) > 0.010 {
		t.Errorf("end anchor %.3f, want the last beat %.3f within 10 ms", e.End.Anchor, want)
	}
	if e.Start.Anchor < e.AudioStart || e.End.Anchor > e.AudioEnd {
		t.Errorf("anchors %.3f and %.3f must lie inside the audible %.3f..%.3f", e.Start.Anchor, e.End.Anchor, e.AudioStart, e.AudioEnd)
	}
	if want := 60.0 / 124; math.Abs(e.Start.Period-want) > 0.001*want || math.Abs(e.End.Period-want) > 0.001*want {
		t.Errorf("periods %.5f and %.5f, want %.5f within 0.1%%", e.Start.Period, e.End.Period, want)
	}
}

func TestEdgesWithABeatlessIntro(t *testing.T) {
	o := drumOpts{intro: 15}
	x := drumTrack(124, 0.337, 80, o)
	e := edgesOf(x, 124, nil)
	first := drumBeats(124, 0.337, 80, o)[0] // the first kick: where the beat starts
	if math.Abs(e.Start.Anchor-first) > 0.010 {
		t.Errorf("start anchor %.3f, want where the beat starts, %.3f, within 10 ms", e.Start.Anchor, first)
	}
	if !e.Start.Sure {
		t.Errorf("25 s of beats after the intro should be sure: %+v", e.Start)
	}
}

func TestEdgesWithABeatlessOutro(t *testing.T) {
	o := drumOpts{outro: 20}
	x := drumTrack(124, 0.337, 80, o)
	e := edgesOf(x, 124, nil)
	beats := drumBeats(124, 0.337, 80, o)
	if want := beats[len(beats)-1]; math.Abs(e.End.Anchor-want) > 0.010 {
		t.Errorf("end anchor %.3f, want where the beat ends, %.3f, within 10 ms", e.End.Anchor, want)
	}
	if !e.End.Sure {
		t.Errorf("40 s of beats before the outro should be sure: %+v", e.End)
	}
}

func TestSteadyBeatsNeedARun(t *testing.T) {
	// Beats every 0.5 s: loud from 10 s to 30 s, silent elsewhere, except one
	// lone loud beat at 3 s that is no steady beat.
	fit := gridFit{period: 0.5, phase: 0.1}
	env := make([]float64, 60*SampleRate/gridHop)
	for k := 0; k < 120; k++ {
		at := fit.phase + float64(k)*fit.period - gridBias // where the envelope shows the onset
		i := int(at*SampleRate/gridHop) - 1
		if k >= 20 && k < 60 || k == 6 {
			env[i] = 5
		} else {
			env[i] = 0.1
		}
	}
	first, last, ok := steadyBeats(env, fit)
	if !ok {
		t.Fatal("no steady beat found")
	}
	if math.Abs(first-(0.1+20*0.5)) > 0.001 || math.Abs(last-(0.1+59*0.5)) > 0.001 {
		t.Errorf("steady from %.2f to %.2f, want 10.10 to 29.60", first, last)
	}
	if _, _, ok := steadyBeats(make([]float64, len(env)), fit); ok {
		t.Error("a silent envelope has no steady beat")
	}
}

func TestUnsteadyTempoIsNotSure(t *testing.T) {
	x := drumTrack(124, 0.3, 120, drumOpts{tempoRamp: 0.09}) // 3% slower within the first 40 s
	e := edgesOf(x, 124, nil)
	if e.Start.Sure || e.End.Sure {
		t.Errorf("a tempo that keeps changing must not be sure: start %+v end %+v", e.Start, e.End)
	}
}

func TestWrongTempoHintIsNotSure(t *testing.T) {
	e := edgesOf(drumTrack(124, 0.3, 120, drumOpts{}), 130, nil)
	if e.Start.Sure || e.End.Sure {
		t.Errorf("a grid for the wrong tempo must not be sure: start %+v end %+v", e.Start, e.End)
	}
}

func TestEdgesOfShortAudioHaveNoGrids(t *testing.T) {
	e := edgesOf(drumTrack(124, 0.3, 12, drumOpts{}), 124, nil)
	if e.Start != (BeatGrid{}) || e.End != (BeatGrid{}) {
		t.Errorf("12 s is too short for grids: %+v %+v", e.Start, e.End)
	}
	if e.AudioEnd < 11 {
		t.Errorf("the audible range is still known: %+v", e)
	}
}

func TestNoBeatHasNoGridsButStillKnowsWhereTheMusicIs(t *testing.T) {
	noise := lcg(5)
	body := make([]float32, 60*SampleRate)
	for i := range body {
		body[i] = float32(0.3 * noise.next())
	}
	x := concat(silence(2), body, silence(3))
	res, err := Analyze(x)
	if err != nil {
		t.Fatal(err)
	}
	if res.Edges.Start != (BeatGrid{}) || res.Edges.End != (BeatGrid{}) {
		t.Errorf("noise has no steady beat, so no grids: %+v", res.Edges)
	}
	if math.Abs(res.Edges.AudioStart-2) > 0.05 || math.Abs(res.Edges.AudioEnd-62) > 0.05 {
		t.Errorf("but the audible range is still found: %.2f..%.2f, want about 2..62", res.Edges.AudioStart, res.Edges.AudioEnd)
	}
}

func TestAnalyzeFillsTheEdgesOfADrumTrack(t *testing.T) {
	res, err := Analyze(drumTrack(124, 0.337, 90, drumOpts{silenceStart: 1, silenceEnd: 1}))
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(res.BPM-124) > 2 {
		t.Fatalf("BPM = %.1f, want about 124", res.BPM)
	}
	if !res.Edges.Start.Sure || !res.Edges.End.Sure || res.Edges.AudioStart < 0.9 {
		t.Errorf("edges = %+v", res.Edges)
	}
}

func TestEdgesAddLittleTime(t *testing.T) {
	x := drumTrack(124, 0.3, 240, drumOpts{})
	start := time.Now()
	edgesOf(x, 124, nil)
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("edges of a 4 minute song took %v, want under 2 s", took)
	}
}

func TestSureGridNeedsEveryCriterion(t *testing.T) {
	const bpm = 124.0
	good := gridFit{period: 60 / bpm, sharp: 8, wobble: 0.010, margin: 2}
	if !sureGrid(good, bpm) {
		t.Fatalf("a sharp, steady, clear grid at the right tempo must be sure: %+v", good)
	}
	for name, mutate := range map[string]func(g *gridFit){
		"not sharp enough":   func(g *gridFit) { g.sharp = 4.9 },
		"wobbles too much":   func(g *gridFit) { g.wobble = 0.021 },
		"beat vs off-beat":   func(g *gridFit) { g.margin = 1.49 },
		"tempo is off by 1%": func(g *gridFit) { g.period = 60 / (bpm * 1.01) },
	} {
		g := good
		mutate(&g)
		if sureGrid(g, bpm) {
			t.Errorf("%s: %+v must not be sure", name, g)
		}
	}
	for name, edge := range map[string]gridFit{
		"sharp at its limit":  {period: 60 / bpm, sharp: 5, wobble: 0.010, margin: 2},
		"wobble at its limit": {period: 60 / bpm, sharp: 8, wobble: 0.020, margin: 2},
		"margin at its limit": {period: 60 / bpm, sharp: 8, wobble: 0.010, margin: 1.5},
		"tempo at its limit":  {period: 60 / (bpm * 1.005), sharp: 8, wobble: 0.010, margin: 2},
	} {
		if !sureGrid(edge, bpm) {
			t.Errorf("%s: %+v should still be sure", name, edge)
		}
	}
}

func TestEndsMustAgreeOnTheTempo(t *testing.T) {
	grid := func(bpm float64) BeatGrid { return BeatGrid{Period: 60 / bpm} }
	for name, tc := range map[string]struct {
		a, b BeatGrid
		want bool
	}{
		"same tempo":             {grid(124), grid(124), true},
		"half a percent apart":   {grid(124), grid(124.6), true},
		"one percent apart":      {grid(124), grid(125.2), true},
		"2% apart":               {grid(115.5), grid(117.8), false},
		"3% apart (live drums)":  {grid(115.5), grid(118.8), false},
		"one end has no grid":    {grid(124), BeatGrid{}, true},
		"neither end has a grid": {BeatGrid{}, BeatGrid{}, true},
	} {
		if got := endsAgree(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: endsAgree = %v, want %v", name, got, tc.want)
		}
	}
}

func TestSureNeedsBothEndsToAgree(t *testing.T) {
	sure := BeatGrid{Period: 60 / 124.0, Sure: true}
	other := BeatGrid{Period: 60 / 115.5, Sure: true}
	e := Edges{Start: sure, End: other}
	e.Start.Sure, e.End.Sure = settleSure(e.Start, e.End)
	if e.Start.Sure || e.End.Sure {
		t.Errorf("ends at 124 and 115.5 BPM must both be unsure: %+v", e)
	}
	s, n := settleSure(sure, sure)
	if !s || !n {
		t.Error("equal ends must stay sure")
	}
	s, n = settleSure(sure, BeatGrid{Period: 60 / 124.0})
	if !s || n {
		t.Errorf("an end that was not sure stays unsure and does not take the other down: got %v, %v", s, n)
	}
}

// ---- a beat that starts late in the song ----

// EDM often opens with a pad and brings the beat in after half a minute or a
// minute. The song is entered where the beat starts, so that must be found,
// however long the intro, as long as it ends within startSearchSeconds.
func TestBeatStartIsFoundAfterALongIntro(t *testing.T) {
	const bpm = 128
	period := 60.0 / bpm
	for _, intro := range []float64{16, 30, 45, 60, 70, 100, 120} {
		o := drumOpts{intro: intro}
		total := intro + 120
		e := edgesOf(drumTrack(bpm, 0.337, total, o), bpm, nil)
		first := drumBeats(bpm, 0.337, total, o)[0] // the first kick: where the beat starts
		if !e.Start.Sure {
			t.Errorf("intro %.0f s: the start must be sure: %+v", intro, e.Start)
			continue
		}
		if off := beatOffset(e.Start.Anchor, first, period); math.Abs(off) > 0.025 {
			t.Errorf("intro %.0f s: start anchor %.3f is %.0f ms off the beat grid (first kick %.3f)", intro, e.Start.Anchor, off*1000, first)
		}
		if d := e.Start.Anchor - first; d < -0.025 || d > period+0.025 {
			t.Errorf("intro %.0f s: start anchor %.3f, want the first kick %.3f or one beat later", intro, e.Start.Anchor, first)
		}
	}
}

// A beat that comes later than the search reaches is not found: the start is
// not sure (the song is then crossfaded into), it is never a made-up beat.
func TestNoBeatStartIsClaimedWhenTheBeatComesAfter150Seconds(t *testing.T) {
	o := drumOpts{intro: 160}
	e := edgesOf(drumTrack(128, 0.337, 240, o), 128, nil)
	if e.Start.Sure {
		t.Errorf("a beat that starts at 160 s must not be claimed: %+v", e.Start)
	}
}

// Irregular noise bursts are not a beat, however many there are.
func TestNoBeatStartInIrregularNoiseBursts(t *testing.T) {
	x := drumTrack(128, 0.337, 200, drumOpts{intro: 1000}) // the pad alone
	noise := lcg(99)
	sr := float64(SampleRate)
	for at := 2.0; at < 195; at += 0.3 + 1.2*(noise.next()+1)/2 {
		i0 := int(at * sr)
		for j := 0; j < int(0.05*sr) && i0+j < len(x); j++ {
			x[i0+j] += float32(0.5 * noise.next() * math.Exp(-float64(j)/(0.01*sr)))
		}
	}
	if e := edgesOf(x, 128, nil); e.Start.Sure {
		t.Errorf("noise bursts must not give a sure beat start: %+v", e.Start)
	}
}
