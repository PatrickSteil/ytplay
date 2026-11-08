package player

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/PatrickSteil/ytplay/internal/mpv"
)

// castState is what is remembered while the player plays on a speaker.
type castState struct {
	deck      Deck
	name      string
	local     int  // the slot of the local deck that was playing before
	noPreload bool // what noPreload was before: nothing is preloaded or mixed while casting
}

// CastTo moves playback to a cast device (d, a CastDeck of the device called
// name): the playing song goes on there from where it was, the queue keeps
// playing there, and the local deck rests, paused, until CastOff. Casting to
// another device while casting moves it on.
func (p *Player) CastTo(ctx context.Context, name string, d Deck) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	pos := p.position(ctx)
	if p.cast != nil { // from one speaker to another
		old, slot := p.cast.deck, p.active
		p.decks[slot] = d
		p.cast.deck, p.cast.name = d, name
		p.watchCast(slot, d)
		p.setVolumeFirst(ctx, d)
		p.startOn(ctx, d, pos)
		_ = old.Close()
		p.emit(mpv.Event{Name: "cast", Reason: name})
		return nil
	}
	p.dropPreload(ctx)
	local := p.active
	slot := 1 - local
	if idle := p.decks[slot]; idle != nil {
		p.decks[slot] = nil
		_ = idle.Close()
	}
	p.cast = &castState{deck: d, name: name, local: local, noPreload: p.noPreload}
	p.noPreload = true
	if p.q.pos >= 0 {
		_, _ = p.decks[local].Command(ctx, "set_property", "pause", true) // kept loaded, to come back to
	}
	p.decks[slot], p.active = d, slot
	p.watchCast(slot, d)
	p.setVolumeFirst(ctx, d)
	p.startOn(ctx, d, pos)
	p.emit(mpv.Event{Name: "cast", Reason: name})
	return nil
}

// CastOff goes back to playing here, from where the speaker was.
func (p *Player) CastOff(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cast == nil {
		return errors.New("not casting")
	}
	return p.leaveCast(ctx, "")
}

// Casting is the name of the device being played on, "" when playing here.
func (p *Player) Casting() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cast == nil {
		return ""
	}
	return p.cast.name
}

// position is where the playing song is, 0 if unknown. The caller holds p.mu.
func (p *Player) position(ctx context.Context) float64 {
	if p.q.pos < 0 {
		return 0
	}
	pos, err := p.decks[p.active].GetFloat(ctx, "time-pos")
	if err != nil {
		return 0
	}
	return pos
}

// startOn plays the current song on deck d from pos, as it was paused or not.
// The caller holds p.mu.
func (p *Player) startOn(ctx context.Context, d Deck, pos float64) {
	if p.q.pos < 0 {
		return
	}
	id, err := load(ctx, d, p.q.urls[p.q.pos])
	if err != nil {
		p.emit(mpv.Event{Name: "end-file", Reason: "error"})
		return
	}
	p.entryID = id
	if pos > 0.5 {
		_, _ = d.Command(ctx, "seek", pos, "absolute")
	}
	if p.paused {
		_, _ = d.Command(ctx, "set_property", "pause", true)
	}
}

// setVolumeFirst gives a speaker the volume the user chose before anything is
// played on it, so that a quiet setting is never preceded by the speaker's own
// (maybe loud) one. The caller holds p.mu.
func (p *Player) setVolumeFirst(ctx context.Context, d Deck) {
	if p.master != 100 {
		_, _ = d.Command(ctx, "set_property", "volume", p.master)
	}
}

// leaveCast makes the local deck play again, from where the speaker was. reason
// is what the UI is told: "" for a wish, "lost" when the speaker went away. The
// caller holds p.mu.
func (p *Player) leaveCast(ctx context.Context, reason string) error {
	cs, slot := p.cast, p.active
	local := p.decks[cs.local]
	if local == nil {
		return errors.New("the local player is gone")
	}
	pos := p.position(ctx)
	p.active, p.decks[slot] = cs.local, nil
	p.noPreload, p.cast = cs.noPreload, nil
	_ = cs.deck.Close()
	if p.q.pos >= 0 {
		args := []any{"loadfile", p.q.urls[p.q.pos], "replace"}
		if pos > 0.5 {
			args = append(args, -1, fmt.Sprintf("start=%v", pos))
		}
		raw, err := local.Command(ctx, args...)
		if err != nil {
			p.emit(mpv.Event{Name: "end-file", Reason: "error"})
		} else {
			var reply struct {
				ID int64 `json:"playlist_entry_id"`
			}
			p.entryID = unknownEntry
			if json.Unmarshal(raw, &reply) == nil && reply.ID != 0 {
				p.entryID = reply.ID
			}
		}
		_, _ = local.Command(ctx, "set_property", "pause", p.paused)
	}
	if p.master != 100 {
		_, _ = local.Command(ctx, "set_property", "volume", p.master)
	}
	reasonName := reason
	p.emit(mpv.Event{Name: "cast", Reason: reasonName})
	return nil
}

// watchCast forwards the events of the speaker's deck (in slot) until it is
// replaced; if it ends by itself the speaker is lost and playback comes back.
func (p *Player) watchCast(slot int, d Deck) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			select {
			case ev := <-d.Events():
				p.mu.Lock()
				current := p.decks[slot] == d
				p.mu.Unlock()
				if current {
					p.onDeckEvent(slot, ev)
				}
			case <-d.Done():
				p.mu.Lock()
				if p.cast != nil && p.cast.deck == d {
					ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
					_ = p.leaveCast(ctx, "lost")
					cancel()
				}
				p.mu.Unlock()
				return
			case <-p.stopped:
				return
			}
		}
	}()
}
