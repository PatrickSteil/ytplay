// Package analyze estimates tempo (BPM) and musical key from raw audio.
//
// It is plain Go with no dependencies. The methods are the standard textbook
// ones, kept deliberately simple:
//
//   - tempo: a log-compressed spectral-flux onset envelope in three frequency
//     bands, autocorrelated. A tempo is scored by how strongly the envelope
//     repeats at many multiples of its beat period (up to four bars), with a
//     mild preference for 120 BPM. When even the best tempo repeats only
//     weakly (speech, ambient music, free-time playing) none is reported;
//   - key: a pitch-class (chroma) histogram built from spectral peaks, where
//     every peak also votes for the notes it could be an overtone of, matched
//     against the Albrecht & Shanahan major / minor key profiles.
//
// The whole song should be analysed, not a clip: the more audio, the steadier
// both estimates. Expect them to be right for most pop, rock and electronic
// music and to be wrong now and then: tempo can come out at half or double
// speed, and the key can land on a neighbouring key.
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
	BPM        float64 // beats per minute, 0 if there is no steady beat
	TempoScore float64 // how regularly the audio repeats at that tempo, 0..1
	Key        string  // e.g. "F# minor"
	Camelot    string  // DJ notation of the key, e.g. "11A"
	KeyScore   float64 // how well the best key matched, 0..1
	// Edges is where the music starts and stops and, if there is a steady beat,
	// where the beats fall at both ends (without one the beat grids are empty).
	Edges Edges
	// Level says how much was asked for. A Light result has no Edges.
	Level Level
	// Wave is the loudness over the analysed audio, for drawing.
	Wave Wave
	// Loudness is the integrated loudness of the analysed audio in LUFS (0 if it
	// could not be measured), Peak its largest sample (1 is full scale). The
	// autodj evens out the loudness of two songs by it.
	Loudness, Peak float64
}

// Level is how much of an analysis is wanted.
type Level int

const (
	// Full is tempo, key and the beat grids at both ends: all that the DJ
	// features need. It is the zero value, so a Result nobody labelled is Full.
	Full Level = iota
	// Light is tempo and key only, what the header shows: no beat grids (measured:
	// 4.3 s of compute per song instead of 6.0 s, 29% less).
	Light
)

// Options says how an analysis is done.
type Options struct {
	Level Level
	// Pool, if not nil, spreads the work over its workers, and lets the key be
	// computed beside the tempo. The result is the same either way.
	Pool *Pool
}

// Analyze estimates tempo, key and beat grids of mono PCM at SampleRate.
func Analyze(pcm []float32) (Result, error) { return AnalyzeWith(pcm, Options{}) }

// AnalyzeWith is Analyze at the level, and on the pool, that Options asks for.
func AnalyzeWith(pcm []float32, o Options) (Result, error) {
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
	res := Result{Level: o.Level, Wave: waveOf(pcm, 0)}
	if lufs, ok := loudness(pcm); ok {
		res.Loudness = lufs
	}
	res.Peak = peakOf(pcm)
	// The key does not depend on the tempo: with a pool it is worked out beside it.
	var key string
	var keyScore float64
	keyDone := make(chan struct{})
	findKey := func() {
		key, keyScore = detectKey(pcm, o.Pool)
		close(keyDone)
	}
	if o.Pool != nil {
		go findKey()
	}
	res.BPM, res.TempoScore = tempo(pcm, o.Pool)
	if res.TempoScore < minTempoScore {
		res.BPM = 0 // no steady beat: better to say nothing than something wrong
		if o.Level == Full {
			start, end := audibleRange(pcm) // but where the music starts and stops is still known
			res.Edges = Edges{AudioStart: start, AudioEnd: end}
		}
	} else if o.Level == Full {
		res.Edges = edgesOf(pcm, res.BPM, o.Pool)
	}
	if o.Level == Light { // no beat grids, but where the sound starts and stops is cheap, and enough to trim silence by
		start, end := audibleRange(pcm)
		res.Edges = Edges{AudioStart: start, AudioEnd: end}
	}
	if o.Pool == nil {
		findKey()
	}
	<-keyDone
	res.Key, res.KeyScore = key, keyScore
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

// frameBatch is how many frames one goroutine handles at a time. It is a
// variable only so that tests can ask for a single batch, the plain one-pass
// loop the batched analysis must reproduce exactly.
var frameBatch = 64

// framer cuts x into windows of n samples, one every hop, and gives their
// spectra. Windows are handed over a batch at a time and never all at once,
// because a whole song's spectrogram would take hundreds of megabytes.
type framer struct {
	x      []float32
	n, hop int
	plan   *fftPlan
	win    []float64
}

func newFramer(x []float32, n, hop int) *framer {
	return &framer{x: x, n: n, hop: hop, plan: newFFT(n), win: hann(n)}
}

// frames is how many windows fit in x.
func (f *framer) frames() int { return max((len(f.x)-f.n)/f.hop, 0) }

// each calls fn with the magnitudes (bins 0..n/2) of windows from to to-1, in
// order. The slice is reused between calls: copy it to keep it. It is safe to
// call from several goroutines at once (the plan and the window are only read).
func (f *framer) each(from, to int, fn func(t int, mag []float64)) {
	re, im := make([]float64, f.n), make([]float64, f.n)
	mag := make([]float64, f.n/2+1)
	for t := from; t < to; t++ {
		seg := f.x[t*f.hop : t*f.hop+f.n]
		for i := range re {
			re[i], im[i] = float64(seg[i])*f.win[i], 0
		}
		f.plan.transform(re, im)
		for k := range mag {
			mag[k] = math.Hypot(re[k], im[k])
		}
		fn(t, mag)
	}
}

// batches is how many batches of frameBatch there are for n items.
func batches(n int) int { return (n + frameBatch - 1) / frameBatch }

// bandRanges are the frequency bands (Hz) the onsets are measured in.
var bandRanges = [3][2]float64{{30, 200}, {200, 2000}, {2000, 8000}}

// bandBins are the FFT bins of a window of win samples that fall in each band.
func bandBins(win int) (bins [3][]int) {
	binHz := float64(SampleRate) / float64(win)
	for b, r := range bandRanges {
		for k := 0; k <= win/2; k++ {
			if f := float64(k) * binHz; f >= r[0] && f < r[1] {
				bins[b] = append(bins[b], k)
			}
		}
	}
	return bins
}

// bandFlux is, for each band, how much the log-compressed spectrum grew from
// each window to the next (only growth counts): value t-1 belongs to window t.
// It is the raw material of both onset envelopes (the coarse one for the tempo,
// the fine one for the beat grids). Batches of windows are computed on the pool;
// each starts by recomputing the window before its first, so every value comes
// out of the same operations as in a single pass.
func bandFlux(x []float32, win, hop int, pool *Pool) (flux [3][]float64) {
	f := newFramer(x, win, hop)
	frames := f.frames()
	if frames < 2 {
		return flux
	}
	for b := range flux {
		flux[b] = make([]float64, frames-1)
	}
	bins := bandBins(win)
	pool.Run(batches(frames-1), func(k int) {
		j0 := k * frameBatch
		j1 := min(j0+frameBatch, frames-1) // this batch makes flux[j0:j1]
		prev, cur := make([]float64, win/2+1), make([]float64, win/2+1)
		f.each(j0, j1+1, func(t int, mag []float64) {
			for i, m := range mag {
				cur[i] = math.Log1p(100 * m)
			}
			if t > j0 {
				for b := range bins {
					var s float64
					for _, i := range bins[b] {
						if d := cur[i] - prev[i]; d > 0 {
							s += d
						}
					}
					flux[b][t-1] = s
				}
			}
			prev, cur = cur, prev
		})
	})
	return flux
}

// ---- tempo ----

const (
	tempoWin  = 1024
	tempoHop  = 256
	minBPM    = 60.0
	maxBPM    = 200.0
	bpmCenter = 120.0
	bpmSigma  = 1.5 // width of the preference for the center, in octaves

	combMultiples = 16   // multiples of the beat period that are checked
	combMinTerms  = 4    // with fewer usable multiples a tempo is not scored
	maxLagSeconds = 20.0 // longest lag of the autocorrelation

	// minTempoScore separates songs from everything else. On the evaluation
	// corpus the weakest song scores 0.12 and the strongest non-song (speech,
	// rain, ambient, rubato piano) 0.02.
	minTempoScore = 0.06
)

// onsetEnvelope is the per-frame "something starts now" strength.
func onsetEnvelope(x []float32, pool *Pool) []float64 {
	flux := bandFlux(x, tempoWin, tempoHop, pool)
	if len(flux[0]) < 1 {
		return nil
	}
	env := make([]float64, len(flux[0]))
	for _, flux := range flux {
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

// autocorrelation returns r[0..maxLag] of the mean-removed signal. Every lag
// is averaged over the samples that have a partner, so long lags are not
// penalised, and r is scaled so that r[0] is 1. It is nil for a flat signal.
func autocorrelation(x []float64, maxLag int) []float64 {
	n := len(x)
	var mean float64
	for _, v := range x {
		mean += v
	}
	mean /= float64(n)
	e := make([]float64, n)
	for i, v := range x {
		e[i] = v - mean
	}
	r := make([]float64, maxLag+1)
	for lag := range r {
		var s float64
		for i := 0; i+lag < n; i++ {
			s += e[i] * e[i+lag]
		}
		r[lag] = s / float64(n-lag)
	}
	if r[0] <= 0 {
		return nil
	}
	for i := len(r) - 1; i >= 0; i-- {
		r[i] /= r[0]
	}
	return r
}

// tempo estimates the BPM from the autocorrelation of the onset envelope and
// says how regularly the audio repeats at that tempo (0..1).
//
// A tempo b is scored by the mean autocorrelation at the first
// combMultiples multiples of its beat period. Looking this far, up to the
// length of several bars, is what tells the beat from a rhythm that merely
// repeats a lot, such as a dotted pattern 1.5 beats long: that one lines up
// with the bars only every third bar, while the beat lines up with every one.
func tempo(x []float32, pool *Pool) (bpm, score float64) {
	env := onsetEnvelope(x, pool)
	if len(env) < 100 {
		return 0, 0
	}
	fps := float64(SampleRate) / tempoHop
	// Never use lags beyond half the audio: they would rest on too little overlap.
	maxLag := min(int(maxLagSeconds*fps), (len(env)-1)/2)
	ac := autocorrelation(env, maxLag)
	if ac == nil {
		return 0, 0
	}

	comb := func(b float64) float64 {
		var s float64
		var n int
		for k := 1; k <= combMultiples; k++ {
			l := fps * 60 / b * float64(k)
			i := int(l)
			if i+1 > maxLag {
				break
			}
			s += ac[i]*(1-(l-float64(i))) + ac[i+1]*(l-float64(i))
			n++
		}
		if n < combMinTerms {
			return 0
		}
		return s / float64(n)
	}
	best, bestWeighted, bestScore := 0.0, 0.0, 0.0
	for b := minBPM; b < maxBPM; b += 0.25 {
		s := comb(b)
		d := math.Log2(b/bpmCenter) / bpmSigma
		if w := s * math.Exp(-0.5*d*d); w > bestWeighted {
			best, bestWeighted, bestScore = b, w, s
		}
	}
	return best, bestScore
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
func chroma(x []float32, pool *Pool) [12]float64 {
	binHz := float64(SampleRate) / keyWin
	lo, hi := int(math.Ceil(keyMinHz/binHz)), int(keyMaxHz/binHz)

	type peak struct {
		frame int
		midi  float64
		amp   float64
	}
	f := newFramer(x, keyWin, keyHop)
	nFrames := f.frames()
	perBatch := make([][]peak, batches(nFrames)) // each batch's peaks, in frame order
	pool.Run(len(perBatch), func(k int) {
		var mine []peak
		f.each(k*frameBatch, min((k+1)*frameBatch, nFrames), func(t int, mag []float64) {
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
				mine = append(mine, peak{t, 12*math.Log2(freq/440) + 69, math.Sqrt(b)})
			}
		})
		perBatch[k] = mine
	})
	var peaks []peak
	for _, mine := range perBatch {
		peaks = append(peaks, mine...)
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

	frames := make([][12]float64, nFrames)
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
func detectKey(x []float32, pool *Pool) (string, float64) {
	c := chroma(x, pool)
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
