// Package player owns the queue and plays it through mpv. ytplay keeps the
// list of songs; each "deck" is an mpv process that plays one file at a time.
// The player tells the UI what changed with the same events mpv itself would
// send for a playlist (playlist, playlist-pos, idle-active, pause, end-file).
package player

import (
	"context"
	"encoding/json"
	"fmt"
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
	pre     *preload // the next song, loaded and paused on the idle deck
	// preFailed is a song whose preload failed; it is not tried again.
	preFailed string
	// noPreload is set when the second deck could not be started or died: the
	// player then runs on one deck and loads every song when its turn comes.
	noPreload bool

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
		start: start, q: queue{pos: -1},
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
				if i == p.active {
					p.doneOnce.Do(func() { close(p.done) })
				} else {
					p.noPreload = true // the idle deck is gone
					if p.pre != nil && p.pre.deck == i {
						p.pre = nil
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
		}
		return
	}
	switch ev.Name {
	case "property-change":
		var paused bool
		if ev.Property == "pause" && json.Unmarshal(ev.Data, &paused) == nil {
			p.paused = paused
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
