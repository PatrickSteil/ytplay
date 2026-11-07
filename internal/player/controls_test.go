package player

import (
	"math"
	"testing"
)

func TestSeekToAndSeekBy(t *testing.T) {
	p, a := newTestPlayer(t)
	if err := p.SeekTo(bg, 30); err != nil || len(a.take()) != 0 {
		t.Fatalf("seeking with nothing playing must do nothing and not fail (err %v)", err)
	}
	_ = p.Queue(bg, "a")
	a.take()
	if err := p.SeekTo(bg, 90.5); err != nil {
		t.Fatal(err)
	}
	eq(t, "absolute", a.take(), []string{"seek 90.5 absolute"})
	if err := p.SeekBy(bg, -5); err != nil {
		t.Fatal(err)
	}
	eq(t, "relative back", a.take(), []string{"seek -5 relative"})
	if err := p.SeekBy(bg, 10); err != nil {
		t.Fatal(err)
	}
	eq(t, "relative forward", a.take(), []string{"seek 10 relative"})
}

func TestPrevGoesBackWhenTheSongHasJustStarted(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.QueueMany(bg, []string{"a", "b", "c"})
	_ = p.Skip(bg) // b plays
	a.take()
	drain(p)
	a.set("time-pos", 1.0)
	if err := p.Prev(bg); err != nil {
		t.Fatal(err)
	}
	eq(t, "commands", a.take(), []string{"loadfile a replace"})
	eq(t, "events", drain(p), state("a*,b,c", 0))
}

func TestPrevRestartsASongThatHasPlayedForAWhile(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.QueueMany(bg, []string{"a", "b", "c"})
	_ = p.Skip(bg)
	a.take()
	drain(p)
	a.set("time-pos", 10)
	if err := p.Prev(bg); err != nil {
		t.Fatal(err)
	}
	eq(t, "commands", a.take(), []string{"seek 0 absolute"})
	quiet(t, p) // the queue did not move
}

func TestPrevOnTheFirstSongRestartsItAndWhenIdleDoesNothing(t *testing.T) {
	p, a := newTestPlayer(t)
	if err := p.Prev(bg); err != nil || len(a.take()) != 0 {
		t.Fatalf("Prev with nothing playing: err %v, commands %q", err, a.snapshot())
	}
	_ = p.QueueMany(bg, []string{"a", "b"})
	a.take()
	drain(p)
	a.set("time-pos", 0.5)
	if err := p.Prev(bg); err != nil {
		t.Fatal(err)
	}
	eq(t, "commands", a.take(), []string{"seek 0 absolute"})
	quiet(t, p)
}

func TestSetUpcomingReplacesTheSongsAfterThePlayingOne(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.QueueMany(bg, []string{"a", "b", "c", "d"})
	a.take()
	drain(p)
	if err := p.SetUpcoming(bg, []string{"d", "x", "b"}); err != nil {
		t.Fatal(err)
	}
	eq(t, "commands", a.take(), nil)
	eq(t, "events", drain(p), state("a*,d,x,b", 0))
	if err := p.SetUpcoming(bg, nil); err != nil {
		t.Fatal(err)
	}
	eq(t, "events after emptying it", drain(p), state("a*", 0))
	if err := p.Skip(bg); err != nil { // nothing is left after a
		t.Fatal(err)
	}
	eq(t, "skip with nothing upcoming", drain(p), state("a", -1))
}

func TestSetUpcomingKeepsTheSongsBeforeThePlayingOne(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.QueueMany(bg, []string{"a", "b", "c"})
	_ = p.Skip(bg) // b plays, a is behind it
	a.take()
	drain(p)
	if err := p.SetUpcoming(bg, []string{"z"}); err != nil {
		t.Fatal(err)
	}
	eq(t, "events", drain(p), state("a,b*,z", 1))
}

func TestSetUpcomingNeedsSomethingPlayingUnlessItIsEmpty(t *testing.T) {
	p, _ := newTestPlayer(t)
	if err := p.SetUpcoming(bg, []string{"x"}); err == nil {
		t.Error("with nothing playing there is no upcoming list to set")
	}
	if err := p.SetUpcoming(bg, nil); err != nil {
		t.Errorf("emptying the upcoming list of nothing is fine: %v", err)
	}
}

func TestSetVolumeSetsTheDecksAndClamps(t *testing.T) {
	p, a := newTestPlayer(t)
	if v := p.Volume(); v != 100 {
		t.Errorf("the volume starts at %v, want 100", v)
	}
	// with nothing playing it is remembered and set, for the next song
	if err := p.SetVolume(bg, 30); err != nil {
		t.Fatal(err)
	}
	eq(t, "idle", a.take(), []string{"set_property volume 30"})
	for in, want := range map[float64]float64{150: 100, -5: 0, 55.5: 55.5} {
		_ = p.SetVolume(bg, in)
		a.take()
		if got := p.Volume(); got != want {
			t.Errorf("SetVolume(%v): Volume() = %v, want %v", in, got, want)
		}
	}
}

func TestAFreshSecondDeckStartsAtTheMasterVolume(t *testing.T) {
	p, a, b := newTestPlayer2(t)
	_ = p.QueueMany(bg, []string{"a", "b", "c"})
	_ = p.SetVolume(bg, 40)
	nearEnd(a)
	p.poll(bg) // preloads b: the deck is started now
	waitCalls(t, "b loaded", b, func(c []string) bool { return has(c, "loadfile b replace") })
	if v := lastVolume(b.snapshot()); v != 40 {
		t.Errorf("deck b's volume = %v, want the master volume 40: %q", v, b.snapshot())
	}
	// the master volume also reaches a deck that already exists
	_ = p.SetVolume(bg, 70)
	if v := lastVolume(b.snapshot()); v != 70 {
		t.Errorf("deck b's volume after changing it = %v, want 70", v)
	}
	if v := lastVolume(a.snapshot()); v != 70 {
		t.Errorf("deck a's volume = %v, want 70", v)
	}
	// and it is still there after the handoff to b
	if err := p.Skip(bg); err != nil {
		t.Fatal(err)
	}
	if v := lastVolume(b.snapshot()); v != 70 {
		t.Errorf("after the handoff deck b's volume = %v, want 70", v)
	}
}

// The crossfade is scaled by the master volume (the ramp's volumes times the
// master, which multiplies mpv's cubic gains) and ends at the master volume,
// never at full.
func TestTheMasterVolumeScalesTheCrossfadeAndEndsAtTheMaster(t *testing.T) {
	p, a, b := armed(t, nil)
	if err := p.SetVolume(bg, 50); err != nil {
		t.Fatal(err)
	}
	waitCalls(t, "the playing deck follows the master", a, func(c []string) bool { return lastVolume(c) == 50 })
	if v := lastVolume(b.snapshot()); v != 0 {
		t.Fatalf("the silent next deck must be left alone, got volume %v", v)
	}
	a.set("time-pos", 193.2)
	p.poll(bg)
	walk(a, 193.2, 197) // halfway through the overlap
	want := math.Sqrt(0.5) * math.Pow(0.5, 3)
	waitCalls(t, "b at the midpoint", b, func(c []string) bool { return math.Abs(gainOf(lastVolume(c))-want) < 0.003 })
	waitCalls(t, "a at the midpoint", a, func(c []string) bool { return math.Abs(gainOf(lastVolume(c))-want) < 0.003 })
	// changing the volume during the overlap is picked up by the ramp
	if err := p.SetVolume(bg, 100); err != nil {
		t.Fatal(err)
	}
	a.set("time-pos", 197.1)
	waitCalls(t, "b at full master", b, func(c []string) bool { return math.Abs(gainOf(lastVolume(c))-math.Sqrt(0.5)) < 0.02 })
	_ = p.SetVolume(bg, 50)
	drain(p)
	a.set("time-pos", 200.05)
	waitEvents(t, p, 3)
	waitCalls(t, "b ends at the master volume", b, func(c []string) bool { return lastVolume(c) == 50 })
	waitCalls(t, "a is restored to the master volume", a, func(c []string) bool { return has(c, "stop") && lastVolume(c) == 50 })
}

// A song already preloaded for the one after the playing song is no longer the
// next one once Prev has gone back. If it stayed, nothing would preload the
// right song (a preload in place blocks the next one), and the handoff to it
// would have to load it the slow way.
func TestPrevDropsTheStalePreload(t *testing.T) {
	p, a, b := newTestPlayer2(t)
	_ = p.QueueMany(bg, []string{"a", "b", "c"})
	nearEnd(a)
	p.poll(bg) // preloads b on deck b
	waitCalls(t, "b preloaded", b, func(c []string) bool { return has(c, "loadfile b replace") })
	if err := p.Skip(bg); err != nil { // handoff: b plays
		t.Fatal(err)
	}
	nearEnd(b)
	p.poll(bg) // preloads c on deck a
	waitCalls(t, "c preloaded", a, func(c []string) bool { return has(c, "loadfile c replace") })
	drain(p)
	b.set("time-pos", 1.0)
	if err := p.Prev(bg); err != nil {
		t.Fatal(err)
	}
	eq(t, "events after going back", drain(p), state("a*,b,c", 0))
	waitCalls(t, "the stale preload is stopped", a, func(c []string) bool { return countCalls(c, "stop") > 0 })
	nearEnd(b) // the song that plays again is nearly over: the right song is preloaded
	p.poll(bg)
	waitCalls(t, "b is preloaded as the song after a", a, func(c []string) bool { return has(c, "loadfile b replace") })
	if err := p.Skip(bg); err != nil {
		t.Fatal(err)
	}
	eq(t, "and it is the one that follows", drain(p), state("a,b*,c", 1))
}
