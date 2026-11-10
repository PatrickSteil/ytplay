package analyze

import "math"

// The beat grid is fitted to an envelope of all frequencies (the low end
// counting double), which is what finds the tempo reliably. Its phase, though,
// is wherever that envelope peaks, and in a track with hats, claps and a
// rolling bass between the kicks that can be 50 to 150 ms from the kick: two
// songs lined up by such grids have their kicks half a beat apart. The anchor
// of a grid is therefore moved to the kick itself, found as the sharpest rise
// of the low end (below about 140 Hz) at the grid's tempo.
const (
	kickBins = 400 // bins per beat the kick's onsets are folded into: 1 ms at 150 BPM
	// kickMinSharp is how much the onsets of the kick must peak over their average
	// for the kick to be trusted over the grid's own phase.
	kickMinSharp = 2.0
)

// kickOnsets is the positive rise of the (rectified, low passed) low end of x,
// per sample: sharp where a kick starts, spread out where a bass swells.
func kickOnsets(x []float32) []float64 {
	const lowpass, smooth = 0.04, 0.08 // one-pole coefficients: about 140 Hz, and 0.6 ms
	var y, e, prev float64
	out := make([]float64, len(x))
	for i, v := range x {
		y += lowpass * (float64(v) - y)
		e += smooth * (math.Abs(y) - e)
		out[i] = math.Max(e-prev, 0)
		prev = e
	}
	return out
}

// kickPhase finds where in each period of the given length (seconds) the kicks
// of x start, counted from the start of x, and how clearly: the peak of the
// folded onsets over their average. ok is false if x has no such peak.
func kickPhase(x []float32, period float64) (phase, sharp float64, ok bool) {
	if len(x) < int(8*period*SampleRate) || period <= 0 {
		return 0, 0, false
	}
	on := kickOnsets(x)
	prof := make([]float64, kickBins)
	for i, v := range on {
		prof[int(math.Mod(float64(i)/SampleRate, period)/period*kickBins)%kickBins] += v
	}
	// a few bins of smoothing (about 3 ms) so that one noisy bin is not the peak
	sm := make([]float64, kickBins)
	var mean float64
	best := 0
	for i := range sm {
		for d := -2; d <= 2; d++ {
			sm[i] += prof[(i+d+kickBins)%kickBins]
		}
		mean += sm[i]
		if sm[i] > sm[best] {
			best = i
		}
	}
	mean /= kickBins
	if mean <= 0 {
		return 0, 0, false
	}
	// interpolate the peak between its neighbours
	l, c, r := sm[(best+kickBins-1)%kickBins], sm[best], sm[(best+1)%kickBins]
	shift := 0.0
	if d := l - 2*c + r; d < 0 {
		shift = 0.5 * (l - r) / d
	}
	return (float64(best) + shift + 0.5) / kickBins * period, sm[best] / mean, true
}
