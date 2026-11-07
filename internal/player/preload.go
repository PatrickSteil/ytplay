package player

import (
	"context"
	"time"
)

// preloadLead is how long before the end of a song the next one is loaded,
// paused, on the idle deck. Resolving a YouTube stream takes about 3 s.
var preloadLead = 15 * time.Second

// autoDJLead is how much longer before the end the next song is loaded when
// autodj is on: a beat-matched transition starts some 8 s before the end, and
// a stream takes several seconds to resolve and load, so 15 s is too late to be
// ready (found by running the real thing).
var autoDJLead = 15 * time.Second

// pollEvery is how often the player looks at the playback position.
var pollEvery = 500 * time.Millisecond

// deckNames name the two mpv processes (and so their sockets).
var deckNames = [2]string{"a", "b"}

// preload is the next song, loaded and paused on the idle deck.
type preload struct {
	url     string
	deck    int
	entryID int64
}

func (p *Player) pollLoop(every time.Duration) {
	defer p.wg.Done()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
			p.poll(ctx)
			cancel()
		case <-p.stopped:
			return
		}
	}
}

// poll starts the preload of the next song once the playing one is within
// preloadLead of its end.
func (p *Player) poll(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pollPreload(ctx)
	if p.autoDJ {
		p.pollTransition(ctx)
	}
}

// pollPreload is the preload part of poll. The caller holds p.mu.
func (p *Player) pollPreload(ctx context.Context) {
	if p.noPreload || p.pre != nil || p.q.pos < 0 || p.q.pos+1 >= len(p.q.urls) || p.q.urls[p.q.pos+1] == p.preFailed {
		return
	}
	d := p.decks[p.active]
	elapsed, err := d.GetFloat(ctx, "time-pos")
	if err != nil {
		return
	}
	total, err := d.GetFloat(ctx, "duration")
	if err != nil || total <= 0 {
		return
	}
	lead := preloadLead
	if p.autoDJ {
		lead += autoDJLead
	}
	if time.Duration((total-elapsed)*float64(time.Second)) > lead {
		return
	}
	p.startPreload(ctx)
}

// startPreload loads the song after the playing one, paused, on the idle
// deck. If that fails the song is remembered so it is not retried on every
// poll; it will simply be loaded when its turn comes. The caller holds p.mu.
func (p *Player) startPreload(ctx context.Context) {
	url := p.q.urls[p.q.pos+1]
	i := 1 - p.active
	if p.decks[i] == nil {
		d, err := p.start(ctx, deckNames[i])
		if err != nil {
			p.noPreload = true // no second deck: run on one
			return
		}
		p.decks[i] = d
		p.watch(i, d)
		if p.master != 100 { // a new deck starts at full volume
			_, _ = d.Command(ctx, "set_property", "volume", p.master)
		}
	}
	d := p.decks[i]
	p.resetSpeed(ctx, i) // a speed left over from an easing that was cut short
	if _, err := d.Command(ctx, "set_property", "pause", true); err != nil {
		p.preFailed = url
		return
	}
	id, err := load(ctx, d, url)
	if err != nil {
		p.preFailed = url
		return
	}
	p.pre = &preload{url: url, deck: i, entryID: id}
}

// syncPreload drops the preload unless it is still the song after the playing
// one. Call it after every change of the queue. The caller holds p.mu.
func (p *Player) syncPreload(ctx context.Context) {
	if p.pre == nil {
		return
	}
	if p.q.pos >= 0 && p.q.pos+1 < len(p.q.urls) && p.q.urls[p.q.pos+1] == p.pre.url {
		return
	}
	p.dropPreload(ctx)
}

func (p *Player) dropPreload(ctx context.Context) {
	if p.pre == nil {
		return
	}
	p.endTransition(ctx, true, false)
	_, _ = p.decks[p.pre.deck].Command(ctx, "stop")
	p.pre = nil
}

// handoff makes the preloaded deck the active one, so the song starts at once
// instead of being loaded. The paused state is the user's, not the preload's.
// It reports false if the deck did not take the command; the caller then loads
// the song the slow way. The caller holds p.mu.
func (p *Player) handoff(ctx context.Context) bool {
	var hadTransition, started bool
	var ease float64
	if p.trans != nil {
		hadTransition, started, ease = true, p.trans.started, p.trans.plan.EaseBack
	}
	p.endTransition(ctx, false, true) // while p.pre is still set: puts the next song's volume back
	pre := p.pre
	p.pre = nil
	d := p.decks[pre.deck]
	if _, err := d.Command(ctx, "set_property", "pause", p.paused); err != nil {
		_, _ = d.Command(ctx, "stop")
		return false
	}
	_, _ = p.decks[p.active].Command(ctx, "stop")
	if hadTransition {
		_, _ = p.decks[p.active].Command(ctx, "set_property", "volume", p.master)
	}
	p.active, p.entryID = pre.deck, pre.entryID
	p.q.pos++
	if started {
		p.startEase(pre.deck, ease) // it was sounding at the beat-matched speed
	} else if hadTransition {
		p.resetSpeed(ctx, pre.deck)
	}
	p.emitState()
	return true
}
