package analyze

import (
	"math"
	"testing"
)

// A kick on the beat and, half a beat later, a bass note that swells slowly: the
// kick is where the anchor belongs, whatever else the song has between the beats.
func TestKickPhaseFindsTheKickNotTheBassBetween(t *testing.T) {
	const bpm, first = 150.0, 0.137
	period := 60 / bpm
	x := make([]float32, 30*SampleRate)
	for b := 0; first+float64(b)*period < 29; b++ {
		t0 := first + float64(b)*period
		for j := 0; j < int(0.2*SampleRate); j++ { // kick: sudden, 55 Hz
			tt := float64(j) / SampleRate
			x[int(t0*SampleRate)+j] += float32(0.7 * math.Exp(-tt/0.05) * math.Sin(2*math.Pi*55*tt))
		}
		t1 := t0 + period/2
		for j := 0; j < int(0.18*SampleRate); j++ { // bass between the kicks: swells over 60 ms, louder than the kick
			tt := float64(j) / SampleRate
			x[int(t1*SampleRate)+j] += float32(0.9 * math.Min(tt/0.06, 1) * math.Exp(-tt/0.12) * math.Sin(2*math.Pi*50*tt))
		}
	}
	phase, sharp, ok := kickPhase(x, period)
	if !ok || sharp < kickMinSharp {
		t.Fatalf("no kick found (sharp %.2f)", sharp)
	}
	d := math.Mod(phase-first+10*period, period)
	if d > period/2 {
		d -= period
	}
	if math.Abs(d) > 0.012 {
		t.Errorf("kick phase %.1f ms from the real kick, want within 12 ms", d*1000)
	}
}

func TestKickPhaseOfSilenceOrAShortClipIsNotFound(t *testing.T) {
	if _, _, ok := kickPhase(make([]float32, 10*SampleRate), 0.4); ok {
		t.Error("silence has no kick")
	}
	if _, _, ok := kickPhase(make([]float32, SampleRate), 0.4); ok {
		t.Error("a clip of 2 beats is too short")
	}
}
