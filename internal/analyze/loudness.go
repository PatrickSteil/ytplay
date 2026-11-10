package analyze

import "math"

// Loudness is measured as in ITU-R BS.1770 (the "LUFS" of loudness
// normalisation): the audio is passed through the K-weighting filter (a
// high shelf for the head, a high pass for the low end the ear ignores), the
// mean power of 400 ms blocks (overlapping by 75%) is taken, blocks quieter
// than -70 LUFS and then those more than 10 LU below the rest are left out, and
// what is left is averaged. Silence around the music and a quiet intro do not
// count, which is what makes two songs comparable.
const (
	blockSeconds = 0.4
	blockHop     = 0.1
	absoluteGate = -70.0 // LUFS
	relativeGate = -10.0 // LU below the loudness of the blocks above the absolute gate
)

// biquad is a second-order filter section (direct form I).
type biquad struct{ b0, b1, b2, a1, a2 float64 }

func (f biquad) apply(x []float64) {
	var x1, x2, y1, y2 float64
	for i, v := range x {
		y := f.b0*v + f.b1*x1 + f.b2*x2 - f.a1*y1 - f.a2*y2
		x2, x1, y2, y1 = x1, v, y1, y
		x[i] = y
	}
}

// kWeighting is the two filters of BS.1770 for sampling rate fs. The standard
// gives coefficients for 48 kHz only; these are the same filters worked out
// for fs from their design values.
func kWeighting(fs float64) (shelf, highpass biquad) {
	const (
		shelfF, shelfGain, shelfQ = 1681.974450955533, 3.999843853973347, 0.7071752369554196
		hpF, hpQ                  = 38.13547087602444, 0.5003270373238773
	)
	a := math.Pow(10, shelfGain/40)
	w0 := 2 * math.Pi * shelfF / fs
	alpha := math.Sin(w0) / (2 * shelfQ)
	cos, sq := math.Cos(w0), 2*math.Sqrt(a)*alpha
	a0 := (a + 1) - (a-1)*cos + sq
	shelf = biquad{
		b0: a * ((a + 1) + (a-1)*cos + sq) / a0,
		b1: -2 * a * ((a - 1) + (a+1)*cos) / a0,
		b2: a * ((a + 1) + (a-1)*cos - sq) / a0,
		a1: 2 * ((a - 1) - (a+1)*cos) / a0,
		a2: ((a + 1) - (a-1)*cos - sq) / a0,
	}
	k := math.Tan(math.Pi * hpF / fs)
	d := 1 + k/hpQ + k*k
	highpass = biquad{b0: 1, b1: -2, b2: 1, a1: 2 * (k*k - 1) / d, a2: (1 - k/hpQ + k*k) / d}
	return shelf, highpass
}

// loudness is the integrated loudness of mono pcm in LUFS. ok is false if the
// audio is shorter than one block or has nothing above the absolute gate.
func loudness(pcm []float32) (lufs float64, ok bool) {
	block, hop := int(blockSeconds*SampleRate), int(blockHop*SampleRate)
	if len(pcm) < block {
		return 0, false
	}
	x := make([]float64, len(pcm))
	for i, v := range pcm {
		x[i] = float64(v)
	}
	shelf, hp := kWeighting(SampleRate)
	shelf.apply(x)
	hp.apply(x)
	// mean power of each block, from a running sum of squares
	sq := make([]float64, len(x)+1)
	for i, v := range x {
		sq[i+1] = sq[i] + v*v
	}
	var power []float64
	for from := 0; from+block <= len(x); from += hop {
		power = append(power, (sq[from+block]-sq[from])/float64(block))
	}
	lu := func(p float64) float64 { return -0.691 + 10*math.Log10(p) }
	mean := func(gate float64) (m float64, n int) {
		for _, p := range power {
			if p > 0 && lu(p) > gate {
				m, n = m+p, n+1
			}
		}
		if n > 0 {
			m /= float64(n)
		}
		return m, n
	}
	m, n := mean(absoluteGate)
	if n == 0 {
		return 0, false
	}
	m, n = mean(lu(m) + relativeGate)
	if n == 0 {
		return 0, false
	}
	return lu(m), true
}

// peakOf is the largest absolute sample.
func peakOf(pcm []float32) float64 {
	var peak float64
	for _, v := range pcm {
		peak = math.Max(peak, math.Abs(float64(v)))
	}
	return peak
}
