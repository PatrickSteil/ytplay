package analyze

import (
	"math"
	"slices"
)

// waveBuckets is how many slices the loudness of a song is kept in: a terminal
// column of the waveform spans many of them even on a wide screen.
const waveBuckets = 600

// Wave is the loudness of a song over time, kept to be drawn: the level of
// each of Level's equal slices of the audio, 0 to 255.
type Wave struct {
	Offset  float64 // where in the song the first slice starts, in seconds
	Seconds float64 // how much of the song the slices cover
	// Level is the root-mean-square level of each slice, measured against the
	// 98th percentile of the song so that one spike does not flatten the rest
	// (anything above it is 255). Linear, not logarithmic: an intro, a
	// breakdown and a drop show up as what they are.
	Level []uint8
}

// waveOf is the loudness of pcm, which starts offset seconds into the song.
func waveOf(pcm []float32, offset float64) Wave {
	n := min(waveBuckets, len(pcm))
	if n == 0 {
		return Wave{}
	}
	rms := make([]float64, n)
	for i := range rms {
		lo, hi := i*len(pcm)/n, (i+1)*len(pcm)/n
		var sum float64
		for _, v := range pcm[lo:hi] {
			sum += float64(v) * float64(v)
		}
		rms[i] = math.Sqrt(sum / float64(hi-lo))
	}
	sorted := slices.Clone(rms)
	slices.Sort(sorted)
	scale := sorted[(n-1)*98/100]
	w := Wave{Offset: offset, Seconds: float64(len(pcm)) / SampleRate, Level: make([]uint8, n)}
	if scale == 0 {
		return w
	}
	for i, v := range rms {
		w.Level[i] = uint8(math.Round(255 * math.Min(v/scale, 1)))
	}
	return w
}
