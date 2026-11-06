// Package analyze estimates tempo (BPM) and musical key from raw audio.
//
// It is plain Go with no dependencies. The methods are the standard textbook
// ones, kept deliberately simple:
//
//   - tempo: a log-compressed spectral-flux onset envelope in three frequency
//     bands, autocorrelated, with a mild preference for 120 BPM and a fix for
//     the usual half-tempo mistake;
//   - key: a pitch-class (chroma) histogram built from spectral peaks, where
//     every peak also votes for the notes it could be an overtone of, matched
//     against the Albrecht & Shanahan major / minor key profiles.
//
// Expect it to be right for most pop, rock and electronic music and to be
// wrong now and then: tempo can come out at half or double speed, and the key
// can land on a neighbouring key.
package analyze

import (
	"errors"
	"math"
)

// SampleRate is the sample rate the analysis expects (mono).
const SampleRate = 22050

const minSeconds = 10

// Result is what could be learned from a piece of audio.
type Result struct {
	BPM      float64 // beats per minute, 0 if unknown
	Key      string  // e.g. "F# minor"
	Camelot  string  // DJ notation of the key, e.g. "11A"
	KeyScore float64 // how well the best key matched, 0..1
}

// Analyze estimates tempo and key of mono PCM at SampleRate.
func Analyze(pcm []float32) (Result, error) {
	if len(pcm) < minSeconds*SampleRate {
		return Result{}, errors.New("not enough audio to analyse")
	}
	var energy float64
	for _, v := range pcm {
		energy += float64(v) * float64(v)
	}
	if math.Sqrt(energy/float64(len(pcm))) < 1e-4 {
		return Result{}, errors.New("audio is silent")
	}
	x := make([]float64, len(pcm))
	for i, v := range pcm {
		x[i] = float64(v)
	}
	res := Result{BPM: tempo(x)}
	res.Key, res.KeyScore = detectKey(x)
	if res.Key != "" {
		res.Camelot = camelot(res.Key)
	}
	return res, nil
}

// ---- FFT ----

type fftPlan struct {
	n        int
	cos, sin []float64
	rev      []int
}

func newFFT(n int) *fftPlan {
	p := &fftPlan{n: n, cos: make([]float64, n/2), sin: make([]float64, n/2), rev: make([]int, n)}
	for i := range p.cos {
		a := -2 * math.Pi * float64(i) / float64(n)
		p.cos[i], p.sin[i] = math.Cos(a), math.Sin(a)
	}
	bits := 0
	for 1<<bits < n {
		bits++
	}
	for i := range p.rev {
		r := 0
		for b := 0; b < bits; b++ {
			if i&(1<<b) != 0 {
				r |= 1 << (bits - 1 - b)
			}
		}
		p.rev[i] = r
	}
	return p
}

// transform is an in-place radix-2 FFT of re+i*im (length must be the plan's n).
func (p *fftPlan) transform(re, im []float64) {
	n := p.n
	for i, j := range p.rev {
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		half, step := size/2, n/size
		for start := 0; start < n; start += size {
			for k := 0; k < half; k++ {
				c, s := p.cos[k*step], p.sin[k*step]
				a, b := start+k, start+k+half
				tr := re[b]*c - im[b]*s
				ti := re[b]*s + im[b]*c
				re[b], im[b] = re[a]-tr, im[a]-ti
				re[a], im[a] = re[a]+tr, im[a]+ti
			}
		}
	}
}

func hann(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
	}
	return w
}

// spectrogram returns the magnitudes (bins 0..n/2) of consecutive windows.
func spectrogram(x []float64, n, hop int) [][]float64 {
	plan, win := newFFT(n), hann(n)
	frames := (len(x) - n) / hop
	out := make([][]float64, 0, max(frames, 0))
	re, im := make([]float64, n), make([]float64, n)
	for t := 0; t < frames; t++ {
		seg := x[t*hop : t*hop+n]
		for i := range re {
			re[i], im[i] = seg[i]*win[i], 0
		}
		plan.transform(re, im)
		mag := make([]float64, n/2+1)
		for k := range mag {
			mag[k] = math.Hypot(re[k], im[k])
		}
		out = append(out, mag)
	}
	return out
}

// ---- tempo ----

const (
	tempoWin    = 1024
	tempoHop    = 256
	minBPM      = 60.0
	maxBPM      = 200.0
	bpmCenter   = 120.0
	bpmSigma    = 1.5 // width of the preference for the center, in octaves
	doubleAbove = 0.6 // see tempo
)

// onsetEnvelope is the per-frame "something starts now" strength.
func onsetEnvelope(x []float64) []float64 {
	spec := spectrogram(x, tempoWin, tempoHop)
	if len(spec) < 2 {
		return nil
	}
	binHz := float64(SampleRate) / tempoWin
	env := make([]float64, len(spec)-1)
	for _, band := range [][2]float64{{30, 200}, {200, 2000}, {2000, 8000}} {
		var bins []int
		for k := range spec[0] {
			if f := float64(k) * binHz; f >= band[0] && f < band[1] {
				bins = append(bins, k)
			}
		}
		flux := make([]float64, len(env))
		for t := range flux {
			for _, k := range bins {
				d := math.Log1p(100*spec[t+1][k]) - math.Log1p(100*spec[t][k])
				if d > 0 {
					flux[t] += d
				}
			}
		}
		// Remove the local mean (21-frame window, zero padded), keep positives.
		prefix := make([]float64, len(flux)+1)
		for i, v := range flux {
			prefix[i+1] = prefix[i] + v
		}
		var mean, sq float64
		for i := range flux {
			lo, hi := max(i-10, 0), min(i+11, len(flux))
			flux[i] = math.Max(flux[i]-(prefix[hi]-prefix[lo])/21, 0)
			mean += flux[i]
		}
		mean /= float64(len(flux))
		for _, v := range flux {
			sq += (v - mean) * (v - mean)
		}
		std := math.Sqrt(sq / float64(len(flux)))
		for i, v := range flux {
			env[i] += v / (std + 1e-9)
		}
	}
	return env
}

// tempo estimates the BPM from the autocorrelation of the onset envelope.
func tempo(x []float64) float64 {
	env := onsetEnvelope(x)
	if len(env) < 100 {
		return 0
	}
	var mean float64
	for _, v := range env {
		mean += v
	}
	mean /= float64(len(env))
	for i := range env {
		env[i] -= mean
	}
	fps := float64(SampleRate) / tempoHop
	maxLag := min(int(4*fps)+3, len(env)-1)
	ac := make([]float64, maxLag+1)
	for lag := range ac {
		var s float64
		for i := 0; i+lag < len(env); i++ {
			s += env[i] * env[i+lag]
		}
		ac[lag] = s
	}
	if ac[0] <= 0 {
		return 0
	}
	for i := len(ac) - 1; i >= 0; i-- {
		ac[i] /= ac[0]
	}

	// How strongly the envelope repeats with the period of b BPM: the lag
	// itself plus its multiples, which all line up for the true tempo.
	periodicity := func(b float64) float64 {
		var s float64
		for _, h := range [...]struct{ mult, weight float64 }{{1, 1}, {2, .5}, {4, .25}} {
			l := fps * 60 / b * h.mult
			i := int(l)
			if i+1 < len(ac) {
				s += h.weight * (ac[i]*(1-(l-float64(i))) + ac[i+1]*(l-float64(i)))
			}
		}
		return s
	}
	best, bestScore := 0.0, math.Inf(-1)
	for b := minBPM; b < maxBPM; b += 0.25 {
		d := math.Log2(b/bpmCenter) / bpmSigma
		if s := periodicity(b) * math.Exp(-0.5*d*d); s > bestScore {
			best, bestScore = b, s
		}
	}
	// A half-tempo reading is the classic mistake: if the double tempo repeats
	// almost as strongly, report the double.
	if 2*best <= 190 && periodicity(2*best) >= doubleAbove*periodicity(best) {
		best *= 2
	}
	return best
}

// ---- key ----

var noteNames = [12]string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}

// Albrecht & Shanahan (2013) key profiles; index 0 is the tonic.
var (
	profileMajor = [12]float64{.238, .006, .111, .006, .137, .094, .016, .214, .009, .080, .008, .081}
	profileMinor = [12]float64{.220, .006, .104, .123, .019, .103, .012, .214, .062, .022, .061, .052}
)

const (
	keyWin      = 8192
	keyHop      = 2048
	keyMinHz    = 80.0
	keyMaxHz    = 3000.0
	keyHarmonic = 6   // a peak also votes for notes it may be an overtone of
	keyDecay    = 0.6 // weight of each further overtone
)

// chroma sums, over all frames, the energy per pitch class.
func chroma(x []float64) [12]float64 {
	spec := spectrogram(x, keyWin, keyHop)
	binHz := float64(SampleRate) / keyWin
	lo, hi := int(math.Ceil(keyMinHz/binHz)), int(keyMaxHz/binHz)

	type peak struct {
		frame int
		midi  float64
		amp   float64
	}
	var peaks []peak
	for t, mag := range spec {
		var top float64
		for k := lo; k <= hi; k++ {
			top = math.Max(top, mag[k])
		}
		for k := lo + 1; k < hi; k++ {
			a, b, c := mag[k-1], mag[k], mag[k+1]
			if !(b > a && b >= c && b > 0.05*top) {
				continue
			}
			d := 0.5 * (a - c) / (a - 2*b + c + 1e-12) // parabolic interpolation
			freq := (float64(k) + d) * binHz
			peaks = append(peaks, peak{t, 12*math.Log2(freq/440) + 69, math.Sqrt(b)})
		}
	}
	if len(peaks) == 0 {
		return [12]float64{}
	}
	// Global tuning offset: circular mean of how far peaks sit from a semitone.
	var sx, sy float64
	for _, p := range peaks {
		dev := p.midi - math.Round(p.midi)
		sx += p.amp * math.Cos(2*math.Pi*dev)
		sy += p.amp * math.Sin(2*math.Pi*dev)
	}
	shift := math.Atan2(sy, sx) / (2 * math.Pi)

	frames := make([][12]float64, len(spec))
	for _, p := range peaks {
		w := p.amp
		for h := 1; h <= keyHarmonic; h++ {
			pc := int(math.Round(p.midi-shift-12*math.Log2(float64(h)))) % 12
			if pc < 0 {
				pc += 12
			}
			frames[p.frame][pc] += w
			w *= keyDecay
		}
	}
	var total [12]float64
	for _, f := range frames { // each frame counts equally, loud or not
		var norm float64
		for _, v := range f {
			norm += v * v
		}
		norm = math.Sqrt(norm) + 1e-9
		for i, v := range f {
			total[i] += v / norm
		}
	}
	return total
}

// pearson is the correlation coefficient of two 12-vectors.
func pearson(a, b [12]float64) float64 {
	var ma, mb float64
	for i := range a {
		ma += a[i] / 12
		mb += b[i] / 12
	}
	var num, da, db float64
	for i := range a {
		num += (a[i] - ma) * (b[i] - mb)
		da += (a[i] - ma) * (a[i] - ma)
		db += (b[i] - mb) * (b[i] - mb)
	}
	if da == 0 || db == 0 {
		return 0
	}
	return num / math.Sqrt(da*db)
}

// detectKey returns the best matching key and its correlation (0 when none).
func detectKey(x []float64) (string, float64) {
	c := chroma(x)
	best, bestKey := 0.0, ""
	for tonic := 0; tonic < 12; tonic++ {
		for _, cand := range [...]struct {
			mode    string
			profile *[12]float64
		}{{"major", &profileMajor}, {"minor", &profileMinor}} {
			var rolled [12]float64
			for i := range rolled {
				rolled[(i+tonic)%12] = cand.profile[i]
			}
			if r := pearson(c, rolled); r > best {
				best, bestKey = r, noteNames[tonic]+" "+cand.mode
			}
		}
	}
	return bestKey, math.Min(best, 1)
}

var camelotMajor = map[string]string{"B": "1B", "F#": "2B", "C#": "3B", "G#": "4B", "D#": "5B", "A#": "6B",
	"F": "7B", "C": "8B", "G": "9B", "D": "10B", "A": "11B", "E": "12B"}
var camelotMinor = map[string]string{"G#": "1A", "D#": "2A", "A#": "3A", "F": "4A", "C": "5A", "G": "6A",
	"D": "7A", "A": "8A", "E": "9A", "B": "10A", "F#": "11A", "C#": "12A"}

// camelot converts "F# minor" to the DJ notation "11A".
func camelot(key string) string {
	for i := 0; i < len(key); i++ {
		if key[i] == ' ' {
			if key[i+1:] == "minor" {
				return camelotMinor[key[:i]]
			}
			return camelotMajor[key[:i]]
		}
	}
	return ""
}
