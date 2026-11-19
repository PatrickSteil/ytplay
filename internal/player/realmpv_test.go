package player

import (
	"context"
	"math"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PatrickSteil/ytplay/internal/analyze"
	"github.com/PatrickSteil/ytplay/internal/autodj"
	"github.com/PatrickSteil/ytplay/internal/mpv"
)

// These tests drive the Player on real mpv processes playing generated tones
// (silently, --ao=null). They skip when mpv or ffmpeg is missing.

func needTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"mpv", "ffmpeg"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
}

// tone writes a sine tone of the given length and returns its path.
func tone(t *testing.T, dir, name string, seconds float64) string {
	t.Helper()
	path := filepath.Join(dir, name)
	out, err := exec.Command("ffmpeg", "-loglevel", "error", "-y", "-f", "lavfi",
		"-i", "sine=frequency=440:duration="+strconv.FormatFloat(seconds, 'f', 2, 64), path).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	return path
}

// realDeck starts an mpv that plays silently.
func realDeck(ctx context.Context, name string) (Deck, error) {
	c, err := mpv.Start(ctx, "yt-dlp", name, "--ao=null")
	if err != nil {
		return nil, err
	}
	if err := c.ObserveAll(); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func TestPlayerPlaysTheQueueRealMpv(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	first, second := tone(t, dir, "a.wav", 1.2), tone(t, dir, "b.wav", 1.2)
	p, err := New(context.Background(), realDeck)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	if err := p.QueueMany(bg, []string{first, second}); err != nil {
		t.Fatal(err)
	}

	var positions []string
	timeout := time.After(10 * time.Second)
	for len(positions) < 3 {
		select {
		case ev := <-p.Events():
			if ev.Property == "playlist-pos" {
				positions = append(positions, string(ev.Data))
			}
		case <-timeout:
			t.Fatalf("playlist-pos events so far: %v, want 0, 1, null", positions)
		}
	}
	eq(t, "playlist-pos", positions, []string{"0", "1", "null"})
}

// TestGaplessHandoffRealMpv plays two 3 s tones. The second must be loaded on
// the second deck before the first ends, and must be audible within 300 ms of
// the player announcing it. (mpv's own time-pos stands still for a few hundred
// milliseconds while a song's last audio drains, so "the longest time without
// progress" would measure mpv, not the handoff.)
func TestGaplessHandoffRealMpv(t *testing.T) {
	needTools(t)
	oldLead, oldPoll := preloadLead, pollEvery
	preloadLead, pollEvery = 20*time.Second, 100*time.Millisecond // preload as soon as the first song plays
	t.Cleanup(func() { preloadLead, pollEvery = oldLead, oldPoll })

	dir := t.TempDir()
	first, second := tone(t, dir, "a.wav", 3), tone(t, dir, "b.wav", 3)
	p, err := New(context.Background(), realDeck)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	if err := p.QueueMany(bg, []string{first, second}); err != nil {
		t.Fatal(err)
	}

	var handedOff, audible time.Time
	sawPreload := false
	deadline := time.After(20 * time.Second)
	for idle := false; !idle; {
		select {
		case <-deadline:
			t.Fatal("the queue did not finish in 20 s")
		case ev := <-p.Events():
			if ev.Property == "playlist-pos" && string(ev.Data) == "1" {
				handedOff = time.Now()
			}
			idle = ev.Property == "idle-active" && string(ev.Data) == "true"
		case <-time.After(10 * time.Millisecond):
		}
		p.mu.Lock()
		sawPreload = sawPreload || p.pre != nil
		p.mu.Unlock()
		if !handedOff.IsZero() && audible.IsZero() {
			if e, _, err := p.Position(bg); err == nil && e > 0.02 {
				audible = time.Now()
			}
		}
	}
	if !sawPreload {
		t.Error("the second song was not preloaded before the first ended")
	}
	p.mu.Lock()
	onSecondDeck := p.active == 1
	p.mu.Unlock()
	if !onSecondDeck {
		t.Error("the second song did not play on the second deck")
	}
	if handedOff.IsZero() || audible.IsZero() {
		t.Fatal("never saw the second song start")
	}
	if gap := audible.Sub(handedOff); gap > 300*time.Millisecond {
		t.Errorf("the second song became audible %v after the handoff, want under 300 ms", gap)
	}
}

// kickTrack is a kick (55 Hz thump with a click) on every beat of a track of
// `seconds` plus a quiet pad, then silenceEnd seconds of silence; it returns
// the track and the kick times.
func kickTrack(bpm, first, seconds, silenceEnd float64) ([]float32, []float64) {
	sr := float64(analyze.SampleRate)
	n := int(seconds * sr)
	x := make([]float32, n)
	for i := range x {
		t := float64(i) / sr
		x[i] = float32(0.06 * (math.Sin(2*math.Pi*220*t) + math.Sin(2*math.Pi*277*t) + math.Sin(2*math.Pi*330*t)))
	}
	seed := uint32(11)
	var beats []float64
	for t := first; t < seconds-0.3; t += 60 / bpm {
		beats = append(beats, t)
		i0 := int(t * sr)
		for j := 0; j < int(0.25*sr) && i0+j < n; j++ {
			tt := float64(j) / sr
			v := 0.8 * math.Exp(-tt/0.06) * math.Sin(2*math.Pi*55*tt)
			if j < 110 {
				seed = seed*1664525 + 1013904223
				v += 0.3 * float64(int32(seed)) / float64(1<<31) * (1 - float64(j)/110)
			}
			x[i0+j] += float32(v)
		}
	}
	return append(x, make([]float32, int(silenceEnd*sr))...), beats
}

// TestBeatMatchRealMpv plays a beat-matched transition between two synthetic
// drum tracks on real decks and measures, from the decks' own position clocks,
// how far the incoming kicks are from the outgoing ones. mpv reports positions
// in steps, so each deck's clock is estimated from many readings (the freshest
// one is the closest to the truth).
func TestBeatMatchRealMpv(t *testing.T) {
	// Measured over repeated runs (8 in a row, nothing else running): a run
	// comes out in one of two ways, about half of them each: worst miss 8 to
	// 9 ms and mean offset +1 ms, or worst miss 14 to 18 ms and mean offset
	// +11 to +13 ms (b late). The step of about 11 ms is of the order of the audio chunk
	// that autodj.go says a deck start cannot be timed better than (the cause
	// is not pinned down). Both are
	// well inside what a flam needs to be audible, about 30 ms, so the limits
	// take in both, with a little room for a busy machine.
	beatMatchRealMpv(t, realCase{bpmA: 124, bpmB: 120, firstA: 0.337, firstB: 0.52, minKicks: 10, maxWorst: 0.025, maxMean: 0.016})
}

// The same at 70 BPM into 140 and 138 into 72: the plan keeps the next song
// near its own speed and lines up every beat of the slower song with a beat of
// the faster one.
//
// This only guards that the engine lands on the right beat (a wrong one is
// 200 ms or more out), so its limits are loose; the precision is that of the
// engine, which these songs show more than 124 into 120 does. Measured over
// repeated runs (worst kick, mean offset): 70 into 140 17 to 20 ms and -12 to
// -14 ms, 138 into 72 41 to 43 ms and -22 ms. Most of 138 into 72's mean is
// its first kick (about -42 ms, before the phase loop has settled) and a
// steady -17 ms after that: the loop's deadband (10 ms) plus the 5 to 9 ms by
// which the analysis puts the two songs' anchors apart. Starting the next song
// later or narrowing the deadband moves one of the cases towards 0 and another
// away from it, so the limits are the engine's, not tighter:
//   - The next song starts 18 to 24 ms early and the phase loop takes about 2 s
//     to bring that in. A song with a beat every 0.83 s (72 BPM) has its first
//     kick in that stretch: worst kick 23 to 33 ms over repeated runs.
//   - The loop leaves errors under phaseDeadband alone, so at exactly speed 1
//     (70 into 140) the early start is pulled in to about 10 ms and stays: a
//     steady 12 to 17 ms early, worst kick 22 ms. At speed 1.033 the loop is
//     always steering and the offset averages out.
func TestDoubleAndHalfTimeBeatMatchRealMpv(t *testing.T) {
	t.Run("70 into 140", func(t *testing.T) {
		beatMatchRealMpv(t, realCase{bpmA: 70, bpmB: 140, firstA: 0.337, firstB: 0.52, wantWhy: "double time", minKicks: 10, maxWorst: 0.045, maxMean: 0.020})
	})
	t.Run("138 into 72", func(t *testing.T) {
		// 72 BPM has half as many kicks in the overlap
		beatMatchRealMpv(t, realCase{bpmA: 138, bpmB: 72, firstA: 0.41, firstB: 0.15, wantWhy: "half time", minKicks: 6, maxWorst: 0.055, maxMean: 0.030})
	})
}

// realCase is one pair of drum tracks (a kick on every beat, the first at firstA
// or firstB seconds) to mix on real decks, and what to demand of the mix.
type realCase struct {
	bpmA, bpmB, firstA, firstB float64
	wantWhy                    string  // what the plan must say, if not empty
	minKicks                   int     // how many kicks of b must have been compared with kicks of a
	maxWorst, maxMean          float64 // the largest miss and the largest mean offset, in seconds
}

// beatMatchRealMpv plays song a into song b with autodj on, and checks that
// wherever a kick of b sounds near one of a, it is on it.
func beatMatchRealMpv(t *testing.T, c realCase) {
	bpmA, bpmB, firstA, firstB, wantWhy := c.bpmA, c.bpmB, c.firstA, c.firstB, c.wantWhy
	needTools(t)
	oldLead, oldPoll := preloadLead, pollEvery
	preloadLead, pollEvery = 30*time.Second, 100*time.Millisecond
	t.Cleanup(func() { preloadLead, pollEvery = oldLead, oldPoll })

	dir := t.TempDir()
	xa, beatsA := kickTrack(bpmA, firstA, 50, 1.2)
	xb, beatsB := kickTrack(bpmB, firstB, 50, 1.2)
	paths := map[string]string{"a": filepath.Join(dir, "a.wav"), "b": filepath.Join(dir, "b.wav")}
	for name, x := range map[string][]float32{"a": xa, "b": xb} {
		if err := autodj.WriteWAV(paths[name], x); err != nil {
			t.Fatal(err)
		}
	}
	songs := map[string]autodj.Song{}
	for name, x := range map[string][]float32{"a": xa, "b": xb} {
		res, err := analyze.Analyze(x)
		if err != nil {
			t.Fatal(err)
		}
		songs[paths[name]] = autodj.Song{Length: float64(len(x)) / analyze.SampleRate, Edges: res.Edges}
	}
	{ // how far the analysis is from the kicks the tracks really have
		ea, eb := songs[paths["a"]].Edges, songs[paths["b"]].Edges
		lastA := beatsA[len(beatsA)-1]
		t.Logf("analysis: a's end anchor %.1f ms off the last kick (period %.4f, true %.4f), b's start anchor %.1f ms off the first (period %.4f, true %.4f)",
			(ea.End.Anchor-lastA)*1000, ea.End.Period, 60/bpmA, (eb.Start.Anchor-firstB)*1000, eb.Start.Period, 60/bpmB)
	}
	plan := autodj.For(songs[paths["a"]], songs[paths["b"]])
	if plan.Kind != autodj.BeatMatch {
		t.Fatalf("plan = %+v (%s), want a beat-match", plan, plan.Why)
	}
	if !strings.Contains(plan.Why, wantWhy) {
		t.Fatalf("plan says %q, want it to mention %q", plan.Why, wantWhy)
	}
	t.Logf("plan: %s; the next song starts at %.2f s of the playing one, from %.2f s, for %.2f s", plan.Why, plan.AStart, plan.BFrom, plan.Overlap)

	p, err := New(context.Background(), realDeck)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	p.SetAutoDJ(true, func(url string) (autodj.Song, bool) { s, ok := songs[url]; return s, ok })
	if err := p.QueueMany(bg, []string{paths["a"], paths["b"]}); err != nil {
		t.Fatal(err)
	}
	for i := 0; ; i++ { // wait until a plays, then jump to shortly before the transition
		if e, _, err := p.Position(bg); err == nil && e > 0.1 {
			break
		}
		if i > 500 {
			t.Fatal("a never played")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := p.SeekPercent(bg, int(100*(plan.AStart-4)/50)); err != nil {
		t.Fatal(err)
	}

	// Sample both decks' position clocks until the queue has moved on to b.
	epoch := time.Now()
	var sa, sb []posSample
	var speeds []float64 // the speed of b while both sounded
	deadline := time.After(40 * time.Second)
	handedOver := false
	for !handedOver {
		select {
		case <-deadline:
			t.Fatal("the transition did not finish in 40 s")
		case ev := <-p.Events():
			handedOver = ev.Property == "playlist-pos" && string(ev.Data) == "1"
		case <-time.After(5 * time.Millisecond):
		}
		p.mu.Lock()
		da, db := p.decks[0], p.decks[1]
		if p.trans != nil && p.trans.started {
			speeds = append(speeds, p.speed[1])
		}
		p.mu.Unlock()
		if e, err := da.GetFloat(bg, "time-pos"); err == nil && e > plan.AStart-3 {
			sa = append(sa, posSample{time.Now(), e})
		}
		if db != nil && !handedOver {
			if e, err := db.GetFloat(bg, "time-pos"); err == nil && e > plan.BFrom+0.1 {
				sb = append(sb, posSample{time.Now(), e})
			}
		}
	}
	if len(sa) < 50 || len(sb) < 50 {
		t.Fatalf("too few position readings: %d from a, %d from b", len(sa), len(sb))
	}
	// When did each deck's position reach a given point? A deck's speed may
	// have been steered, so no constant speed is assumed: around the moment
	// the reported position reaches the target, take the local slope of the
	// readings and the offset of the freshest one (a reading is never ahead of
	// the truth), and solve for the time.
	crossing := func(samples []posSample, target float64) (float64, bool) {
		first := -1
		for i, s := range samples {
			if s.pos >= target {
				first = i
				break
			}
		}
		if first < 0 {
			return 0, false
		}
		centre := samples[first].wall
		var win []posSample
		for _, s := range samples {
			if d := s.wall.Sub(centre).Seconds(); d > -0.4 && d < 0.4 {
				win = append(win, s)
			}
		}
		if len(win) < 8 {
			return 0, false
		}
		var sw, sp, sww, swp float64
		for _, s := range win {
			w := s.wall.Sub(epoch).Seconds()
			sw, sp, sww, swp = sw+w, sp+s.pos, sww+w*w, swp+w*s.pos
		}
		n := float64(len(win))
		slope := (n*swp - sw*sp) / (n*sww - sw*sw)
		if slope < 0.5 || slope > 1.5 {
			return 0, false
		}
		offset := math.Inf(-1)
		for _, s := range win {
			offset = math.Max(offset, s.pos-slope*s.wall.Sub(epoch).Seconds())
		}
		return (target - offset) / slope, true
	}
	worst, sum, n := 0.0, 0.0, 0
	var errs, since []float64 // each overlapping kick's error, and when it sounds relative to the overlap's start
	clockStart, _ := crossing(sa, plan.AStart)
	for _, tb := range beatsB {
		if tb < plan.BFrom+0.5 {
			continue
		}
		wallB, ok := crossing(sb, tb)
		if !ok {
			continue
		}
		best := math.Inf(1)
		for _, ta := range beatsA {
			if wallA, ok := crossing(sa, ta); ok {
				if d := wallB - wallA; math.Abs(d) < math.Abs(best) {
					best = d
				}
			}
		}
		if math.Abs(best) < 0.25 { // a kick of a really is near
			worst, sum, n = math.Max(worst, math.Abs(best)), sum+best, n+1
			errs = append(errs, best)
			since = append(since, wallB-clockStart)
		}
	}
	if len(speeds) > 0 {
		lo, hi := speeds[0], speeds[0]
		for _, v := range speeds {
			lo, hi = math.Min(lo, v), math.Max(hi, v)
		}
		t.Logf("speed of b during the overlap: from %.4f to %.4f (planned %.4f)", lo, hi, plan.BSpeed)
	}
	for i := range errs {
		t.Logf("  kick %2d at +%.1f s of the overlap: b %+6.1f ms", i+1, since[i], errs[i]*1000)
	}
	t.Logf("%d kicks of b overlap kicks of a: worst miss %.1f ms, mean signed offset %+.1f ms (b late is positive)", n, worst*1000, sum/float64(n)*1000)
	if n < c.minKicks {
		t.Errorf("only %d of b's kicks could be compared with a's, want %d", n, c.minKicks)
	}
	if worst > c.maxWorst {
		t.Errorf("a kick of b misses a kick of a by %.0f ms, want within %.0f ms", worst*1000, c.maxWorst*1000)
	}
	if mean := sum / float64(n); math.Abs(mean) > c.maxMean {
		t.Errorf("b is %+.1f ms from a on average, want within %.0f ms", mean*1000, c.maxMean*1000)
	}
}
