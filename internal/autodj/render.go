package autodj

import (
	"encoding/binary"
	"math"
	"os"

	"github.com/PatrickSteil/ytplay/internal/analyze"
)

// Stretch plays x `speed` times as fast without changing its pitch (the result
// is 1/speed as long). Time-stretching is injected so that tests can use a
// plain resampler and previews a proper one.
type Stretch func(x []float32, speed float64) []float32

// Render mixes a transition as a listener would hear it, from lead seconds
// before the next song starts until tail seconds after the overlap ends. Both
// songs are mono at analyze.SampleRate. The volumes cross with an equal-power
// curve over the overlap; the playing song plays at its ASpeed from where the
// overlap starts, the next song at BSpeed (and GainB) throughout (the easing
// back to normal speed is not rendered).
func Render(a, b []float32, p Plan, lead, tail float64, stretch Stretch) []float32 {
	rate := float64(analyze.SampleRate)
	t0 := p.AStart - lead
	aSpeed := p.PlayingSpeed()
	overlap := p.Overlap / aSpeed // how long the overlap lasts as it is heard
	out := make([]float32, int((lead+overlap+tail)*rate))
	gainB := math.Pow(10, p.GainB/20)

	var playing []float32 // the playing song from where the overlap starts, at its speed
	if from := int(p.AStart * rate); from >= 0 && from < len(a) {
		playing = a[from:]
		if aSpeed != 1 {
			playing = stretch(playing, aSpeed)
		}
	}
	var incoming []float32 // the next song from where it starts, at its speed
	if from := int(p.BFrom * rate); from < len(b) {
		incoming = b[from:]
		if p.BSpeed != 1 {
			incoming = stretch(incoming, p.BSpeed)
		}
	}
	for i := range out {
		t := t0 + float64(i)/rate
		ga, gb := 1.0, 0.0 // gains of the playing and of the next song
		if t >= p.AStart {
			u := 1.0
			if overlap > 0 {
				u = math.Min((t-p.AStart)/overlap, 1)
			}
			ga, gb = math.Cos(math.Pi/2*u), math.Sin(math.Pi/2*u)*gainB
		}
		var v float64
		if ga > 1e-9 {
			if t < p.AStart {
				if ia := int(math.Round(t * rate)); ia >= 0 && ia < len(a) {
					v += ga * float64(a[ia])
				}
			} else if ia := int(math.Round((t - p.AStart) * rate)); ia < len(playing) {
				v += ga * float64(playing[ia])
			}
		}
		if ib := int((t - p.AStart) * rate); gb > 1e-9 && t >= p.AStart && ib < len(incoming) {
			v += gb * float64(incoming[ib])
		}
		out[i] = float32(v)
	}
	return out
}

// WriteWAV writes mono 16-bit audio at analyze.SampleRate to path.
func WriteWAV(path string, x []float32) error {
	pcm := make([]byte, 2*len(x))
	for i, v := range x {
		s := int16(math.Max(-1, math.Min(1, float64(v))) * 32767)
		binary.LittleEndian.PutUint16(pcm[2*i:], uint16(s))
	}
	h := make([]byte, 44)
	copy(h, "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+len(pcm)))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1) // PCM
	binary.LittleEndian.PutUint16(h[22:], 1) // mono
	binary.LittleEndian.PutUint32(h[24:], analyze.SampleRate)
	binary.LittleEndian.PutUint32(h[28:], analyze.SampleRate*2)
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(len(pcm)))
	return os.WriteFile(path, append(h, pcm...), 0o644)
}
