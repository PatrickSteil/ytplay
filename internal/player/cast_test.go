package player

import (
	"strings"
	"testing"
	"time"
)

// castPlaying is a queue a, b, c playing on deck a at 42 s of 200, and a fake
// cast deck to move it to.
func castPlaying(t *testing.T) (p *Player, a, b, cd *fakeDeck) {
	t.Helper()
	p, a, b = newTestPlayer2(t)
	if err := p.QueueMany(bg, []string{"a", "b", "c"}); err != nil {
		t.Fatal(err)
	}
	a.props["time-pos"], a.props["duration"] = 42, 200
	cd = newFakeDeck()
	drain(p)
	a.take()
	return p, a, b, cd
}

func TestCastingMovesTheSongToTheSpeakerWhereItWas(t *testing.T) {
	p, a, _, cd := castPlaying(t)
	if err := p.CastTo(bg, "Wohnzimmer", cd); err != nil {
		t.Fatal(err)
	}
	eq(t, "the speaker", cd.take(), []string{"loadfile a replace", "seek 42 absolute"})
	eq(t, "the local deck is paused, not stopped", a.take(), []string{"set_property pause true"})
	eq(t, "events", drain(p), []string{"cast:Wohnzimmer"})
	select {
	case <-p.Done():
		t.Error("the player says it is done")
	default:
	}
}

func TestCastingWhileIdleLoadsNothingUntilThereIsASong(t *testing.T) {
	p, a, _ := newTestPlayer2(t)
	cd := newFakeDeck()
	if err := p.CastTo(bg, "Wohnzimmer", cd); err != nil {
		t.Fatal(err)
	}
	eq(t, "the speaker", cd.take(), nil)
	drain(p)
	_ = p.QueueMany(bg, []string{"a", "b"})
	eq(t, "the speaker plays the queue", cd.take(), []string{"loadfile a replace"})
	eq(t, "the local deck", a.take(), nil)
}

func TestPausedStaysPausedOnTheSpeaker(t *testing.T) {
	p, a, _, cd := castPlaying(t)
	a.events <- pauseEvent(true)
	time.Sleep(50 * time.Millisecond)
	a.take()
	_ = p.CastTo(bg, "Wohnzimmer", cd)
	eq(t, "the speaker", cd.take(), []string{"loadfile a replace", "seek 42 absolute", "set_property pause true"})
}

func TestTheSongEndingOnTheSpeakerPlaysTheNextOneThere(t *testing.T) {
	p, _, _, cd := castPlaying(t)
	_ = p.CastTo(bg, "Wohnzimmer", cd)
	cd.take()
	drain(p)
	cd.endFile("eof", 1)
	eq(t, "events", waitEvents(t, p, 3), state("a,b*,c", 1))
	eq(t, "the speaker", cd.take(), []string{"loadfile b replace"})
}

func TestNothingIsPreloadedOrMixedWhileCasting(t *testing.T) {
	p, a, b, cd := castPlaying(t)
	_ = p.CastTo(bg, "Wohnzimmer", cd)
	cd.take()
	cd.props["time-pos"], cd.props["duration"] = 190, 200
	p.poll(bg)
	eq(t, "the speaker", cd.take(), nil)
	eq(t, "the idle local deck", b.take(), nil)
	eq(t, "the local deck", a.take(), []string{"set_property pause true"})
}

func TestTheVolumeAndPauseGoToTheSpeaker(t *testing.T) {
	p, _, _, cd := castPlaying(t)
	_ = p.CastTo(bg, "Wohnzimmer", cd)
	cd.take()
	_ = p.SetVolume(bg, 40)
	_ = p.TogglePause(bg)
	_ = p.SeekTo(bg, 10)
	got := cd.take()
	for _, want := range []string{"set_property volume 40", "cycle pause", "seek 10 absolute"} {
		found := false
		for _, g := range got {
			found = found || g == want
		}
		if !found {
			t.Errorf("the speaker was not told %q: %v", want, got)
		}
	}
}

func TestCastOffGoesBackToTheLocalDeckAtThePositionOfTheSpeaker(t *testing.T) {
	p, a, _, cd := castPlaying(t)
	_ = p.CastTo(bg, "Wohnzimmer", cd)
	cd.take()
	a.take()
	drain(p)
	cd.props["time-pos"], cd.props["duration"] = 77.5, 200
	if err := p.CastOff(bg); err != nil {
		t.Fatal(err)
	}
	got := a.take()
	if len(got) == 0 || got[0] != "loadfile a replace -1 start=77.5" {
		t.Errorf("local deck: %v, want it to load the song at 77.5 s", got)
	}
	if !cd.closed {
		t.Error("the speaker's deck was not closed")
	}
	eq(t, "events", drain(p), []string{"cast:"})
	_ = p.Skip(bg)
	if got := a.take(); len(got) == 0 || !strings.HasPrefix(got[len(got)-1], "loadfile b") && !strings.Contains(strings.Join(got, ","), "loadfile b") {
		t.Errorf("after casting, skip goes to the local deck: %v", got)
	}
}

func TestCastOffWithoutCastingIsAnError(t *testing.T) {
	p, _, _ := newTestPlayer2(t)
	if err := p.CastOff(bg); err == nil {
		t.Error("no error")
	}
}

func TestLosingTheSpeakerGoesBackToTheLocalDeck(t *testing.T) {
	p, a, _, cd := castPlaying(t)
	_ = p.CastTo(bg, "Wohnzimmer", cd)
	a.take()
	drain(p)
	cd.props["time-pos"] = 60
	cd.exit()
	got := waitEvents(t, p, 1)
	if got[0] != "cast:lost" {
		t.Errorf("events %v", got)
	}
	if calls := a.take(); len(calls) == 0 || calls[0] != "loadfile a replace -1 start=60" {
		t.Errorf("local deck: %v", calls)
	}
	select {
	case <-p.Done():
		t.Error("losing a speaker ended the program")
	default:
	}
}

func TestSwitchingToAnotherSpeakerContinuesThere(t *testing.T) {
	p, _, _, cd := castPlaying(t)
	_ = p.CastTo(bg, "Wohnzimmer", cd)
	cd.take()
	cd.props["time-pos"], cd.props["duration"] = 90, 200
	cd2 := newFakeDeck()
	if err := p.CastTo(bg, "Kitchen speaker", cd2); err != nil {
		t.Fatal(err)
	}
	eq(t, "the new speaker", cd2.take(), []string{"loadfile a replace", "seek 90 absolute"})
	if !cd.closed {
		t.Error("the first speaker was kept")
	}
	cd.endFile("eof", 1) // a late report of the speaker that was left
	time.Sleep(80 * time.Millisecond)
	eq(t, "nothing is done for it", cd2.take(), nil)
	select {
	case <-p.Done():
		t.Error("the player says it is done")
	default:
	}
}

func TestClosingAnIdleDeckNoLongerEndsThePlayer(t *testing.T) {
	// the deck that was preloaded is closed when casting starts, and the slot is
	// taken by the speaker: its exit must not look like the active deck's
	p, a, b := newTestPlayer2(t)
	_ = p.QueueMany(bg, []string{"a", "b"})
	nearEnd(a)
	p.poll(bg)
	b.take()
	cd := newFakeDeck()
	_ = p.CastTo(bg, "Wohnzimmer", cd)
	time.Sleep(100 * time.Millisecond)
	select {
	case <-p.Done():
		t.Error("closing the preloaded deck ended the player")
	default:
	}
}

func TestPreloadingWorksAgainAfterCasting(t *testing.T) {
	p, a, b, cd := castPlaying(t)
	_ = p.CastTo(bg, "Wohnzimmer", cd)
	_ = p.CastOff(bg)
	a.take()
	nearEnd(a)
	p.poll(bg)
	eq(t, "the next song is preloaded again", b.take(), []string{"set_property pause true", "loadfile b replace"})
}

func TestTheSpeakerGetsTheVolumeBeforeAnySong(t *testing.T) {
	p, _, _, cd := castPlaying(t)
	_ = p.SetVolume(bg, 8)
	if err := p.CastTo(bg, "Wohnzimmer", cd); err != nil {
		t.Fatal(err)
	}
	eq(t, "the speaker", cd.take(), []string{"set_property volume 8", "loadfile a replace", "seek 42 absolute"})

	idle, _, _ := newTestPlayer2(t)
	_ = idle.SetVolume(bg, 8)
	cd2 := newFakeDeck()
	_ = idle.CastTo(bg, "Wohnzimmer", cd2)
	eq(t, "an idle player still sets the volume", cd2.take(), []string{"set_property volume 8"})

	_ = p.SetVolume(bg, 8)
	cd3 := newFakeDeck()
	cd.take()
	_ = p.CastTo(bg, "Kitchen speaker", cd3) // moving to another speaker too
	eq(t, "the next speaker", cd3.take(), []string{"set_property volume 8", "loadfile a replace"})
}
