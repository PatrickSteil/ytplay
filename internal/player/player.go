// Package player owns the queue and plays it through mpv. ytplay keeps the
// list of songs; each "deck" is an mpv process that plays one file at a time.
// The player tells the UI what changed with the same events mpv itself would
// send for a playlist (playlist, playlist-pos, idle-active, pause, end-file).
package player

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/PatrickSteil/ytplay/internal/mpv"
)

// cmdTimeout bounds the commands the player sends on its own account (when a
// song ends), as opposed to those sent for a UI action, which bring a context.
const cmdTimeout = 5 * time.Second

// unknownEntry stands for the entry id of a file loaded by an mpv that does
// not report ids: any end-file is then taken to be about the current file.
const unknownEntry int64 = -1

// Deck is one mpv process. *mpv.Client is one.
type Deck interface {
	Command(ctx context.Context, args ...any) (json.RawMessage, error)
	GetFloat(ctx context.Context, prop string) (float64, error)
	Events() <-chan mpv.Event
	Done() <-chan struct{}
	StderrTail() string
	Close() error
}

// StartDeck starts the mpv process called name ("a", "b").
type StartDeck func(ctx context.Context, name string) (Deck, error)

// Player plays the queue.
type Player struct {
	start StartDeck

	mu      sync.Mutex // guards everything below
	decks   [2]Deck
	active  int
	q       queue
	entryID int64 // playlist_entry_id of the file loaded on the active deck, 0 if none
	paused  bool
	// master is the volume the user chose, in percent of full. Every volume the
	// engine sets is scaled by it, and a deck at rest is at it.
	master float64
	pre    *preload // the next song, loaded and paused on the idle deck
	// preFailed is a song whose preload failed; it is not tried again.
	preFailed string
	// noPreload is set when the second deck could not be started or died: the
	// player then runs on one deck and loads every song when its turn comes.
	noPreload bool
	// Automatic transitions (see autodj.go).
	autoDJ   bool
	analyses Analyses
	// trim tells where songs start and end sounding (see trim.go); trimmedStart is
	// the song whose start was dealt with.
	trim         Analyses
	cast         *castState // set while playing on a speaker (see cast.go)
	trimmedStart trimmed
	trans        *transition
	epoch        time.Time  // the time positions are counted from when timing a start
	speed        [2]float64 // the speed each deck was last set to
	gain         [2]float64 // the gain (dB, mpv's volume-gain) each deck was last set to: autodj evens out the loudness of songs with it
	ease         *easing    // the speed of deck easeDeck is returning to 1
	easeDeck     int

	events    chan mpv.Event
	done      chan struct{} // closed when the active deck's process exits
	doneOnce  sync.Once
	stopped   chan struct{} // closed by Close
	closeOnce sync.Once
	closeErr  error
	wg        sync.WaitGroup
}

// New starts the first deck.
func New(ctx context.Context, start StartDeck) (*Player, error) {
	a, err := start(ctx, "a")
	if err != nil {
		return nil, err
	}
	p := &Player{
		start: start, q: queue{pos: -1}, epoch: time.Now(), speed: [2]float64{1, 1}, master: 100,
		events:  make(chan mpv.Event, 256),
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	p.decks[0] = a
	p.watch(0, a)
	p.wg.Add(1)
	go p.pollLoop(pollEvery)
	return p, nil
}

// Events delivers what the UI needs to follow the player.
func (p *Player) Events() <-chan mpv.Event { return p.events }

// Done is closed when the active deck's mpv process has exited.
func (p *Player) Done() <-chan struct{} { return p.done }

// StderrTail is the end of the active deck's stderr, for error messages.
func (p *Player) StderrTail() string {
	p.mu.Lock()
	d := p.decks[p.active]
	p.mu.Unlock()
	return d.StderrTail()
}

// Close stops the decks. It is safe to call twice.
func (p *Player) Close() error {
	p.closeOnce.Do(func() {
		close(p.stopped)
		p.mu.Lock()
		decks := p.decks
		p.mu.Unlock()
		for _, d := range decks {
			if d == nil {
				continue
			}
			if err := d.Close(); err != nil && p.closeErr == nil {
				p.closeErr = err
			}
		}
		p.wg.Wait()
	})
	return p.closeErr
}

// watch forwards the events of deck d (number i) until it exits.
func (p *Player) watch(i int, d Deck) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			select {
			case ev := <-d.Events():
				p.onDeckEvent(i, ev)
			case <-d.Done():
				p.mu.Lock()
				if p.decks[i] != d {
					p.mu.Unlock()
					return
				}
				if i == p.active {
					p.doneOnce.Do(func() { close(p.done) })
				} else {
					p.noPreload = true // the idle deck is gone
					if p.pre != nil && p.pre.deck == i {
						p.pre = nil
						p.forgetTransition()
					}
				}
				p.mu.Unlock()
				return
			case <-p.stopped:
				return
			}
		}
	}()
}

func (p *Player) onDeckEvent(i int, ev mpv.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i != p.active {
		// The idle deck holds the preload. If its file cannot be opened, forget
		// the preload quietly: the UI hears about it when the song's turn comes.
		if p.pre != nil && p.pre.deck == i && ev.Name == "end-file" && ev.Reason == "error" &&
			(p.pre.entryID == unknownEntry || ev.EntryID == p.pre.entryID) {
			p.preFailed, p.pre = p.pre.url, nil
			p.forgetTransition()
		}
		return
	}
	switch ev.Name {
	case "property-change":
		var paused bool
		if ev.Property == "pause" && json.Unmarshal(ev.Data, &paused) == nil {
			p.paused = paused
			p.forwardPause(paused)
			p.emit(ev)
		}
	case "end-file":
		// Only the end of the file that is loaded counts. A late end-file of an
		// earlier file (a skip raced with the song ending) must not advance the
		// queue a second time.
		if p.q.pos < 0 || p.entryID == 0 || p.entryID != unknownEntry && ev.EntryID != p.entryID {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		defer cancel()
		switch ev.Reason {
		case "eof":
			p.playFrom(ctx, p.q.pos+1)
		case "error":
			p.emit(mpv.Event{Name: "end-file", Reason: "error"})
			p.playFrom(ctx, p.q.pos+1)
		}
	}
}

// load starts url on deck d, replacing what plays there, and returns the
// entry id mpv gave the file.
func load(ctx context.Context, d Deck, url string) (int64, error) {
	raw, err := d.Command(ctx, "loadfile", url, "replace")
	if err != nil {
		return 0, err
	}
	var reply struct {
		ID int64 `json:"playlist_entry_id"`
	}
	if json.Unmarshal(raw, &reply) != nil || reply.ID == 0 {
		return unknownEntry, nil
	}
	return reply.ID, nil
}

// playFrom plays entry i. An entry that cannot be loaded is reported (the UI
// shows "Could not play") and skipped. Past the last entry the player goes idle.
// The caller holds p.mu.
func (p *Player) playFrom(ctx context.Context, i int) {
	p.syncPreload(ctx)
	p.preFailed = ""
	if p.pre != nil && i == p.q.pos+1 && i < len(p.q.urls) && p.handoff(ctx) {
		return // syncPreload kept it, so it is entry i
	}
	for ; i < len(p.q.urls); i++ {
		p.q.pos, p.entryID = i, 0
		p.emitState() // first, so the UI knows which song a failure is about
		p.resetDeck(ctx, p.active)
		id, err := load(ctx, p.decks[p.active], p.q.urls[i])
		if err == nil {
			p.entryID = id
			return
		}
		p.emit(mpv.Event{Name: "end-file", Reason: "error"})
	}
	p.goIdle(ctx)
}

// goIdle stops playing and keeps the queue. The caller holds p.mu.
func (p *Player) goIdle(ctx context.Context) {
	p.dropPreload(ctx)
	p.q.pos, p.entryID = -1, 0
	_, _ = p.decks[p.active].Command(ctx, "stop")
	p.emitState()
}

// cmd sends a command to the active deck. The caller holds p.mu.
func (p *Player) cmd(ctx context.Context, args ...any) error {
	if _, err := p.decks[p.active].Command(ctx, args...); err != nil {
		return fmt.Errorf("%v: %w", args[0], err)
	}
	return nil
}

// queueLocked appends urls and starts the first one if nothing was playing.
func (p *Player) queueLocked(ctx context.Context, urls []string) error {
	if len(urls) == 0 {
		return nil
	}
	first, idle := len(p.q.urls), p.q.pos < 0
	p.q.append(urls...)
	if idle {
		p.playFrom(ctx, first)
	} else {
		p.emitState()
	}
	return nil
}

// Queue appends url and starts it if the player was idle.
func (p *Player) Queue(ctx context.Context, url string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.queueLocked(ctx, []string{url})
}

// QueueMany appends all urls; the first one starts if the player was idle.
func (p *Player) QueueMany(ctx context.Context, urls []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.queueLocked(ctx, urls)
}

// PlayNow plays url immediately and keeps the rest of the queue behind it.
func (p *Player) PlayNow(ctx context.Context, url string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.q.pos < 0 {
		return p.queueLocked(ctx, []string{url})
	}
	p.playFrom(ctx, p.q.insertAfterCurrent(url))
	return nil
}

// Skip plays the next entry; at the last one it goes idle.
func (p *Player) Skip(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.q.pos >= 0 {
		p.playFrom(ctx, p.q.pos+1)
	}
	return nil
}

// SeekPercent seeks to n percent (0..100) of the current song.
func (p *Player) SeekPercent(ctx context.Context, n int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.q.pos < 0 {
		return nil
	}
	return p.cmd(ctx, "seek", n, "absolute-percent")
}

// SeekTo seeks to an absolute time (seconds) in the current song.
func (p *Player) SeekTo(ctx context.Context, seconds float64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.q.pos < 0 {
		return nil
	}
	return p.cmd(ctx, "seek", seconds, "absolute")
}

// SeekBy seeks by a number of seconds, back if it is negative.
func (p *Player) SeekBy(ctx context.Context, seconds float64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.q.pos < 0 {
		return nil
	}
	return p.cmd(ctx, "seek", seconds, "relative")
}

// prevRestart is how long a song may have played for Prev to go back to its
// beginning instead of to the song before.
const prevRestart = 3.0

// Prev goes back: to the song before if this one has only just started, else to
// the beginning of this one (which is also all there is to do on the first song).
func (p *Player) Prev(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.q.pos < 0 {
		return nil
	}
	elapsed, err := p.decks[p.active].GetFloat(ctx, "time-pos")
	if p.q.pos == 0 || (err == nil && elapsed > prevRestart) {
		return p.cmd(ctx, "seek", 0, "absolute")
	}
	p.dropPreload(ctx) // it is the song after this one, which is about to be the one after the next
	p.playFrom(ctx, p.q.pos-1)
	return nil
}

// Volume is the master volume, in percent of full.
func (p *Player) Volume() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.master
}

// SetVolume sets the master volume (0 to 100, in percent of full; mpv's own
// scale is cubic, this is the scale it is on). It reaches the decks at rest at
// once and a transition on its next step: while one is on, its ramp owns the
// volumes of the decks it fades.
func (p *Player) SetVolume(ctx context.Context, percent float64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.master = math.Max(0, math.Min(percent, 100))
	var first error
	for i, d := range p.decks {
		if d == nil || p.rampOwns(i) {
			continue
		}
		if _, err := d.Command(ctx, "set_property", "volume", p.master); err != nil && first == nil {
			first = fmt.Errorf("volume: %w", err)
		}
	}
	return first
}

// rampOwns says whether deck i's volume is being set by a transition: the next
// song's deck from when it is prepared (silent), both from when it has started.
// The caller holds p.mu.
func (p *Player) rampOwns(i int) bool {
	tr := p.trans
	if tr == nil || p.pre == nil {
		return false
	}
	return (tr.prepared && i == p.pre.deck) || (tr.started && i == p.active)
}

// TogglePause pauses or resumes.
func (p *Player) TogglePause(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.q.pos < 0 {
		return nil
	}
	return p.cmd(ctx, "cycle", "pause")
}

// SetPause pauses (true) or resumes (false) explicitly.
func (p *Player) SetPause(ctx context.Context, paused bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cmd(ctx, "set_property", "pause", paused)
}

// Stop stops playing and keeps the queue.
func (p *Player) Stop(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.goIdle(ctx)
	return nil
}

// Remove deletes entry i of the whole list. Removing the song that plays
// starts the one after it.
func (p *Player) Remove(ctx context.Context, i int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	wasCurrent := i == p.q.pos
	if err := p.q.remove(i); err != nil {
		return err
	}
	switch {
	case !wasCurrent:
		p.syncPreload(ctx)
		p.emitState()
	case p.q.pos >= 0:
		p.playFrom(ctx, p.q.pos)
	default:
		p.goIdle(ctx)
	}
	return nil
}

// MoveUp swaps entry i with the one before it.
func (p *Player) MoveUp(ctx context.Context, i int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.q.swap(i-1, i); err != nil {
		return err
	}
	p.syncPreload(ctx)
	p.emitState()
	return nil
}

// MoveDown swaps entry i with the one after it.
func (p *Player) MoveDown(ctx context.Context, i int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.q.swap(i, i+1); err != nil {
		return err
	}
	p.syncPreload(ctx)
	p.emitState()
	return nil
}

// SetUpcoming replaces the songs after the playing one, so songs can be added,
// removed and put in any order at once (this is what undoing a queue edit needs).
func (p *Player) SetUpcoming(ctx context.Context, urls []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.q.pos < 0 {
		if len(urls) != 0 {
			return errors.New("nothing is playing, so there are no upcoming songs")
		}
		return nil
	}
	p.q.setUpcoming(urls)
	p.syncPreload(ctx)
	p.emitState()
	return nil
}

// ReorderUpcoming puts the songs after the playing one in the given order.
// urls must be exactly the upcoming songs.
func (p *Player) ReorderUpcoming(ctx context.Context, urls []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.q.reorderUpcoming(urls); err != nil {
		return err
	}
	p.syncPreload(ctx)
	p.emitState()
	return nil
}

// ClearQueue removes every entry except the one that plays now.
func (p *Player) ClearQueue(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.q.clearExceptCurrent()
	p.syncPreload(ctx)
	p.emitState()
	return nil
}

// Position is the playback position and length of the current song in
// seconds; it errors while nothing is playing.
func (p *Player) Position(ctx context.Context) (elapsed, total float64, err error) {
	p.mu.Lock()
	d, playing := p.decks[p.active], p.q.pos >= 0
	p.mu.Unlock()
	if !playing {
		return 0, 0, fmt.Errorf("nothing is playing")
	}
	if elapsed, err = d.GetFloat(ctx, "time-pos"); err != nil {
		return 0, 0, err
	}
	if total, err = d.GetFloat(ctx, "duration"); err != nil {
		return 0, 0, err
	}
	return elapsed, total, nil
}
