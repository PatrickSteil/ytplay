package player

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/PatrickSteil/ytplay/internal/mpv"
)

// Tests call poll themselves; the background ticker must not interfere.
func init() { pollEvery = time.Hour }

// newTestPlayer2 returns a Player whose decks "a" and "b" are fakes.
func newTestPlayer2(t *testing.T) (p *Player, a, b *fakeDeck) {
	t.Helper()
	a, b = newFakeDeck(), newFakeDeck()
	p, err := New(bg, func(ctx context.Context, name string) (Deck, error) {
		switch name {
		case "a":
			return a, nil
		case "b":
			return b, nil
		}
		return nil, errors.New("no deck " + name)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p, a, b
}

// nearEnd makes the deck report 10 s left of a 200 s song (inside the lead).
func nearEnd(d *fakeDeck) { d.props["time-pos"], d.props["duration"] = 190, 200 }

// preloaded queues a, b, c, lets deck a play near its end, polls once and
// forgets the commands and events so far.
func preloaded(t *testing.T) (p *Player, a, b *fakeDeck) {
	t.Helper()
	p, a, b = newTestPlayer2(t)
	if err := p.QueueMany(bg, []string{"a", "b", "c"}); err != nil {
		t.Fatal(err)
	}
	nearEnd(a)
	p.poll(bg)
	eq(t, "preload commands", b.take(), []string{"set_property pause true", "loadfile b replace"})
	a.take()
	drain(p)
	return p, a, b
}

func pauseEvent(on bool) mpv.Event {
	data, _ := json.Marshal(on)
	return mpv.Event{Name: "property-change", Property: "pause", Data: data}
}

func TestPreloadStartsWithinTheLeadOfTheEnd(t *testing.T) {
	p, a, b := newTestPlayer2(t)
	_ = p.QueueMany(bg, []string{"a", "b"})
	a.props["time-pos"], a.props["duration"] = 100, 300 // 200 s left
	p.poll(bg)
	eq(t, "far from the end", b.take(), nil)

	nearEnd(a)
	p.poll(bg)
	eq(t, "near the end", b.take(), []string{"set_property pause true", "loadfile b replace"})
	p.poll(bg)
	eq(t, "no second preload", b.take(), nil)
}

func TestNoPreloadOnTheLastEntryOrWhenIdle(t *testing.T) {
	p, a, b := newTestPlayer2(t)
	p.poll(bg) // idle
	_ = p.Queue(bg, "a")
	nearEnd(a)
	p.poll(bg) // nothing after the playing song
	eq(t, "commands", b.take(), nil)
}

func TestHandoffUnpausesThePreloadedDeckWithoutReloading(t *testing.T) {
	p, a, b := preloaded(t)
	a.endFile("eof", 1)
	eq(t, "events", waitEvents(t, p, 3), state("a,b*,c", 1))
	eq(t, "new deck", b.take(), []string{"set_property pause false"})
	eq(t, "old deck", a.take(), []string{"stop"}) // no loadfile of b on it

	// The song after that is now preloaded on the deck that became idle.
	nearEnd(b)
	p.poll(bg)
	eq(t, "next preload", a.take(), []string{"set_property pause true", "loadfile c replace"})
}

func TestHandoffReportsTheNewDeckAsTheActiveOne(t *testing.T) {
	p, a, b := preloaded(t)
	a.endFile("eof", 1)
	waitEvents(t, p, 3)
	// From now on the position comes from the deck that took over.
	b.props["time-pos"], b.props["duration"] = 1.5, 180
	elapsed, total, err := p.Position(bg)
	if err != nil || elapsed != 1.5 || total != 180 {
		t.Errorf("Position = %v, %v, %v", elapsed, total, err)
	}
	// Its end-file (entry 1 on deck b) advances the queue; deck a's old ids do not.
	a.endFile("eof", 1)
	quiet(t, p)
	b.endFile("eof", 1)
	eq(t, "events", waitEvents(t, p, 3), state("a,b,c*", 2))
}

func TestSkipUsesThePreload(t *testing.T) {
	p, a, b := preloaded(t)
	if err := p.Skip(bg); err != nil {
		t.Fatal(err)
	}
	eq(t, "events", drain(p), state("a,b*,c", 1))
	eq(t, "new deck", b.take(), []string{"set_property pause false"})
	eq(t, "old deck", a.take(), []string{"stop"})
}

func TestPreloadIsDroppedWhenTheNextSongChanges(t *testing.T) {
	for _, tt := range []struct {
		name    string
		act     func(p *Player) error
		current []string // commands on deck a caused by the action itself
		then    []string // deck b after the next poll
	}{
		{"remove the next song", func(p *Player) error { return p.Remove(bg, 1) }, nil,
			[]string{"set_property pause true", "loadfile c replace"}},
		{"move the next song down", func(p *Player) error { return p.MoveDown(bg, 1) }, nil,
			[]string{"set_property pause true", "loadfile c replace"}},
		{"move the song after it up", func(p *Player) error { return p.MoveUp(bg, 2) }, nil,
			[]string{"set_property pause true", "loadfile c replace"}},
		{"play something now", func(p *Player) error { return p.PlayNow(bg, "n") }, []string{"loadfile n replace"},
			[]string{"set_property pause true", "loadfile b replace"}},
		{"clear the queue", func(p *Player) error { return p.ClearQueue(bg) }, nil, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, a, b := preloaded(t)
			if err := tt.act(p); err != nil {
				t.Fatal(err)
			}
			eq(t, "idle deck stopped", b.take(), []string{"stop"})
			eq(t, "active deck", a.take(), tt.current)
			p.poll(bg)
			eq(t, "after the next poll", b.take(), tt.then)
		})
	}
}

func TestStalePreloadIsNeverPlayed(t *testing.T) {
	p, a, b := preloaded(t)
	if err := p.Remove(bg, 1); err != nil { // the preloaded b is gone
		t.Fatal(err)
	}
	drain(p)
	a.take()
	b.take()
	a.endFile("eof", 1)
	eq(t, "events", waitEvents(t, p, 3), state("a,c*", 1))
	eq(t, "c is loaded cold", a.takeAfter(t, "loadfile c replace"), []string{"loadfile c replace"})
	eq(t, "deck b was not unpaused", b.take(), nil)
}

func TestHandoffKeepsTheUsersPause(t *testing.T) {
	p, a, b := preloaded(t)
	a.events <- pauseEvent(true) // mpv reports the pause the user asked for
	eq(t, "pause event", waitEvents(t, p, 1), []string{"pause=true"})
	if err := p.Skip(bg); err != nil {
		t.Fatal(err)
	}
	eq(t, "new deck", b.take(), []string{"set_property pause true"})
}

func TestFailedPreloadFallsBackToAColdLoad(t *testing.T) {
	t.Run("loadfile is rejected", func(t *testing.T) {
		p, a, b := newTestPlayer2(t)
		b.failLoad["b"] = true
		_ = p.QueueMany(bg, []string{"a", "b", "c"})
		nearEnd(a)
		p.poll(bg)
		eq(t, "attempt", b.take(), []string{"set_property pause true", "loadfile b replace"})
		p.poll(bg)
		eq(t, "no retry", b.take(), nil)
		a.take()
		drain(p)
		a.endFile("eof", 1)
		eq(t, "events", waitEvents(t, p, 3), state("a,b*,c", 1))
		eq(t, "cold load", a.takeAfter(t, "loadfile b replace"), []string{"loadfile b replace"})
	})
	t.Run("the preloaded file fails to open", func(t *testing.T) {
		p, a, b := preloaded(t)
		b.endFile("error", 1) // deck b's first (and only) file
		quiet(t, p)           // the UI hears nothing about a song that is not playing
		p.poll(bg)
		eq(t, "no retry", b.take(), nil)
		a.endFile("eof", 1)
		eq(t, "events", waitEvents(t, p, 3), state("a,b*,c", 1))
		eq(t, "cold load", a.takeAfter(t, "loadfile b replace"), []string{"loadfile b replace"})
	})
}

func TestPauseEventsOfTheIdleDeckAreNotForwarded(t *testing.T) {
	p, _, b := preloaded(t)
	b.events <- pauseEvent(true)
	quiet(t, p)
}

func TestSecondDeckFailingToStartDisablesPreloadForGood(t *testing.T) {
	a := newFakeDeck()
	starts := map[string]int{}
	p, err := New(bg, func(ctx context.Context, name string) (Deck, error) {
		starts[name]++
		if name == "a" {
			return a, nil
		}
		return nil, errors.New("cannot open the sound device")
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	_ = p.QueueMany(bg, []string{"a", "b", "c"})
	nearEnd(a)
	p.poll(bg)
	p.poll(bg)
	if err := p.Skip(bg); err != nil { // playback carries on with one deck
		t.Fatal(err)
	}
	p.poll(bg) // a new song, again near its end
	if starts["b"] != 1 {
		t.Errorf("deck b was started %d times, want 1 attempt", starts["b"])
	}
	eq(t, "cold load", a.take(), []string{"loadfile a replace", "loadfile b replace"})
}

func TestIdleDeckDyingDisablesPreload(t *testing.T) {
	p, a, b := preloaded(t)
	b.exit()
	time.Sleep(80 * time.Millisecond) // the player notices
	b.take()
	a.endFile("eof", 1)
	eq(t, "events", waitEvents(t, p, 3), state("a,b*,c", 1))
	eq(t, "cold load on the surviving deck", a.takeAfter(t, "loadfile b replace"), []string{"loadfile b replace"})
	eq(t, "the dead deck is left alone", b.take(), nil)
	nearEnd(a)
	p.poll(bg)
	eq(t, "no more preloads", b.take(), nil)
}

func TestActiveDeckDyingClosesDone(t *testing.T) {
	p, a, b := preloaded(t)
	a.endFile("eof", 1) // deck b takes over
	waitEvents(t, p, 3)
	select {
	case <-p.Done():
		t.Fatal("Done closed while the active deck is alive")
	default:
	}
	b.exit()
	select {
	case <-p.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done did not close when the active deck exited")
	}
}

func TestReorderUpcomingDropsAStalePreload(t *testing.T) {
	p, a, b := preloaded(t) // b is preloaded
	if err := p.ReorderUpcoming(bg, []string{"c", "b"}); err != nil {
		t.Fatal(err)
	}
	eq(t, "idle deck stopped", b.take(), []string{"stop"})
	p.poll(bg)
	eq(t, "the new next song is preloaded", b.take(), []string{"set_property pause true", "loadfile c replace"})
	_ = a
}
