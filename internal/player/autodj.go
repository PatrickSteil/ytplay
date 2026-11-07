package player

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/PatrickSteil/ytplay/internal/autodj"
)

// Analyses tells what is known about a song, by its URL, for planning a
// transition: its length and the analysis of its two ends. false means nothing
// is known (yet). It is called from the player's own goroutines, so it must be
// safe for concurrent use.
type Analyses func(url string) (autodj.Song, bool)

var (
	// rampEvery is how often, during a transition, the volumes are made to
	// follow the position of the playing song.
	rampEvery = 50 * time.Millisecond
	// armWithin is how many seconds before the planned start of a transition
	// the ramp loop is started (it is checked on every poll, which is slower
	// than that, so this is more than pollEvery).
	armWithin = 1.5
	// tooLate is how little of the playing song may be left for a transition
	// still to be worth starting.
	tooLate = 1.0
	// startLatency is how long after the order to start the next song it can
	// be heard. Measured on real decks (mpv with a null output) from the early
	// or late kicks of beat-matched transitions: the start lands 17 or 40 ms
	// early with 50 ms, so about 22 ms. It is only the first guess: the phase
	// is corrected while both songs sound (see phaseTau).
	startLatency = 22 * time.Millisecond
	// easeScale scales how long the speed takes to ease back (1: the plan's
	// time; tests make it small), easeStep is the time between two steps.
	easeScale = 1.0
	easeStep  = 500 * time.Millisecond
	// The next song of a beat-matched transition is held in phase by steering
	// its speed: an error is removed over about phaseTau seconds, readings of the
	// first phaseSettle seconds after it started are not used, and the speed
	// never strays more than phaseMax (a fraction) from the beat-matched one.
	// Starting a deck by an order over a socket cannot be timed better than
	// about one audio chunk (some 20 ms), whatever startLatency is.
	phaseTau    = 1.0
	phaseSettle = 0.3
	phaseMax    = 0.03
	// Errors smaller than phaseDeadband seconds are within what the readings
	// can tell apart and are left alone, and the speed is changed at most every
	// phaseEvery: steering after the noise would put the beats off by as much as
	// the noise is.
	phaseDeadband = 0.010
	phaseEvery    = 250 * time.Millisecond
)

// minClockSamples is how many readings of the playing song's position are
// taken before the start of a beat-matched song is timed from them. The
// readings kept are those of the last clockWindow (the playing song) or
// phaseWindow (the next song): a window has to span several of mpv's steps for
// it to contain a reading taken just after one, which is the one that counts.
const (
	minClockSamples = 6
	clockWindow     = time.Second
	phaseWindow     = 400 * time.Millisecond
)

// recent drops the readings older than window.
func recent(s []posSample, now time.Time, window time.Duration) []posSample {
	i := 0
	for i < len(s) && now.Sub(s[i].wall) > window {
		i++
	}
	return s[i:]
}

// transition is the handover from the playing song to the preloaded one, in
// the making or in progress.
type transition struct {
	plan      autodj.Plan
	prepared  bool // the next song is at its starting point, silent and paused
	running   bool // the ramp loop was started
	started   bool // the next song is sounding
	skip      bool // too late for it: leave it to the plain handoff
	cancel    context.CancelFunc
	lastA     float64 // the volumes last sent, to send only changes
	lastB     float64
	samples   []posSample // recent readings of the playing song's position
	bSamples  []posSample // and of the next song's, once it sounds
	startedAt time.Time
	// lastCorrection is when the speed was last steered.
	lastCorrection time.Time
}

// posSample is one reading of a position and when it arrived.
type posSample struct {
	wall time.Time
	pos  float64
}

// clockOffset estimates when (in seconds after epoch) a song that plays at
// normal speed was at position 0. mpv updates the position in steps and a
// reading can only be stale, never early, so the smallest of "arrival time
// minus position" over the readings is the best estimate: it comes from the
// reading taken just after an update.
func clockOffset(samples []posSample, epoch time.Time) float64 {
	best := math.Inf(1)
	for _, s := range samples {
		best = math.Min(best, s.wall.Sub(epoch).Seconds()-s.pos)
	}
	return best
}

// easing is a deck's speed on its way back to 1.
type easing struct{ cancel context.CancelFunc }

// SetAutoDJ turns the automatic transitions on or off. With them on, the next
// song is started under the end of the playing one as planned by the autodj
// package from songs' analyses (nil: none known, every transition is a
// crossfade by duration).
func (p *Player) SetAutoDJ(on bool, songs Analyses) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.autoDJ, p.analyses = on, songs
	if !on {
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		defer cancel()
		p.abortTransition(ctx)
	}
}

// volumeFor is the mpv volume (0..100) that gives an amplitude gain of g.
// mpv's volume is cubic: gain = (volume/100)^3.
func volumeFor(g float64) float64 {
	return 100 * math.Cbrt(math.Max(0, math.Min(g, 1)))
}

// planTransition plans the handover from the playing song to the preloaded
// one with what the player and the analyses know. The caller holds p.mu.
func (p *Player) planTransition(ctx context.Context) (autodj.Plan, bool) {
	lengthA, err := p.decks[p.active].GetFloat(ctx, "duration")
	if err != nil || lengthA <= 0 {
		return autodj.Plan{}, false
	}
	lengthB, _ := p.decks[p.pre.deck].GetFloat(ctx, "duration")
	a, b := autodj.Song{Length: lengthA}, autodj.Song{Length: lengthB}
	if p.analyses != nil {
		if s, ok := p.analyses(p.q.urls[p.q.pos]); ok {
			a.Edges = s.Edges
		}
		if s, ok := p.analyses(p.pre.url); ok {
			b.Edges = s.Edges
		}
	}
	return autodj.For(a, b), true
}

// pollTransition prepares the next song and arms the ramp loop when the end of
// the playing song is near. The caller holds p.mu.
func (p *Player) pollTransition(ctx context.Context) {
	if p.pre == nil || p.q.pos < 0 {
		return
	}
	if p.trans == nil {
		p.trans = &transition{lastA: -1, lastB: -1}
	}
	tr := p.trans
	if tr.skip || tr.running {
		return
	}
	if !tr.prepared {
		plan, ok := p.planTransition(ctx) // re-planned until prepared: an analysis may arrive meanwhile
		if !ok {
			return
		}
		tr.plan = plan
		if !p.prepareTransition(ctx, tr) {
			return
		}
	}
	pos, err := p.decks[p.active].GetFloat(ctx, "time-pos")
	if err != nil {
		return
	}
	end := tr.plan.AStart + tr.plan.Overlap
	switch {
	case tr.plan.AStart-pos > armWithin:
		return
	case end-pos < tooLate:
		tr.skip = true // no time left: the plain handoff does it
		return
	case pos-tr.plan.AStart > tooLate:
		p.crossfadeFrom(ctx, tr, pos)
	}
	loopCtx, cancel := context.WithCancel(context.Background())
	tr.cancel, tr.running = cancel, true
	p.wg.Add(1)
	go p.runTransition(loopCtx, tr)
}

// prepareTransition makes the preloaded song ready: at its starting point,
// silent. It reports false while the song is not loaded yet. The caller holds
// p.mu.
func (p *Player) prepareTransition(ctx context.Context, tr *transition) bool {
	d := p.decks[p.pre.deck]
	if _, err := d.GetFloat(ctx, "duration"); err != nil {
		return false // still loading
	}
	if tr.plan.BFrom > 0.001 {
		if _, err := d.Command(ctx, "seek", tr.plan.BFrom, "absolute", "exact"); err != nil {
			return false
		}
	}
	if _, err := d.Command(ctx, "set_property", "volume", 0); err != nil {
		return false
	}
	if tr.plan.BSpeed != 1 {
		if _, err := d.Command(ctx, "set_property", "speed", tr.plan.BSpeed); err != nil {
			return false
		}
		p.speed[p.pre.deck] = tr.plan.BSpeed
	}
	tr.prepared = true
	return true
}

// runTransition follows the position of the playing song, the master clock,
// until the transition is over or cancelled.
func (p *Player) runTransition(ctx context.Context, tr *transition) {
	defer p.wg.Done()
	t := time.NewTicker(rampEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			more, startIn := p.stepTransition(tr)
			if !more {
				return
			}
			if startIn >= 0 { // the moment to start the next song is close: wait for it exactly
				if startIn > 0 {
					select {
					case <-time.After(startIn):
					case <-ctx.Done():
						return
					case <-p.stopped:
						return
					}
				}
				if !p.startNext(tr) {
					return
				}
			}
		case <-ctx.Done():
			return
		case <-p.stopped:
			return
		}
	}
}

// stepTransition starts a crossfade's next song when its time has come, sets
// the volumes for the playing song's position, and ends the transition when
// the overlap is over. more says whether the loop should go on. startIn >= 0
// asks the loop to start the next song of a beat-matched transition after that
// long, outside the lock, so that the moment is not lost to the loop's step.
func (p *Player) stepTransition(tr *transition) (more bool, startIn time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.trans != tr || p.pre == nil {
		return false, -1
	}
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	pos, err := p.decks[p.active].GetFloat(ctx, "time-pos")
	arrived := time.Now()
	if err != nil {
		return true, -1
	}
	u := (pos - tr.plan.AStart) / tr.plan.Overlap
	if tr.plan.Kind == autodj.BeatMatch {
		tr.samples = recent(append(tr.samples, posSample{arrived, pos}), arrived, clockWindow)
	}
	switch {
	case u >= 1 && tr.started:
		p.takeOver(ctx)
		return false, -1
	case u >= 1:
		p.handoff(ctx) // jumped past the whole overlap before the next song ever started
		return false, -1
	case tr.started && u < -0.05: // the user went back in the song
		p.abortTransition(ctx)
		return false, -1
	case !tr.started && tr.plan.Kind == autodj.BeatMatch && pos < tr.plan.AStart:
		// Beat-matched: the next song has to start at the very moment the
		// playing one reaches AStart, less the time the deck takes to start. Its
		// position only moves in steps, so the moment is worked out from a number
		// of readings rather than from one.
		if len(tr.samples) >= minClockSamples {
			at := clockOffset(tr.samples, p.epoch) + tr.plan.AStart
			startAt := p.epoch.Add(time.Duration(at * float64(time.Second))).Add(-startLatency)
			if wait := time.Until(startAt); wait <= 2*rampEvery {
				return true, max(wait, 0)
			}
		}
		return true, -1
	case !tr.started && pos >= tr.plan.AStart:
		if pos-tr.plan.AStart > tooLate {
			p.crossfadeFrom(ctx, tr, pos)
			u = 0
		}
		if _, err := p.decks[p.pre.deck].Command(ctx, "set_property", "pause", p.paused); err != nil {
			p.abortTransition(ctx)
			return false, -1
		}
		tr.started, tr.startedAt = true, time.Now()
	}
	if tr.started {
		p.setGains(ctx, tr, math.Max(0, u))
		if tr.plan.Kind == autodj.BeatMatch {
			p.correctPhase(ctx, tr)
		}
	}
	return true, -1
}

// correctPhase steers the speed of the next song of a beat-matched transition
// so that its beats stay on the playing song's. Where the next song should be
// is fixed by the plan: BFrom at the moment the playing song is at AStart, then
// at the planned speed. How far ahead of that it is, delta, is read from its
// position, which can only be stale and so never ahead of the truth: the
// largest of the recent readings is the best one. Slowing down by delta/phaseTau
// takes the error out in about phaseTau seconds. The caller holds p.mu.
func (p *Player) correctPhase(ctx context.Context, tr *transition) {
	deck := p.pre.deck
	pos, err := p.decks[deck].GetFloat(ctx, "time-pos")
	arrived := time.Now()
	if err != nil {
		return
	}
	tr.bSamples = recent(append(tr.bSamples, posSample{arrived, pos}), arrived, phaseWindow)
	planned := clockOffset(tr.samples, p.epoch) + tr.plan.AStart // seconds after epoch at which the playing song is at AStart
	delta, n := math.Inf(-1), 0
	for _, s := range tr.bSamples {
		if s.wall.Sub(tr.startedAt).Seconds() < phaseSettle {
			continue
		}
		should := tr.plan.BFrom + tr.plan.BSpeed*(s.wall.Sub(p.epoch).Seconds()-planned)
		delta, n = math.Max(delta, s.pos-should), n+1
	}
	if n < 3 || arrived.Sub(tr.lastCorrection) < phaseEvery {
		return
	}
	over := math.Copysign(math.Max(math.Abs(delta)-phaseDeadband, 0), delta) // the part of the error beyond the noise
	speed := tr.plan.BSpeed - over/phaseTau
	speed = math.Max(tr.plan.BSpeed*(1-phaseMax), math.Min(speed, tr.plan.BSpeed*(1+phaseMax)))
	if math.Abs(speed-p.speed[deck]) < 0.0005 {
		return
	}
	if _, err := p.decks[deck].Command(ctx, "set_property", "speed", speed); err == nil {
		slog.Debug("autodj: phase correction", "ahead_ms", delta*1000, "speed", speed)
		p.speed[deck], tr.lastCorrection = speed, arrived
	}
}

// crossfadeFrom turns a transition whose start has already passed by more than
// tooLate into a crossfade over the time that is left, at normal speed: the
// next song was ready too late to line the beats up, and a crossfade is what
// can still be done well. The caller holds p.mu.
func (p *Player) crossfadeFrom(ctx context.Context, tr *transition, pos float64) {
	end := tr.plan.AStart + tr.plan.Overlap
	if tr.plan.Kind == autodj.BeatMatch {
		p.resetSpeed(ctx, p.pre.deck)
		tr.plan.Kind, tr.plan.BSpeed, tr.plan.EaseBack = autodj.Crossfade, 1, 0
	}
	tr.plan.AStart, tr.plan.Overlap = pos, end-pos
}

// startNext starts the next song of a beat-matched transition. It reports
// whether the transition is still going on.
func (p *Player) startNext(tr *transition) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.trans != tr || p.pre == nil || tr.started {
		return p.trans == tr && p.pre != nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	if _, err := p.decks[p.pre.deck].Command(ctx, "set_property", "pause", p.paused); err != nil {
		p.abortTransition(ctx)
		return false
	}
	tr.started, tr.startedAt = true, time.Now()
	return true
}

// setGains sets the volumes of the two decks for a position u (0..1) in the
// overlap, along an equal-power curve. The caller holds p.mu.
func (p *Player) setGains(ctx context.Context, tr *transition, u float64) {
	scale := p.master / 100 // the ramp's volumes times the master multiply mpv's cubic gains
	va := volumeFor(math.Cos(math.Pi/2*u)) * scale
	vb := volumeFor(math.Sin(math.Pi/2*u)) * scale
	if math.Abs(va-tr.lastA) >= 0.25 {
		if _, err := p.decks[p.active].Command(ctx, "set_property", "volume", va); err == nil {
			tr.lastA = va
		}
	}
	if math.Abs(vb-tr.lastB) >= 0.25 {
		if _, err := p.decks[p.pre.deck].Command(ctx, "set_property", "volume", vb); err == nil {
			tr.lastB = vb
		}
	}
}

// takeOver makes the next song, already sounding, the current one, and eases
// its speed back to normal. The caller holds p.mu.
func (p *Player) takeOver(ctx context.Context) {
	pre, ease := p.pre, p.trans.plan.EaseBack
	p.endTransition(ctx, false, true) // sets the next song's volume to full
	p.pre = nil
	old := p.decks[p.active]
	_, _ = old.Command(ctx, "stop")
	_, _ = old.Command(ctx, "set_property", "volume", p.master)
	p.active, p.entryID = pre.deck, pre.entryID
	p.q.pos++
	p.startEase(pre.deck, ease)
	p.emitState()
}

// endTransition stops the ramp loop and puts the volumes back to the master. If
// restorePlaying is true the playing song's volume is restored too. If the
// next song is not going on to be played (continuing is false) its speed goes
// back to normal too. It does nothing if there is no transition. The caller
// holds p.mu.
func (p *Player) endTransition(ctx context.Context, restorePlaying, continuing bool) {
	tr := p.trans
	if tr == nil {
		return
	}
	p.trans = nil
	if tr.cancel != nil {
		tr.cancel()
	}
	if tr.prepared && p.pre != nil {
		_, _ = p.decks[p.pre.deck].Command(ctx, "set_property", "volume", p.master)
		if !continuing {
			p.resetSpeed(ctx, p.pre.deck)
		}
	}
	if restorePlaying && tr.started {
		_, _ = p.decks[p.active].Command(ctx, "set_property", "volume", p.master)
	}
}

// resetSpeed puts a deck back to normal speed at once. The caller holds p.mu.
func (p *Player) resetSpeed(ctx context.Context, deck int) {
	if p.ease != nil && p.easeDeck == deck {
		p.stopEase()
	}
	if p.speed[deck] != 1 {
		if _, err := p.decks[deck].Command(ctx, "set_property", "speed", 1); err == nil {
			p.speed[deck] = 1
		}
	}
}

// startEase eases a deck's speed back to 1 over seconds (scaled by
// easeScale), in steps. The caller holds p.mu.
func (p *Player) startEase(deck int, seconds float64) {
	p.stopEase()
	from := p.speed[deck]
	if from == 1 || seconds <= 0 {
		return
	}
	total := time.Duration(seconds * easeScale * float64(time.Second))
	step := time.Duration(float64(easeStep) * easeScale)
	n := max(int(total/max(step, time.Millisecond)), 1)
	ctx, cancel := context.WithCancel(context.Background())
	p.ease, p.easeDeck = &easing{cancel: cancel}, deck
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for k := 1; k <= n; k++ {
			select {
			case <-time.After(max(step, time.Millisecond)):
			case <-ctx.Done():
				return
			case <-p.stopped:
				return
			}
			speed := from + (1-from)*float64(k)/float64(n)
			p.mu.Lock()
			if ctx.Err() != nil {
				p.mu.Unlock()
				return
			}
			cctx, ccancel := context.WithTimeout(context.Background(), cmdTimeout)
			if _, err := p.decks[deck].Command(cctx, "set_property", "speed", speed); err == nil {
				p.speed[deck] = speed
			}
			ccancel()
			p.mu.Unlock()
		}
	}()
}

// stopEase stops an easing of the speed that is going on. The caller holds p.mu.
func (p *Player) stopEase() {
	if p.ease != nil {
		p.ease.cancel()
		p.ease = nil
	}
}

// abortTransition gives up on the transition: the next song is dropped (it is
// loaded again when needed) and the playing song carries on at full volume.
// The caller holds p.mu.
func (p *Player) abortTransition(ctx context.Context) {
	if p.trans == nil {
		return
	}
	p.endTransition(ctx, true, false)
	p.dropPreload(ctx)
}

// forgetTransition drops the transition without sending anything, for when
// the deck it needs is gone. The caller holds p.mu.
func (p *Player) forgetTransition() {
	if p.trans != nil && p.trans.cancel != nil {
		p.trans.cancel()
	}
	p.trans = nil
}

// forwardPause makes the next song follow the user's pause while both sound.
// The caller holds p.mu.
func (p *Player) forwardPause(paused bool) {
	if p.trans == nil || !p.trans.started || p.pre == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	_, _ = p.decks[p.pre.deck].Command(ctx, "set_property", "pause", paused)
}
