package autodj

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/PatrickSteil/ytplay/internal/analyze"
)

const sr = analyze.SampleRate

func constant(seconds, v float64) []float32 {
	x := make([]float32, int(seconds*sr))
	for i := range x {
		x[i] = float32(v)
	}
	return x
}

// resample is a Stretch that plays x `speed` times as fast by linear
// interpolation (the pitch changes, which does not matter for timing tests).
func resample(x []float32, speed float64) []float32 {
	out := make([]float32, int(float64(len(x))/speed))
	for i := range out {
		pos := float64(i) * speed
		j := int(pos)
		if j+1 >= len(x) {
			break
		}
		f := float32(pos - float64(j))
		out[i] = x[j]*(1-f) + x[j+1]*f
	}
	return out
}

var testPlan = Plan{Kind: BeatMatch, AStart: 20, BFrom: 1, Overlap: 8, BSpeed: 1}

func TestRenderLength(t *testing.T) {
	out := Render(constant(40, 0.5), constant(40, 0.5), testPlan, 3, 4, resample)
	if want := int(15 * sr); math.Abs(float64(len(out)-want)) > 1 {
		t.Errorf("length = %d samples, want %d (lead + overlap + tail = 15 s)", len(out), want)
	}
}

func TestRenderVolumeCurve(t *testing.T) {
	a := Render(constant(40, 1), constant(40, 0), testPlan, 3, 4, resample)
	b := Render(constant(40, 0), constant(40, 1), testPlan, 3, 4, resample)
	at := func(x []float32, secondsIntoOutput float64) float64 { return float64(x[int(secondsIntoOutput*sr)]) }
	near(t, "A before the overlap", at(a, 1), 1, 1e-6)
	near(t, "A at the start of the overlap", at(a, 3.0001), 1, 1e-3)
	near(t, "A in the middle (equal power)", at(a, 3+4), math.Sqrt(0.5), 1e-3)
	near(t, "A after the overlap", at(a, 3+8+1), 0, 1e-6)
	near(t, "B before it starts", at(b, 1), 0, 1e-6)
	near(t, "B in the middle (equal power)", at(b, 3+4), math.Sqrt(0.5), 1e-3)
	near(t, "B after the overlap", at(b, 3+8+1), 1, 1e-6)
	// equal power: the squares of the two gains always add up to 1
	for _, s := range []float64{3.5, 5, 6.5, 8, 10} {
		g := math.Pow(at(a, s), 2) + math.Pow(at(b, s), 2)
		near(t, "sum of squared gains", g, 1, 1e-3)
	}
}

// impulseAt returns where (in seconds) the strongest sample of x[from:to] is.
func impulseAt(x []float32, from, to float64) float64 {
	best, at := 0.0, 0
	for i := int(from * sr); i < int(to*sr) && i < len(x); i++ {
		if v := math.Abs(float64(x[i])); v > best {
			best, at = v, i
		}
	}
	return float64(at) / sr
}

func TestRenderPlacesTheIncomingSong(t *testing.T) {
	// A click in the next song 2 s after the point it starts from, and nothing else.
	b := make([]float32, 60*sr)
	b[int(3*sr)] = 1 // BFrom is 1 s, so the click is 2 s into what is heard of B
	silentA := constant(40, 0)

	out := Render(silentA, b, testPlan, 3, 10, resample)
	near(t, "click at speed 1", impulseAt(out, 4, 8), 3+2, 2.0/sr)

	fast := testPlan
	fast.BSpeed = 2
	out = Render(silentA, b, fast, 3, 10, resample)
	near(t, "click at speed 2 comes twice as soon", impulseAt(out, 4, 8), 3+1, 3.0/sr)
}

func TestRenderPlacesThePlayingSong(t *testing.T) {
	a := make([]float32, 40*sr)
	a[int(18*sr)] = 1 // 2 s before AStart
	out := Render(a, constant(40, 0), testPlan, 3, 4, resample)
	near(t, "click in the playing song", impulseAt(out, 0, 3), 1, 2.0/sr) // lead 3 s: AStart - 2 s is 1 s in
}

func TestRenderBeforeTheStartOfASongIsSilent(t *testing.T) {
	// a lead longer than the playing song has played so far
	out := Render(constant(40, 1), constant(40, 0), Plan{AStart: 2, BFrom: 0, Overlap: 4, BSpeed: 1}, 5, 2, resample)
	if out[0] != 0 || out[int(2.9*sr)] != 0 {
		t.Errorf("before the start of the playing song: %v, %v, want silence", out[0], out[int(2.9*sr)])
	}
	if v := out[int(3.5*sr)]; v < 0.99 {
		t.Errorf("after its start the playing song is at full level, got %v", v)
	}
}

func TestWriteWAVRoundTrip(t *testing.T) {
	x := []float32{0, 0.5, -0.5, 1, -1, 0.25}
	path := filepath.Join(t.TempDir(), "x.wav")
	if err := WriteWAV(path, x); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw[:4]) != "RIFF" || string(raw[8:12]) != "WAVE" || binary.LittleEndian.Uint32(raw[24:]) != sr {
		t.Fatalf("bad header: %q", raw[:44])
	}
	if got := binary.LittleEndian.Uint32(raw[40:]); int(got) != 2*len(x) {
		t.Errorf("data size = %d, want %d", got, 2*len(x))
	}
	for i, want := range x {
		got := float64(int16(binary.LittleEndian.Uint16(raw[44+2*i:]))) / 32767
		near(t, "sample", got, float64(want), 1.0/32767)
	}
}
