package player

import (
	"strings"
	"testing"

	"github.com/PatrickSteil/ytplay/internal/autodj"
)

// loudSongs knows a (-14 LUFS) and b (-10 LUFS, 4 LU louder), without beat
// grids: the transition is a crossfade, the gain still matches.
func loudSongs(url string) (autodj.Song, bool) {
	switch url {
	case "a":
		return autodj.Song{Length: 200, Loudness: -14, Peak: 0.5}, true
	case "b":
		return autodj.Song{Length: 180, Loudness: -10, Peak: 0.5}, true
	}
	return autodj.Song{}, false
}

func gainCalls(calls []string) []string {
	var out []string
	for _, c := range calls {
		if strings.HasPrefix(c, "set_property volume-gain") {
			out = append(out, c)
		}
	}
	return out
}

func TestTheNextSongIsPreparedAtTheLoudnessOfThePlayingOne(t *testing.T) {
	_, _, b := armed(t, loudSongs)
	calls := b.snapshot()
	if got := gainCalls(calls); len(got) != 1 || got[0] != "set_property volume-gain -4" {
		t.Fatalf("gain commands = %q, want b turned down by 4 dB", got)
	}
	if has(calls, "set_property pause false") {
		t.Errorf("b must be adjusted before it sounds: %q", calls)
	}
}

func TestSongsOfUnknownLoudnessAreLeftAsTheyAre(t *testing.T) {
	_, a, b := armed(t, nil)
	if got := append(gainCalls(a.snapshot()), gainCalls(b.snapshot())...); len(got) != 0 {
		t.Errorf("gain was touched without anything known: %q", got)
	}
}

func TestTheGainStaysWithTheSongAfterTheTakeover(t *testing.T) {
	p, a, b := armed(t, loudSongs)
	a.set("time-pos", 193.2)
	p.poll(bg)
	a.set("time-pos", 197)
	waitCalls(t, "started", b, func(c []string) bool { return has(c, "set_property pause false") })
	drain(p)
	a.set("time-pos", 200.05)
	waitEvents(t, p, 3)
	waitCalls(t, "b at full volume", b, func(c []string) bool { return lastVolume(c) == 100 })
	if got := gainCalls(b.snapshot()); len(got) != 1 {
		t.Errorf("b's gain was changed again: %q", got)
	}
	p.mu.Lock()
	gain := p.gain[1]
	p.mu.Unlock()
	if gain != -4 {
		t.Errorf("deck b remembers gain %v, want -4 (so the next transition counts it)", gain)
	}
}

func TestADeckThatWasLeftWithAGainStartsTheNextSongWithout(t *testing.T) {
	p, a, b := newTestPlayer2(t)
	p.mu.Lock()
	p.gain[1] = -3
	p.mu.Unlock()
	_ = p.QueueMany(bg, []string{"a", "b"})
	a.set("duration", 200)
	a.set("time-pos", 190)
	p.poll(bg)
	if !has(b.snapshot(), "set_property volume-gain 0") {
		t.Errorf("b should be put back to no gain before it loads the next song: %q", b.snapshot())
	}
}

func TestDroppingTheNextSongPutsItsGainBack(t *testing.T) {
	p, _, b := armed(t, loudSongs)
	p.SetAutoDJ(false, nil)
	calls := waitCalls(t, "dropped", b, func(c []string) bool { return has(c, "stop") })
	got := gainCalls(calls)
	if len(got) < 2 || got[len(got)-1] != "set_property volume-gain 0" {
		t.Errorf("gain commands = %q, want the gain back at 0", got)
	}
}

func TestChangingTheMasterVolumeLeavesTheGainAlone(t *testing.T) {
	p, a, b := armed(t, loudSongs)
	_ = p.SetVolume(bg, 50)
	_ = a
	if got := gainCalls(b.snapshot()); len(got) != 1 {
		t.Errorf("gain commands = %q, want only the one from the preparation", got)
	}
}
