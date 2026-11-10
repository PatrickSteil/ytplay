package analyze

import (
	"math"
	"testing"
)

func sine(freq, amp, seconds float64) []float32 {
	x := make([]float32, int(seconds*SampleRate))
	for i := range x {
		x[i] = float32(amp * math.Sin(2*math.Pi*freq*float64(i)/SampleRate))
	}
	return x
}

func TestLoudnessOfAFullScaleSineIsAboutMinus3(t *testing.T) {
	// ITU-R BS.1770: a 1 kHz sine at amplitude A reads 20*log10(A) - 3.01 LUFS.
	lufs, ok := loudness(sine(1000, 1, 10))
	if !ok || math.Abs(lufs-(-3.01)) > 0.3 {
		t.Errorf("loudness = %.2f (ok %v), want about -3.01", lufs, ok)
	}
}

func TestLoudnessRisesBySixPerDoubling(t *testing.T) {
	quiet, _ := loudness(sine(440, 0.1, 10))
	loud, _ := loudness(sine(440, 0.2, 10))
	if d := loud - quiet; math.Abs(d-6.02) > 0.05 {
		t.Errorf("doubling the amplitude changed the loudness by %.2f LU, want 6.02", d)
	}
}

func TestLoudnessIgnoresSilenceAndQuietPassages(t *testing.T) {
	music := sine(440, 0.3, 10)
	plain, _ := loudness(music)
	padded := append(append(make([]float32, 8*SampleRate), music...), make([]float32, 8*SampleRate)...)
	withSilence, _ := loudness(padded)
	if math.Abs(plain-withSilence) > 0.25 { // the blocks at the edges are half music
		t.Errorf("silence around the music changed the loudness: %.2f against %.2f", withSilence, plain)
	}
	// a long quiet intro (more than 10 LU below the rest) is gated out as well
	quietIntro := append(sine(440, 0.01, 10), music...)
	got, _ := loudness(quietIntro)
	if math.Abs(plain-got) > 0.3 {
		t.Errorf("a quiet intro changed the loudness: %.2f against %.2f", got, plain)
	}
}

func TestLoudnessOfSilenceIsUnknown(t *testing.T) {
	if _, ok := loudness(make([]float32, 10*SampleRate)); ok {
		t.Error("silence has no loudness")
	}
	if _, ok := loudness(make([]float32, SampleRate/10)); ok {
		t.Error("less than one block of audio has no loudness")
	}
}

func TestPeakIsTheLargestSample(t *testing.T) {
	x := sine(440, 0.5, 2)
	x[100] = -0.9
	if got := peakOf(x); math.Abs(got-0.9) > 1e-6 {
		t.Errorf("peak = %v, want 0.9", got)
	}
}

func TestAnalysisReportsLoudnessAndPeak(t *testing.T) {
	x := clickTrack(120, 40)
	res, err := Analyze(x)
	if err != nil {
		t.Fatal(err)
	}
	if res.Loudness == 0 || res.Peak <= 0 {
		t.Errorf("loudness %v and peak %v should be set", res.Loudness, res.Peak)
	}
}
