package analyze

import (
	"math"
	"sort"
)

// Edges says what an automatic transition needs to know about the two ends of
// a song: where the music really starts and stops (files often begin and end
// with silence) and where the beats fall in the first and in the last stretch.
// All times are seconds into the song. The zero value means "not known".
type Edges struct {
	AudioStart, AudioEnd float64
	Start, End           BeatGrid
}

// BeatGrid is the beat grid fitted to one end of a song.
type BeatGrid struct {
	Period float64 // seconds per beat
	// Anchor is the beat a transition is built on: where the steady beat
	// starts for Edges.Start (the first beat after a beatless intro, if any),
	// where it ends for Edges.End.
	Anchor float64
	// Sure is true only if the grid can be relied on to line two songs up: see
	// the constants below. Sharp, Wobble and Margin are what it is judged on.
	Sure                  bool
	Sharp, Wobble, Margin float64
}

// audibleRange returns the time of the first and of the end of the last 20 ms
// frame that is louder than 1% of the loudest frame, or 0, 0 for silence.
func audibleRange(pcm []float32) (start, end float64) {
	const frame = SampleRate / 50
	n := len(pcm) / frame
	rms := make([]float64, n)
	var peak float64
	for i := range rms {
		var s float64
		for _, v := range pcm[i*frame : (i+1)*frame] {
			s += float64(v) * float64(v)
		}
		rms[i] = math.Sqrt(s / frame)
		peak = math.Max(peak, rms[i])
	}
	if peak == 0 {
		return 0, 0
	}
	first, last := -1, -1
	for i, v := range rms {
		if v > 0.01*peak {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	return float64(first*frame) / SampleRate, float64((last+1)*frame) / SampleRate
}

// ---- the beat grid of one stretch ----

const (
	gridWin  = 1024 // analysis window and step of the onset envelope used for beats:
	gridHop  = 128  // finer in time than the one used for the tempo
	gridBins = 128  // resolution of the beat profile
	// gridBias is how much earlier than the audio the onset envelope puts an
	// onset: a window sees a click before its centre. Measured on exact clicks
	// (TestGridPhaseBiasIsCalibrated).
	gridBias = 0.0408
	// The period is searched within this fraction of the song's tempo.
	gridSearch     = 0.03
	minGridSeconds = 8
	// A quarter of the stretch counts towards its wobble only if its beat
	// profile peaks at least this fraction as sharply as the whole stretch's.
	wobbleMinSharp = 0.4
)

// gridFit is a beat grid fitted to one stretch of audio.
type gridFit struct {
	period float64 // seconds per beat
	phase  float64 // seconds from the start of the stretch to a beat, in [0, period)
	// sharp is how much higher the beat profile peaks than its average, margin
	// how much higher than half a beat away, wobble how far the beat drifts
	// between the four quarters of the stretch (seconds).
	sharp, margin, wobble float64
}

// gridEnvelope is the onset envelope per band (30-200, 200-2000, 2000-8000 Hz,
// log-compressed spectral flux with its local mean removed, each scaled to unit
// variance), added with the low band counting double: that is where the kick
// is, and hats or guitars on the off-beat can be as strong as the beat.
func gridEnvelope(x []float32, pool *Pool) []float64 {
	flux := bandFlux(x, gridWin, gridHop, pool)
	if len(flux[0]) == 0 {
		return nil
	}
	env := make([]float64, len(flux[0]))
	for b, fl := range flux {
		prefix := make([]float64, len(fl)+1)
		for i, v := range fl {
			prefix[i+1] = prefix[i] + v
		}
		const half = 20 // about 240 ms either side
		var mean, sq float64
		for i := range fl {
			lo, hi := max(i-half, 0), min(i+half+1, len(fl))
			fl[i] = math.Max(fl[i]-(prefix[hi]-prefix[lo])/float64(hi-lo), 0)
			mean += fl[i]
		}
		mean /= float64(len(fl))
		for _, v := range fl {
			sq += (v - mean) * (v - mean)
		}
		std := math.Sqrt(sq/float64(len(fl))) + 1e-9
		weight := 1.0
		if b == 0 {
			weight = 2
		}
		for i, v := range fl {
			env[i] += weight * v / std
		}
	}
	return env
}

// envTime is the time an envelope index stands for, before correcting gridBias.
func envTime(i int) float64 { return float64(i+1) * gridHop / SampleRate }

// foldProfile adds env[from:to] up by the phase of a period of p seconds.
func foldProfile(env []float64, p float64, from, to int) []float64 {
	prof := make([]float64, gridBins)
	for i := from; i < to; i++ {
		prof[int(math.Mod(envTime(i), p)/p*gridBins)%gridBins] += env[i]
	}
	return prof
}

// profilePeak smooths a circular profile and returns where it peaks (in bins,
// interpolated), how high that is relative to the average, and relative to the
// opposite phase.
func profilePeak(prof []float64) (pos, sharp, margin float64) {
	sm := make([]float64, gridBins)
	var mean float64
	for i := range sm {
		sm[i] = (prof[(i+gridBins-1)%gridBins] + 2*prof[i] + prof[(i+1)%gridBins]) / 4
		mean += sm[i]
	}
	mean /= gridBins
	best := 0
	for i, v := range sm {
		if v > sm[best] {
			best = i
		}
	}
	a, b, c := sm[(best+gridBins-1)%gridBins], sm[best], sm[(best+1)%gridBins]
	d := 0.5 * (a - c) / (a - 2*b + c + 1e-12)
	pos = math.Mod(float64(best)+0.5+d+gridBins, gridBins)
	return pos, b / (mean + 1e-12), b / (sm[(best+gridBins/2)%gridBins] + 1e-12)
}

// beatOffsetIn is where the beat sits in env[from:to], in seconds, relative to
// the grid peak at bin centre, looking only a quarter beat either side so that
// a switch between the beat and the off-beat is not mistaken for drift.
func beatOffsetIn(env []float64, period float64, centre int, from, to int) float64 {
	prof := foldProfile(env, period, from, to)
	best, bestV := 0, math.Inf(-1)
	for d := -gridBins / 4; d <= gridBins/4; d++ {
		i := (centre + d + gridBins) % gridBins
		if v := prof[(i+gridBins-1)%gridBins] + 2*prof[i] + prof[(i+1)%gridBins]; v > bestV {
			best, bestV = d, v
		}
	}
	return float64(best) / gridBins * period
}

// gridOf fits a beat grid to x, a stretch of audio, for a song of about bpm.
// It returns the zero value if x is too short.
func gridOf(x []float32, bpm float64, pool *Pool) gridFit {
	if len(x) < minGridSeconds*SampleRate {
		return gridFit{}
	}
	env := gridEnvelope(x, pool)
	return fitGrid(env, bpm, 0, len(env))
}

// fitGrid fits a beat grid to env[from:to] for a song of about bpm. The phase
// is counted from the start of env.
func fitGrid(env []float64, bpm float64, from, to int) gridFit {
	if bpm <= 0 || float64(to-from)*gridHop < minGridSeconds*SampleRate {
		return gridFit{}
	}
	p0 := 60 / bpm
	var best gridFit
	var bestPos float64
	try := func(rel float64) {
		p := p0 * (1 + rel)
		pos, sharp, margin := profilePeak(foldProfile(env, p, from, to))
		if sharp > best.sharp {
			best, bestPos = gridFit{period: p, sharp: sharp, margin: margin}, pos
		}
	}
	for rel := -gridSearch; rel <= gridSearch; rel += 0.001 { // coarse
		try(rel)
	}
	centre := best.period/p0 - 1
	for rel := centre - 0.001; rel <= centre+0.001; rel += 0.00005 { // fine
		try(rel)
	}
	best.phase = math.Mod(bestPos/gridBins*best.period+gridBias+10*best.period, best.period)

	// A quarter without a beat of its own (a beatless intro, a break) says
	// nothing about drift, so only quarters that peak about as clearly as the
	// whole stretch count.
	var lo, hi float64
	q := (to - from) / 4
	for s := 0; s < 4; s++ {
		qa, qb := from+s*q, from+(s+1)*q
		if _, sharp, _ := profilePeak(foldProfile(env, best.period, qa, qb)); sharp < wobbleMinSharp*best.sharp {
			continue
		}
		d := beatOffsetIn(env, best.period, int(bestPos), qa, qb)
		lo, hi = math.Min(lo, d), math.Max(hi, d)
	}
	best.wobble = hi - lo
	return best
}

// steadyBeats finds where the beat of a grid fitted to env is there to stay:
// the times (counted from the start of env) of the first and of the last beat
// that belong to a run of at least steadyRun beats whose onsets are at least
// steadyStrength of the 75th percentile of all beats' onsets. ok is false if
// there is no such run.
func steadyBeats(env []float64, fit gridFit) (first, last float64, ok bool) {
	length := float64(len(env)) * gridHop / SampleRate
	var times, strength []float64
	for t := fit.phase; t < length; t += fit.period {
		c := int(math.Round((t-gridBias)*SampleRate/gridHop)) - 1 // the envelope sees an onset gridBias early
		var m float64
		for i := max(c-3, 0); i <= min(c+3, len(env)-1); i++ {
			m = math.Max(m, env[i])
		}
		times, strength = append(times, t), append(strength, m)
	}
	if len(times) < steadyRun {
		return 0, 0, false
	}
	sorted := append([]float64(nil), strength...)
	sort.Float64s(sorted)
	ref := sorted[len(sorted)*3/4]
	if ref <= 0 {
		return 0, 0, false
	}
	first, last = -1, -1
	run := 0
	for k, m := range strength {
		if m < steadyStrength*ref {
			run = 0
			continue
		}
		if run++; run >= steadyRun {
			if first < 0 {
				first = times[k-steadyRun+1]
			}
			last = times[k]
		}
	}
	return first, last, first >= 0
}

// ---- the edges of a song ----

const (
	// edgeSeconds is how much steady beat at each end the grid is fitted to:
	// long enough to be sure of the beat, short enough that a tempo that wanders
	// through the song does not matter. firstPassSeconds is how much of the end
	// is searched for where the steady beat starts or stops.
	edgeSeconds      = 40
	firstPassSeconds = 60
	// startSearchSeconds is how much of the start is searched for where the
	// steady beat begins: an EDM track may open with a minute or more of pad
	// before its beat comes in (64 bars at 128 BPM are 120 s). It must also hold
	// 2*minGridSeconds of steady beat, so a beat later than about 134 s is not found.
	startSearchSeconds = 150
	// The start is searched with windows of scanWindowSeconds, moved on by
	// scanHopSeconds: the first window with a sharp beat is where the beat is
	// (one fit over the whole search would be dulled by a long beatless intro).
	scanWindowSeconds = 30
	scanHopSeconds    = 10

	// The steady beat is a run of at least steadyRun beats whose onsets are at
	// least steadyStrength of the 75th percentile of the beats' onsets.
	steadyRun      = 8
	steadyStrength = 0.4

	// A grid is sure only if all of these hold. They are deliberately strict:
	// an unsure grid costs a plain crossfade, a wrong one a clash of beats.
	sureSharp    = 5.0   // the beat profile peaks at least this much over its average
	sureWobble   = 0.020 // the beat drifts at most this much (seconds) within the stretch
	sureMargin   = 1.5   // the beat is clearly stronger than the off-beat
	sureTempoGap = 0.005 // and its tempo is within this fraction of the song's
	// A song is only sure at one end if its tempo is the same at the other
	// end, within this fraction: a drummer whose tempo wanders, or a tempo
	// change, is not something to line another song up with.
	sureEndsGap = 0.01
)

// edgesOf finds where the music starts and stops, and the beat grid at both
// ends, for a song of about bpm.
func edgesOf(pcm []float32, bpm float64, pool *Pool) Edges {
	start, end := audibleRange(pcm)
	e := Edges{AudioStart: start, AudioEnd: end}
	if end-start < 2*minGridSeconds { // too little to judge steadiness by
		return e
	}
	e.Start = startGrid(pcm, bpm, start, end, pool)
	e.End, _ = edgeGrid(pcm, bpm, math.Max(end-firstPassSeconds, start), end, false, false, pool)
	e.Start.Sure, e.End.Sure = settleSure(e.Start, e.End)
	return e
}

// endsAgree reports whether the two ends of a song have the same tempo within
// sureEndsGap. If an end has no grid there is nothing to disagree with.
func endsAgree(a, b BeatGrid) bool {
	return a.Period == 0 || b.Period == 0 || math.Abs(a.Period/b.Period-1) <= sureEndsGap
}

// settleSure returns whether the start and the end are sure once their tempi
// are compared: if they disagree neither is.
func settleSure(start, end BeatGrid) (bool, bool) {
	if !endsAgree(start, end) {
		return false, false
	}
	return start.Sure, end.Sure
}

// startGrid finds the beat grid at the start of a song whose music starts at
// start and ends at end. It looks at the first firstPassSeconds of music; only
// if that gives no beat that could be sure (none found, or a beat profile
// flatter than sureSharp, which is what a pad does: it also gets a "steady run"
// out of noise) it looks at startSearchSeconds, scanning for where the beat has
// begun (a long intro, a build-up). The longer look costs more compute than the
// whole of the shorter one, so a song whose beat starts early does not pay for
// it, and gets the same grid as before.
func startGrid(pcm []float32, bpm, start, end float64, pool *Pool) BeatGrid {
	g, found := edgeGrid(pcm, bpm, start, math.Min(start+firstPassSeconds, end), true, false, pool)
	if (!found || g.Sharp < sureSharp) && start+firstPassSeconds < end {
		g, _ = edgeGrid(pcm, bpm, start, math.Min(start+startSearchSeconds, end), true, true, pool)
	}
	return g
}

// edgeGrid finds the beat grid at one end of a song: audio between from and to
// seconds, the start of the song if atStart. A first fit finds where the steady
// beat starts (or, at the end, stops); the grid is then fitted again to the
// edgeSeconds of steady beat next to that point, so that a beatless intro or
// outro does not dull it. With scan (at the start) the first fit only looks from
// the first window with a sharp beat on (firstSharpWindow), so that an intro of a
// minute or two does not dull it either. Its anchor is the first (or last) beat
// of the steady beat. found says whether a steady beat was located at all; if it
// was not, or the grid is not good enough, the result is not sure.
func edgeGrid(pcm []float32, bpm, from, to float64, atStart, scan bool, pool *Pool) (grid BeatGrid, found bool) {
	env := gridEnvelope(pcm[int(from*SampleRate):min(int(to*SampleRate), len(pcm))], pool)
	// A long beatless intro would dull the fit and the strength the beats are
	// judged by, so with scan both are taken from where the beat has begun.
	base := 0
	if scan {
		base = firstSharpWindow(env, bpm)
	}
	rest := env[base:]
	fit := fitGrid(rest, bpm, 0, len(rest))
	if fit.period == 0 {
		return BeatGrid{}, false
	}
	unsure := BeatGrid{Period: fit.period, Sharp: fit.sharp, Wobble: fit.wobble, Margin: fit.margin}
	first, last, ok := steadyBeats(rest, fit)
	offset := float64(base) * gridHop / SampleRate // times in rest count from here, those below from the start of env
	first, last = first+offset, last+offset
	if !ok || last-first < 2*minGridSeconds {
		return unsure, false
	}
	lo, hi, edge := first, math.Min(first+edgeSeconds, last), first
	if !atStart {
		lo, hi, edge = math.Max(last-edgeSeconds, first), last, last
	}
	fit = fitGrid(env, bpm, int(lo*SampleRate/gridHop), int(hi*SampleRate/gridHop))
	if fit.period == 0 {
		return unsure, false
	}
	phase := fit.phase
	if kp, sharp, ok := kickPhase(pcm[int((from+lo)*SampleRate):int((from+hi)*SampleRate)], fit.period); ok && sharp >= kickMinSharp {
		phase = lo + kp // the grid's own phase may sit on a clap or a bass note
	}
	k := math.Round((edge - phase) / fit.period)
	return BeatGrid{
		Period: fit.period, Anchor: from + phase + k*fit.period, Sure: sureGrid(fit, bpm),
		Sharp: fit.sharp, Wobble: fit.wobble, Margin: fit.margin,
	}, true
}

// firstSharpWindow returns the index in env where the first window of
// scanWindowSeconds with a sharp beat begins, or 0 if env is shorter than two
// windows or none is sharp.
func firstSharpWindow(env []float64, bpm float64) int {
	win := int(scanWindowSeconds * SampleRate / gridHop)
	hop := int(scanHopSeconds * SampleRate / gridHop)
	if len(env) < 2*win {
		return 0
	}
	for from := 0; from+win <= len(env); from += hop {
		if fitGrid(env, bpm, from, from+win).sharp >= sureSharp {
			return from
		}
	}
	return 0
}

// sureGrid says whether a grid fitted to a song of about bpm can be relied on.
func sureGrid(g gridFit, bpm float64) bool {
	return g.sharp >= sureSharp && g.wobble <= sureWobble && g.margin >= sureMargin &&
		math.Abs(60/g.period-bpm) <= sureTempoGap*bpm
}
