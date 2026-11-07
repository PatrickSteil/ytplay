package analyze

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// steps is a 200 Hz tone, quiet for the first quiet seconds, then loud until
// seconds: the shape of an intro and a drop.
func steps(seconds, quiet int, low, high float64) []float32 {
	x := make([]float32, seconds*SampleRate)
	for i := range x {
		a := high
		if i < quiet*SampleRate {
			a = low
		}
		x[i] = float32(a * math.Sin(2*math.Pi*200*float64(i)/SampleRate))
	}
	return x
}

func TestWaveFollowsTheLoudnessOverTime(t *testing.T) {
	w := waveOf(steps(100, 40, 0.05, 0.5), 0)
	if len(w.Level) != waveBuckets {
		t.Fatalf("%d slices, want %d", len(w.Level), waveBuckets)
	}
	if math.Abs(w.Seconds-100) > 0.01 || w.Offset != 0 {
		t.Errorf("covers %.2f s from %.2f, want 100 s from 0", w.Seconds, w.Offset)
	}
	// 40% of the slices are the quiet intro (a tenth of the loud level), the
	// rest the loud part, which is what the others are measured against.
	for i, v := range w.Level {
		quiet := i < waveBuckets*40/100-2
		loud := i > waveBuckets*40/100+2
		switch {
		case quiet && v > 40:
			t.Fatalf("slice %d of the intro is %d, want about 25", i, v)
		case loud && v < 245:
			t.Fatalf("slice %d of the loud part is %d, want 255", i, v)
		}
	}
}

func TestWaveIsLinearInAmplitude(t *testing.T) {
	// Half the amplitude is half the level: not a quarter (power), not a log scale.
	w := waveOf(steps(100, 50, 0.25, 0.5), 0)
	for _, i := range []int{50, 200, waveBuckets/2 - 5} {
		if v := w.Level[i]; v < 126 || v > 129 {
			t.Errorf("slice %d of the half-amplitude part is %d, want 127 or 128", i, v)
		}
	}
	if v := w.Level[waveBuckets-50]; v < 253 {
		t.Errorf("the louder part is %d, want 255 (give or take rounding)", v)
	}
}

func TestWaveIgnoresASingleLoudSpike(t *testing.T) {
	// One click as loud as full scale must not flatten the rest of the song:
	// the levels are measured against the 98th percentile, not the maximum.
	x := steps(100, 0, 0.5, 0.2)
	for i := 0; i < 2000; i++ {
		x[50*SampleRate+i] = float32(math.Copysign(1, math.Sin(float64(i))))
	}
	w := waveOf(x, 0)
	var low int
	for _, v := range w.Level {
		if v < 200 {
			low++
		}
	}
	if low != 0 {
		t.Errorf("%d slices fell below 200: the spike set the scale", low)
	}
}

func TestWaveOfSilenceIsFlatAndOfNothingIsEmpty(t *testing.T) {
	w := waveOf(make([]float32, 30*SampleRate), 0)
	for i, v := range w.Level {
		if v != 0 {
			t.Fatalf("silent slice %d is %d", i, v)
		}
	}
	if w := waveOf(nil, 0); len(w.Level) != 0 || w.Seconds != 0 {
		t.Errorf("no audio gave %+v", w)
	}
}

func TestWaveOfAShortClipHasASliceForEverySample(t *testing.T) {
	w := waveOf([]float32{0.1, 0.5, 0.25}, 0)
	if len(w.Level) != 3 {
		t.Errorf("%d slices for 3 samples", len(w.Level))
	}
}

func TestAnalysisCarriesTheWave(t *testing.T) {
	x := clickTrack(120, 30)
	for _, level := range []Level{Light, Full} {
		res, err := AnalyzeWith(x, Options{Level: level})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Wave.Level) == 0 || math.Abs(res.Wave.Seconds-30) > 0.01 || res.Wave.Offset != 0 {
			t.Errorf("level %v: wave %d slices, %.2f s from %.2f", level, len(res.Wave.Level), res.Wave.Seconds, res.Wave.Offset)
		}
	}
}

func TestAnExcerptsWaveSaysWhereInTheSongItIs(t *testing.T) {
	x := clickTrack(120, 30)
	res, err := analyzeExcerpt(x, Excerpt{Offset: 100, Duration: 700}, Options{Level: Full})
	if err != nil {
		t.Fatal(err)
	}
	if res.Wave.Offset != 100 || math.Abs(res.Wave.Seconds-30) > 0.01 {
		t.Errorf("wave from %.2f for %.2f s, want from 100 for 30", res.Wave.Offset, res.Wave.Seconds)
	}
	if res.Edges != (Edges{}) {
		t.Error("the edges of a middle excerpt are still dropped")
	}
}

// toneMpv is a fake mpv that writes 20 s of a loud tone for every start in
// answers (and almost nothing for the others).
func toneMpv(t *testing.T, answers ...string) string {
	t.Helper()
	tone := steps(20, 0, 0.5, 0.5)
	raw := make([]byte, 4*len(tone))
	for i, v := range tone {
		binary.NativeEndian.PutUint32(raw[4*i:], math.Float32bits(v))
	}
	pcm := filepath.Join(t.TempDir(), "pcm")
	if err := os.WriteFile(pcm, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	var cases string
	for _, a := range answers {
		cases += ` [ "$start" = "` + a + `" ] ||`
	}
	mpv, _ := fakeMpv(t, `if`+strings.TrimSuffix(cases, " ||")+`; then cat `+pcm+` > "$file"; else head -c 400 /dev/zero > "$file"; fi`)
	return mpv
}

func TestTheWaveOfALongTrackSaysItIsFromTheMiddle(t *testing.T) {
	res, err := SongWith(context.Background(), toneMpv(t, "700"), "yt-dlp", "abc", 2000, Options{Level: Light})
	if err != nil {
		t.Fatal(err)
	}
	if res.Wave.Offset != 700 || math.Abs(res.Wave.Seconds-20) > 0.01 {
		t.Errorf("wave from %.0f for %.2f s, want from 700 for 20", res.Wave.Offset, res.Wave.Seconds)
	}
}

func TestTheWaveIsNotPlacedInTheMiddleWhenOnlyTheBeginningWasFetched(t *testing.T) {
	// The middle of the long mix is missing and the beginning is read instead:
	// what was heard is the start of the song, not minute 11.
	res, err := SongWith(context.Background(), toneMpv(t, "0"), "yt-dlp", "abc", 2000, Options{Level: Light})
	if err != nil {
		t.Fatal(err)
	}
	if res.Wave.Offset != 0 || math.Abs(res.Wave.Seconds-20) > 0.01 {
		t.Errorf("wave from %.0f for %.2f s, want from 0 for 20", res.Wave.Offset, res.Wave.Seconds)
	}
}
