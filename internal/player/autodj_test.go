package player

import (
	"math"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PatrickSteil/ytplay/internal/analyze"
	"github.com/PatrickSteil/ytplay/internal/autodj"
)

func init() { rampEvery = 3 * time.Millisecond }

// set changes what a fake deck reports; safe while the player is running.
func (d *fakeDeck) set(prop string, v float64) {
	d.mu.Lock()
	d.props[prop] = v
	d.mu.Unlock()
}

// snapshot returns the commands received so far without forgetting them.
func (d *fakeDeck) snapshot() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.calls...)
}

// waitCalls waits until pred holds for the commands a deck has received.
func waitCalls(t *testing.T, what string, d *fakeDeck, pred func(calls []string) bool) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		calls := d.snapshot()
		if pred(calls) {
			return calls
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: timed out; commands so far: %q", what, calls)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func has(calls []string, want string) bool {
	for _, c := range calls {
		if c == want {
			return true
		}
	}
	return false
}

// lastVolume is the volume a deck was last told to have (-1 if never).
func lastVolume(calls []string) float64 {
	v := -1.0
	for _, c := range calls {
		if rest, ok := strings.CutPrefix(c, "set_property volume "); ok {
			v, _ = strconv.ParseFloat(rest, 64)
		}
	}
	return v
}

func countCalls(calls []string, prefix string) int {
	n := 0
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// mpv's volume is cubic: gain = (volume/100)^3.
func gainOf(volume float64) float64 { return math.Pow(volume/100, 3) }

// armed returns a player with autodj on (the given analyses, nil for none) and
// songs a, b, c queued. Deck a plays a 200 s song at 190 s; deck b has b loaded
// (180 s long), prepared, and the plan is a crossfade starting at 194 s unless
// the analyses say more.
func armed(t *testing.T, songs Analyses) (p *Player, a, b *fakeDeck) {
	t.Helper()
	p, a, b = newTestPlayer2(t)
	p.SetAutoDJ(true, songs)
	if err := p.QueueMany(bg, []string{"a", "b", "c"}); err != nil {
		t.Fatal(err)
	}
	a.set("duration", 200)
	a.set("time-pos", 190)
	p.poll(bg) // preloads b
	b.set("duration", 180)
	p.poll(bg) // prepares it
	drain(p)
	return p, a, b
}

func TestAutoDJPreparesTheNextSongSilentlyAtItsStartingPoint(t *testing.T) {
	_, _, b := armed(t, nil)
	calls := b.snapshot()
	if lastVolume(calls) != 0 {
		t.Errorf("the next song must be silent until it starts: %q", calls)
	}
	if countCalls(calls, "seek") != 0 {
		t.Errorf("a crossfade starts the next song from its beginning: %q", calls)
	}
	if has(calls, "set_property pause false") {
		t.Errorf("it must not start yet: %q", calls)
	}
}

func TestAutoDJSeeksTheNextSongToWhereItsBeatStarts(t *testing.T) {
	// B's analysis says the music starts 1.3 s in.
	songs := func(url string) (autodj.Song, bool) {
		if url == "b" {
			return autodj.Song{Length: 180, Edges: analyze.Edges{AudioStart: 1.3, AudioEnd: 178}}, true
		}
		return autodj.Song{}, false
	}
	_, _, b := armed(t, songs)
	if !has(b.snapshot(), "seek 1.3 absolute exact") {
		t.Errorf("b should be seeked to 1.3 s: %q", b.snapshot())
	}
}

func TestCrossfadeStartsOnlyWhenTheEndIsNear(t *testing.T) {
	p, a, b := armed(t, nil)
	a.set("time-pos", 185) // 9 s before the start of the crossfade: too early
	p.poll(bg)
	time.Sleep(40 * time.Millisecond)
	if has(b.snapshot(), "set_property pause false") {
		t.Fatal("b started far too early")
	}
	a.set("time-pos", 193.0) // within the arming window, but before 194 s
	p.poll(bg)
	time.Sleep(40 * time.Millisecond)
	if has(b.snapshot(), "set_property pause false") {
		t.Fatal("b started before the playing song reached 194 s")
	}
	a.set("time-pos", 194.02)
	waitCalls(t, "b starts", b, func(c []string) bool { return has(c, "set_property pause false") })
}

// walk moves a deck's reported position from one point to another in steps
// small enough for the transition loop to see each one, as a clock would. (A
// jump of more than a second past the start counts as a late start.)
func walk(d *fakeDeck, from, to float64) {
	for x := from; x < to; x += 0.4 {
		d.set("time-pos", x)
		time.Sleep(10 * time.Millisecond)
	}
	d.set("time-pos", to)
}

func TestVolumesFollowAnEqualPowerCurve(t *testing.T) {
	p, a, b := armed(t, nil)
	a.set("time-pos", 193.2)
	p.poll(bg)
	walk(a, 193.2, 197) // to halfway through the 6 s overlap
	want := math.Sqrt(0.5)
	waitCalls(t, "volumes at the midpoint", b, func(c []string) bool { return math.Abs(gainOf(lastVolume(c))-want) < 0.01 })
	waitCalls(t, "volumes at the midpoint", a, func(c []string) bool { return math.Abs(gainOf(lastVolume(c))-want) < 0.01 })
	a.set("time-pos", 194.6) // 10% in
	waitCalls(t, "b early in the fade", b, func(c []string) bool { return math.Abs(gainOf(lastVolume(c))-math.Sin(math.Pi/2*0.1)) < 0.01 })
	waitCalls(t, "a early in the fade", a, func(c []string) bool { return math.Abs(gainOf(lastVolume(c))-math.Cos(math.Pi/2*0.1)) < 0.01 })
}

func TestTheNextSongTakesOverWhenTheOverlapIsOver(t *testing.T) {
	p, a, b := armed(t, nil)
	a.set("time-pos", 193.2)
	p.poll(bg)
	a.set("time-pos", 197)
	waitCalls(t, "started", b, func(c []string) bool { return has(c, "set_property pause false") })
	drain(p)
	a.set("time-pos", 200.05)
	if got := waitEvents(t, p, 3); got[1] != "playlist-pos=1" {
		t.Fatalf("events = %q, want the queue to move on to b", got)
	}
	waitCalls(t, "b at full volume", b, func(c []string) bool { return lastVolume(c) == 100 })
	calls := waitCalls(t, "a stopped and restored", a, func(c []string) bool { return has(c, "stop") && lastVolume(c) == 100 })
	if countCalls(calls, "loadfile") != 1 {
		t.Errorf("a should only have loaded its own song: %q", calls)
	}
	p.mu.Lock()
	active := p.active
	p.mu.Unlock()
	if active != 1 {
		t.Errorf("active deck = %d, want 1", active)
	}
	// and the song after it is preloaded on the deck that became free
	b.set("duration", 150)
	b.set("time-pos", 140)
	p.poll(bg)
	if !has(a.snapshot(), "loadfile c replace") {
		t.Errorf("c should be preloaded on deck a: %q", a.snapshot())
	}
}

func TestSkippingDuringATransitionJustSwitches(t *testing.T) {
	p, a, b := armed(t, nil)
	a.set("time-pos", 193.2)
	p.poll(bg)
	a.set("time-pos", 196)
	waitCalls(t, "started", b, func(c []string) bool { return has(c, "set_property pause false") })
	drain(p)
	if err := p.Skip(bg); err != nil {
		t.Fatal(err)
	}
	eq(t, "events", drain(p), state("a,b*,c", 1))
	waitCalls(t, "b at full volume", b, func(c []string) bool { return lastVolume(c) == 100 })
	waitCalls(t, "the old deck's volume restored for its next use", a, func(c []string) bool { return has(c, "stop") && lastVolume(c) == 100 })
	if countCalls(a.snapshot(), "loadfile") != 1 {
		t.Errorf("b must not be loaded again on a: %q", a.snapshot())
	}
	before := len(b.snapshot()) + len(a.snapshot())
	a.set("time-pos", 197)
	time.Sleep(40 * time.Millisecond)
	if after := len(b.snapshot()) + len(a.snapshot()); after != before {
		t.Errorf("the old ramp is still sending volumes after the skip (%d commands more)", after-before)
	}
}

func TestPausingDuringATransitionPausesBothDecks(t *testing.T) {
	p, a, b := armed(t, nil)
	a.set("time-pos", 193.2)
	p.poll(bg)
	a.set("time-pos", 196)
	waitCalls(t, "started", b, func(c []string) bool { return has(c, "set_property pause false") })
	a.events <- pauseEvent(true)
	waitCalls(t, "b paused", b, func(c []string) bool { return has(c[len(c)-1:], "set_property pause true") })
	a.events <- pauseEvent(false)
	waitCalls(t, "b resumed", b, func(c []string) bool { return has(c[len(c)-1:], "set_property pause false") })
}

func TestChangingTheNextSongDuringATransitionCancelsIt(t *testing.T) {
	p, a, b := armed(t, nil)
	a.set("time-pos", 193.2)
	p.poll(bg)
	a.set("time-pos", 196)
	waitCalls(t, "started", b, func(c []string) bool { return has(c, "set_property pause false") })
	if err := p.Remove(bg, 1); err != nil { // the song that is fading in goes
		t.Fatal(err)
	}
	waitCalls(t, "b stopped", b, func(c []string) bool { return has(c, "stop") })
	waitCalls(t, "a back at full volume", a, func(c []string) bool { return lastVolume(c) == 100 })
	// it must stay that way: no ramp is left running
	a.set("time-pos", 198)
	time.Sleep(40 * time.Millisecond)
	if v := lastVolume(a.snapshot()); v != 100 {
		t.Errorf("a's volume = %v after the cancel, a ramp is still running", v)
	}
}

func TestSeekingBackDuringATransitionCancelsIt(t *testing.T) {
	p, a, b := armed(t, nil)
	a.set("time-pos", 193.2)
	p.poll(bg)
	a.set("time-pos", 196)
	waitCalls(t, "started", b, func(c []string) bool { return has(c, "set_property pause false") })
	a.set("time-pos", 120) // the user jumped back
	waitCalls(t, "b stopped", b, func(c []string) bool { return has(c, "stop") })
	waitCalls(t, "a back at full volume", a, func(c []string) bool { return lastVolume(c) == 100 })
}

func TestAutoDJOffNeverTouchesTheVolume(t *testing.T) {
	p, a, b := newTestPlayer2(t)
	_ = p.QueueMany(bg, []string{"a", "b", "c"})
	a.set("duration", 200)
	a.set("time-pos", 190)
	p.poll(bg)
	b.set("duration", 180)
	a.set("time-pos", 198)
	p.poll(bg)
	time.Sleep(30 * time.Millisecond)
	if countCalls(a.snapshot(), "set_property volume") != 0 || countCalls(b.snapshot(), "set_property volume") != 0 {
		t.Errorf("autodj is off but volumes were set: a %q, b %q", a.snapshot(), b.snapshot())
	}
	if has(b.snapshot(), "set_property pause false") {
		t.Error("b started before the playing song ended")
	}
}

func TestTurningAutoDJOffMidTransitionRestoresTheVolume(t *testing.T) {
	p, a, b := armed(t, nil)
	a.set("time-pos", 193.2)
	p.poll(bg)
	a.set("time-pos", 196)
	waitCalls(t, "started", b, func(c []string) bool { return has(c, "set_property pause false") })
	p.SetAutoDJ(false, nil)
	waitCalls(t, "b stopped", b, func(c []string) bool { return has(c, "stop") })
	waitCalls(t, "a back at full volume", a, func(c []string) bool { return lastVolume(c) == 100 })
}

func TestTooLateForATransitionLeavesItToThePlainHandoff(t *testing.T) {
	p, a, b := armed(t, nil)
	a.set("time-pos", 199.7) // the preload was only ready now: 0.3 s left
	p.poll(bg)
	time.Sleep(40 * time.Millisecond)
	if has(b.snapshot(), "set_property pause false") {
		t.Error("a transition was started with 0.3 s to go")
	}
	a.endFile("eof", 1)
	if got := waitEvents(t, p, 3); got[1] != "playlist-pos=1" {
		t.Errorf("events = %q, want the plain handoff to b", got)
	}
}

func TestVolumeForGain(t *testing.T) {
	for gain, want := range map[float64]float64{0: 0, 1: 100, 0.125: 50, 0.001: 10, -1: 0, 2: 100} {
		if got := volumeFor(gain); math.Abs(got-want) > 1e-6 {
			t.Errorf("volumeFor(%v) = %v, want %v", gain, got, want)
		}
	}
}

// ---- beat-matched transitions ----

func sureSong(length, audioEnd, period, startAnchor, endAnchor float64) autodj.Song {
	return autodj.Song{Length: length, Edges: analyze.Edges{
		AudioStart: 0.1, AudioEnd: audioEnd,
		Start: analyze.BeatGrid{Period: period, Anchor: startAnchor, Sure: true},
		End:   analyze.BeatGrid{Period: period, Anchor: endAnchor, Sure: true},
	}}
}

// beatSongs knows a (period 0.5 s, its beat ends at 19.5 s of 20) and b (period
// 0.48 s, its beat starts at 0.3 s). The plan: b starts when a is at
// 19.5 - 16*0.5 = 11.5 s, at speed 0.96.
func beatSongs(url string) (autodj.Song, bool) {
	switch url {
	case "a":
		return sureSong(20, 19.8, 0.5, 0.4, 19.5), true
	case "b":
		return sureSong(180, 170, 0.48, 0.3, 169.8), true
	}
	return autodj.Song{}, false
}

// clockedPlayer is a player whose deck a reports a position that moves in real
// time: from 11 s on, in steps of quant seconds, shifted by shift seconds. The
// plan is a beat-match that starts b when a is at 11.5 s.
func clockedPlayer(t *testing.T, quant float64, shift time.Duration) (p *Player, a, b *fakeDeck, t0 time.Time) {
	t.Helper()
	p, a, b = newTestPlayer2(t)
	t0 = time.Now().Add(shift)
	a.posFn = func() float64 {
		x := 11 + time.Since(t0).Seconds()
		return math.Floor(x/quant) * quant
	}
	p.SetAutoDJ(true, beatSongs)
	if err := p.QueueMany(bg, []string{"a", "b", "c"}); err != nil {
		t.Fatal(err)
	}
	a.set("duration", 20)
	p.poll(bg) // preload
	b.set("duration", 180)
	p.poll(bg) // prepare (and arm: a is 0.5 s before the start)
	return p, a, b, t0
}

func TestBeatMatchSetsTheSpeedBeforeStarting(t *testing.T) {
	_, _, b, _ := clockedPlayer(t, 0.05, 0)
	calls := waitCalls(t, "b prepared", b, func(c []string) bool { return has(c, "set_property volume 0") })
	if !has(calls, "set_property speed 0.96") {
		t.Fatalf("b should be set to speed 0.96: %q", calls)
	}
	if has(calls, "set_property pause false") {
		t.Errorf("b must not have started yet: %q", calls)
	}
	if !has(calls, "seek 0.3 absolute exact") {
		t.Errorf("b should be at where its beat starts: %q", calls)
	}
}

func TestBeatMatchStartsEarlyByTheStartLatency(t *testing.T) {
	old := startLatency
	startLatency = 40 * time.Millisecond
	t.Cleanup(func() { startLatency = old })
	// However the position clock is stepped and shifted, b is told to start
	// when a is at 11.5 s less the latency, to within a few milliseconds.
	for name, tc := range map[string]struct {
		quant float64
		shift time.Duration
	}{
		"fine steps":          {0.001, 0},
		"50 ms steps":         {0.05, 0},
		"50 ms steps, 17 ms":  {0.05, 17 * time.Millisecond},
		"50 ms steps, 33 ms":  {0.05, 33 * time.Millisecond},
		"100 ms steps, 61 ms": {0.1, 61 * time.Millisecond},
	} {
		t.Run(name, func(t *testing.T) {
			p, _, b, t0 := clockedPlayer(t, tc.quant, tc.shift)
			var at time.Time
			deadline := time.Now().Add(4 * time.Second)
			for nextPoll := time.Now(); time.Now().Before(deadline); {
				if ts, ok := b.timeOf("set_property pause false"); ok {
					at = ts
					break
				}
				if time.Now().After(nextPoll) { // the player polls every so often
					p.poll(bg)
					nextPoll = time.Now().Add(20 * time.Millisecond)
				}
				time.Sleep(time.Millisecond)
			}
			if at.IsZero() {
				t.Fatalf("b never started: %q", b.snapshot())
			}
			want := t0.Add(500*time.Millisecond - startLatency)
			if d := at.Sub(want); d < -15*time.Millisecond || d > 15*time.Millisecond {
				t.Errorf("b started %v from the planned moment, want within 15 ms", d)
			}
		})
	}
}

func TestClockOffsetUsesTheFreshestReading(t *testing.T) {
	// A position that moves in 50 ms steps, read every 7 ms: the readings are
	// up to 50 ms stale, never early. The estimate must be within the spacing
	// of the readings, not within the step.
	epoch := time.Now()
	w0 := 0.3 // seconds after epoch at which the song's position was 0
	var samples []posSample
	for w := 1.0; w < 2.0; w += 0.007 {
		pos := math.Floor((w-w0)/0.05) * 0.05
		samples = append(samples, posSample{wall: epoch.Add(time.Duration(w * float64(time.Second))), pos: pos})
	}
	if got := clockOffset(samples, epoch); math.Abs(got-w0) > 0.008 {
		t.Errorf("clock offset = %.4f s, want %.3f within 8 ms", got, w0)
	}
}

func TestSpeedEasesBackToOneAfterTheOverlap(t *testing.T) {
	old := easeScale
	easeScale = 0.02 // 8 s of easing in 160 ms
	t.Cleanup(func() { easeScale = old })
	p, a, b, _ := clockedPlayer(t, 0.05, 0)
	waitCalls(t, "b started", b, func(c []string) bool { return has(c, "set_property pause false") })
	a.setPosFn(nil)
	a.set("time-pos", 11.5+8.3+0.1) // past the end of the overlap
	waitEvents(t, p, 3)
	calls := waitCalls(t, "speed back at 1", b, func(c []string) bool {
		for i := len(c) - 1; i >= 0; i-- {
			if strings.HasPrefix(c[i], "set_property speed ") {
				return c[i] == "set_property speed 1"
			}
		}
		return false
	})
	// in several steps, and without overshooting
	var speeds []float64
	for _, c := range calls {
		if rest, ok := strings.CutPrefix(c, "set_property speed "); ok {
			v, _ := strconv.ParseFloat(rest, 64)
			speeds = append(speeds, v)
		}
	}
	if len(speeds) < 4 {
		t.Fatalf("speed commands = %v, want a gradual return to 1", speeds)
	}
	for i := 1; i < len(speeds); i++ {
		if speeds[i] < speeds[i-1]-1e-9 || speeds[i] > 1+1e-9 {
			t.Errorf("speeds %v should rise monotonically from 0.96 to 1", speeds)
			break
		}
	}
}

func TestSkippingDuringABeatMatchEasesTheSpeedBack(t *testing.T) {
	old := easeScale
	easeScale = 0.02
	t.Cleanup(func() { easeScale = old })
	p, _, b, _ := clockedPlayer(t, 0.05, 0)
	waitCalls(t, "b started", b, func(c []string) bool { return has(c, "set_property pause false") })
	if err := p.Skip(bg); err != nil {
		t.Fatal(err)
	}
	waitCalls(t, "speed back at 1", b, func(c []string) bool { return has(c, "set_property speed 1") })
}

func TestDroppingTheNextSongPutsItsSpeedBack(t *testing.T) {
	p, _, b, _ := clockedPlayer(t, 0.05, 0)
	waitCalls(t, "prepared", b, func(c []string) bool { return has(c, "set_property speed 0.96") })
	p.SetAutoDJ(false, nil)
	calls := waitCalls(t, "dropped", b, func(c []string) bool { return has(c, "stop") })
	if !has(calls, "set_property speed 1") {
		t.Errorf("the idle deck must go back to normal speed: %q", calls)
	}
}

func TestAPreloadOnADeckThatWasLeftAtAnotherSpeedStartsAtNormalSpeed(t *testing.T) {
	p, a, b := newTestPlayer2(t)
	p.mu.Lock()
	p.speed[1] = 0.97 // an earlier easing was cut short
	p.mu.Unlock()
	_ = p.QueueMany(bg, []string{"a", "b"})
	a.set("duration", 200)
	a.set("time-pos", 190)
	p.poll(bg)
	if !has(b.snapshot(), "set_property speed 1") {
		t.Errorf("b should be put back to normal speed before it loads the next song: %q", b.snapshot())
	}
}

func TestACrossfadeNeverChangesTheSpeed(t *testing.T) {
	p, a, b := armed(t, nil)
	a.set("time-pos", 193.2)
	p.poll(bg)
	a.set("time-pos", 200.05)
	waitEvents(t, p, 3)
	if n := countCalls(a.snapshot(), "set_property speed") + countCalls(b.snapshot(), "set_property speed"); n != 0 {
		t.Errorf("a crossfade changed the speed %d times", n)
	}
}

func TestSkippingBeforeABeatMatchStartsPlaysTheNextSongAtNormalSpeed(t *testing.T) {
	p, _, b, _ := clockedPlayer(t, 0.05, 0)
	waitCalls(t, "prepared", b, func(c []string) bool { return has(c, "set_property speed 0.96") })
	p.mu.Lock()
	p.trans.running = true // pretend the ramp loop has not started it yet: it is not audible
	p.mu.Unlock()
	if err := p.Skip(bg); err != nil {
		t.Fatal(err)
	}
	calls := waitCalls(t, "normal speed", b, func(c []string) bool { return lastSpeed(c) == 1 })
	if lastVolume(calls) != 100 {
		t.Errorf("and full volume: %q", calls)
	}
}

func TestBeatMatchStillStartsWhenTheStartMomentWasMissed(t *testing.T) {
	// The position jumps over AStart before enough readings were taken to time
	// the start: the next song is started at once rather than never.
	p, a, b := newTestPlayer2(t)
	p.SetAutoDJ(true, beatSongs)
	_ = p.QueueMany(bg, []string{"a", "b", "c"})
	a.set("duration", 20)
	a.set("time-pos", 11.4)
	p.poll(bg)
	b.set("duration", 180)
	p.poll(bg)
	a.set("time-pos", 11.7)
	waitCalls(t, "b started", b, func(c []string) bool { return has(c, "set_property pause false") })
}

// lastSpeed is the speed a deck was last told to have (0 if never).
func lastSpeed(calls []string) float64 {
	v := 0.0
	for _, c := range calls {
		if rest, ok := strings.CutPrefix(c, "set_property speed "); ok {
			v, _ = strconv.ParseFloat(rest, 64)
		}
	}
	return v
}

// ---- phase correction ----

// simDeck models the position of a deck that is started by an order and then
// really starts a little later (or earlier than assumed) and plays at whatever
// speed it was last told to: what a real deck does, with its position reported
// in steps.
type simDeck struct {
	mu       sync.Mutex
	pos0     float64       // where it was seeked to
	speed    float64       // the speed it was last told to play at
	delay    time.Duration // from the order to start until it really starts
	startAt  time.Time     // when it really starts (zero: not ordered yet)
	lastWall time.Time     // the integration state
	lastPos  float64
	quant    float64
	jitter   float64   // seconds: every reading is off by up to this much, either way
	noise    uint32    // for the jitter
	speeds   []float64 // every speed it was told, in order
}

func (s *simDeck) onCommand(cmd string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	switch {
	case strings.HasPrefix(cmd, "seek "):
		f := strings.Fields(cmd)
		s.pos0, _ = strconv.ParseFloat(f[1], 64)
	case strings.HasPrefix(cmd, "set_property speed "):
		v, _ := strconv.ParseFloat(strings.TrimPrefix(cmd, "set_property speed "), 64)
		if !s.startAt.IsZero() && now.After(s.startAt) {
			s.lastPos, s.lastWall = s.exactAt(now), now
		}
		s.speed = v
		s.speeds = append(s.speeds, v)
	case cmd == "set_property pause false" && s.startAt.IsZero():
		s.startAt = now.Add(s.delay)
		s.lastWall, s.lastPos = s.startAt, s.pos0
	}
}

func (s *simDeck) exactAt(now time.Time) float64 {
	if s.startAt.IsZero() || now.Before(s.startAt) {
		return s.pos0
	}
	return s.lastPos + s.speed*now.Sub(s.lastWall).Seconds()
}

func (s *simDeck) exact() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exactAt(time.Now())
}

func (s *simDeck) reported() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	pos := math.Floor(s.exactAt(time.Now())/s.quant) * s.quant
	if s.jitter > 0 {
		s.noise = s.noise*1664525 + 1013904223
		pos += s.jitter * float64(int32(s.noise)) / float64(1<<31)
	}
	return pos
}

// phasePlayer is clockedPlayer with a simulated deck b whose start is off by
// `off` from what the player assumes. Phase correction is made quick for tests.
func phasePlayer(t *testing.T, off time.Duration, tau, settle float64, tweaks ...func()) (p *Player, b *simDeck, t0 time.Time) {
	t.Helper()
	oldLat, oldTau, oldSettle, oldBand, oldEvery := startLatency, phaseTau, phaseSettle, phaseDeadband, phaseEvery
	startLatency, phaseTau, phaseSettle, phaseDeadband, phaseEvery = 50*time.Millisecond, tau, settle, 0.003, 20*time.Millisecond
	t.Cleanup(func() {
		startLatency, phaseTau, phaseSettle, phaseDeadband, phaseEvery = oldLat, oldTau, oldSettle, oldBand, oldEvery
	})
	for _, tweak := range tweaks { // package settings, before the player that reads them exists
		tweak()
	}
	var fb *fakeDeck
	p, _, fb, t0 = clockedPlayer(t, 0.05, 0)
	// The commands the player sent before the hook existed (seek, volume, speed)
	// are the sim's starting state.
	sim := &simDeck{pos0: 0.3, speed: 0.96, delay: startLatency + off, quant: 0.05}
	fb.mu.Lock()
	fb.posFn, fb.onCmd = sim.reported, sim.onCommand
	fb.mu.Unlock()
	return p, sim, t0
}

// deltaAt is how far ahead of the beat-matched position (in seconds of the
// next song) the simulated deck is, at a time after the planned start.
func (s *simDeck) deltaAt(t0 time.Time, afterStart time.Duration) float64 {
	w := t0.Add(500*time.Millisecond + afterStart) // a is at 11.5 s at t0 + 0.5 s
	s.mu.Lock()
	defer s.mu.Unlock()
	ref := 0.3 + 0.96*afterStart.Seconds()
	return s.exactAt(w) - ref
}

func TestAStartThatIsLateIsPulledIntoPhase(t *testing.T) {
	for name, off := range map[string]time.Duration{
		"40 ms late":  40 * time.Millisecond,
		"25 ms early": -25 * time.Millisecond,
		"on time":     0,
	} {
		t.Run(name, func(t *testing.T) {
			p, sim, t0 := phasePlayer(t, off, 0.25, 0.1)
			deadline := time.Now().Add(4 * time.Second)
			for nextPoll := time.Now(); time.Now().Before(deadline); {
				if time.Since(t0) > 500*time.Millisecond+1500*time.Millisecond {
					break
				}
				if time.Now().After(nextPoll) {
					p.poll(bg)
					nextPoll = time.Now().Add(20 * time.Millisecond)
				}
				time.Sleep(time.Millisecond)
			}
			// 1.5 s after the planned start the position must be on the beat-matched track.
			d := sim.deltaAt(t0, 1500*time.Millisecond)
			if math.Abs(d) > 0.008 {
				t.Errorf("the next song is %+.1f ms from the beat-matched position after 1.5 s, want within 8 ms", d*1000)
			}
		})
	}
}

func TestPhaseCorrectionStaysWithinAFewPercent(t *testing.T) {
	p, sim, t0 := phasePlayer(t, 400*time.Millisecond, 0.25, 0.1) // wildly late
	deadline := time.Now().Add(3 * time.Second)
	for nextPoll := time.Now(); time.Now().Before(deadline) && time.Since(t0) < 2000*time.Millisecond; {
		if time.Now().After(nextPoll) {
			p.poll(bg)
			nextPoll = time.Now().Add(20 * time.Millisecond)
		}
		time.Sleep(time.Millisecond)
	}
	sim.mu.Lock()
	defer sim.mu.Unlock()
	if len(sim.speeds) == 0 {
		t.Fatal("no correction was made for a start 400 ms late")
	}
	for _, v := range sim.speeds {
		if v < 0.96*0.97-1e-9 || v > 0.96*1.03+1e-9 {
			t.Errorf("speed %v is more than 3%% from the beat-matched 0.96", v)
		}
	}
}

func TestAStartOnTimeIsLeftAlone(t *testing.T) {
	// With the gain the player really uses: the few milliseconds of jitter any
	// start has must not turn into audible changes of speed.
	p, sim, t0 := phasePlayer(t, 0, 2.0, 0.1)
	deadline := time.Now().Add(3 * time.Second)
	for nextPoll := time.Now(); time.Now().Before(deadline) && time.Since(t0) < 1800*time.Millisecond; {
		if time.Now().After(nextPoll) {
			p.poll(bg)
			nextPoll = time.Now().Add(20 * time.Millisecond)
		}
		time.Sleep(time.Millisecond)
	}
	sim.mu.Lock()
	defer sim.mu.Unlock()
	for _, v := range sim.speeds {
		if math.Abs(v-0.96) > 0.96*0.01 {
			t.Errorf("a start that was on time was corrected to speed %v (more than 1%% from 0.96)", v)
		}
	}
}

func TestNothingIsCorrectedBeforeTheNextSongHasSettled(t *testing.T) {
	p, sim, t0 := phasePlayer(t, 40*time.Millisecond, 0.25, 0.6)
	deadline := time.Now().Add(3 * time.Second)
	for nextPoll := time.Now(); time.Now().Before(deadline) && time.Since(t0) < 500*time.Millisecond+450*time.Millisecond; {
		if time.Now().After(nextPoll) {
			p.poll(bg)
			nextPoll = time.Now().Add(20 * time.Millisecond)
		}
		time.Sleep(time.Millisecond)
	}
	sim.mu.Lock()
	defer sim.mu.Unlock()
	if len(sim.speeds) != 0 {
		t.Errorf("speed was changed to %v within 0.45 s of the start, before the 0.6 s settling time was over", sim.speeds)
	}
}

// ---- found by running the real thing ----

func TestAutoDJPreloadsEarlyEnoughToPrepareTheTransition(t *testing.T) {
	// A beat-matched transition starts about 8 s before the end, and a stream
	// takes seconds to resolve: at 15 s before the end it is too late to be ready.
	for name, tc := range map[string]struct {
		on   bool
		want bool
	}{"autodj on": {true, true}, "autodj off": {false, false}} {
		t.Run(name, func(t *testing.T) {
			p, a, b := newTestPlayer2(t)
			if tc.on {
				p.SetAutoDJ(true, nil)
			}
			_ = p.QueueMany(bg, []string{"a", "b"})
			a.set("duration", 200)
			a.set("time-pos", 172) // 28 s before the end
			p.poll(bg)
			if got := has(b.snapshot(), "loadfile b replace"); got != tc.want {
				t.Errorf("next song loaded at 28 s before the end: %v, want %v (%q)", got, tc.want, b.snapshot())
			}
		})
	}
}

func TestALateBeatMatchBecomesACrossfadeForTheTimeThatIsLeft(t *testing.T) {
	p, a, b := newTestPlayer2(t)
	p.SetAutoDJ(true, beatSongs) // plan: b starts when a is at 11.5 s, at speed 0.96
	_ = p.QueueMany(bg, []string{"a", "b", "c"})
	a.set("duration", 20)
	a.set("time-pos", 10)
	p.poll(bg) // preloads b
	b.set("duration", 180)
	p.poll(bg) // prepares it: speed 0.96, at 0.3 s
	if !has(b.snapshot(), "set_property speed 0.96") {
		t.Fatalf("b should have been prepared for the beat-match: %q", b.snapshot())
	}
	a.set("time-pos", 14) // but only now did the playing song get close enough to be armed: 2.5 s late
	p.poll(bg)
	calls := waitCalls(t, "b started", b, func(c []string) bool { return has(c, "set_property pause false") })
	if lastSpeed(calls) != 1 {
		t.Errorf("a crossfade plays the next song at normal speed, but its speed was left at %v: %q", lastSpeed(calls), calls)
	}
	// the crossfade covers what is left: from 14 s to the end of the overlap at 19.8 s
	a.set("time-pos", 16.9) // half way
	want := math.Sqrt(0.5)
	waitCalls(t, "volumes at the midpoint", b, func(c []string) bool { return math.Abs(gainOf(lastVolume(c))-want) < 0.01 })
	n := countCalls(b.snapshot(), "set_property speed")
	time.Sleep(60 * time.Millisecond)
	if countCalls(b.snapshot(), "set_property speed") != n {
		t.Error("a crossfade must not steer the speed")
	}
}

func TestNoiseInThePositionReadingsIsNotChased(t *testing.T) {
	// Readings that are off by up to 8 ms either way, on top of mpv's steps:
	// the beat is where it should be, and the speed must not wander after the noise.
	p, sim, t0 := phasePlayer(t, 0, 1.0, 0.1, func() { phaseDeadband = 0.010 /* what the player uses */ })
	sim.mu.Lock()
	sim.jitter = 0.008
	sim.mu.Unlock()
	deadline := time.Now().Add(4 * time.Second)
	for nextPoll := time.Now(); time.Now().Before(deadline) && time.Since(t0) < 2500*time.Millisecond; {
		if time.Now().After(nextPoll) {
			p.poll(bg)
			nextPoll = time.Now().Add(20 * time.Millisecond)
		}
		time.Sleep(time.Millisecond)
	}
	sim.mu.Lock()
	defer sim.mu.Unlock()
	for _, v := range sim.speeds {
		if math.Abs(v-0.96) > 0.96*0.005 {
			t.Errorf("speed %v: the loop chased noise (more than 0.5%% from 0.96)", v)
		}
	}
}

func TestACorrectionIsNotMadeMoreOftenThanPhaseEvery(t *testing.T) {
	p, sim, t0 := phasePlayer(t, 40*time.Millisecond, 0.25, 0.1, func() { phaseEvery = 300 * time.Millisecond })
	deadline := time.Now().Add(4 * time.Second)
	for nextPoll := time.Now(); time.Now().Before(deadline) && time.Since(t0) < 500*time.Millisecond+1500*time.Millisecond; {
		if time.Now().After(nextPoll) {
			p.poll(bg)
			nextPoll = time.Now().Add(20 * time.Millisecond)
		}
		time.Sleep(time.Millisecond)
	}
	sim.mu.Lock()
	defer sim.mu.Unlock()
	if len(sim.speeds) == 0 {
		t.Fatal("no correction at all")
	}
	if len(sim.speeds) > 6 { // 1.5 s at one every 0.3 s is 5, one more for luck
		t.Errorf("%d corrections in 1.5 s, want at most one every 300 ms", len(sim.speeds))
	}
}
