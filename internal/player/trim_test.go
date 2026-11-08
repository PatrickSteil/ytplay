package player

import (
	"strings"
	"testing"

	"github.com/PatrickSteil/ytplay/internal/analyze"
	"github.com/PatrickSteil/ytplay/internal/autodj"
)

// known is what the analyses say about songs: length, where the sound starts
// and where it ends.
func known(m map[string][3]float64) Analyses {
	return func(url string) (autodj.Song, bool) {
		v, ok := m[url]
		if !ok {
			return autodj.Song{}, false
		}
		return autodj.Song{Length: v[0], Edges: analyze.Edges{AudioStart: v[1], AudioEnd: v[2]}}, true
	}
}

func seeks(cmds []string) []string {
	var out []string
	for _, c := range cmds {
		if strings.HasPrefix(c, "seek ") {
			out = append(out, c)
		}
	}
	return out
}

func TestSilenceAtTheStartIsSkippedOnce(t *testing.T) {
	p, a, _ := newTestPlayer2(t)
	p.SetTrim(known(map[string][3]float64{"a": {200, 4, 190}}))
	_ = p.QueueMany(bg, []string{"a", "b"})
	a.take()
	a.props["time-pos"], a.props["duration"] = 0.4, 200
	p.poll(bg)
	eq(t, "seek past the silence", seeks(a.take()), []string{"seek 3.95 absolute"})
	a.props["time-pos"] = 4
	p.poll(bg)
	p.poll(bg)
	eq(t, "not again", seeks(a.take()), nil)
}

func TestShortSilenceAtTheStartIsLeftAlone(t *testing.T) {
	p, a, _ := newTestPlayer2(t)
	p.SetTrim(known(map[string][3]float64{"a": {200, 0.6, 190}}))
	_ = p.QueueMany(bg, []string{"a", "b"})
	a.take()
	a.props["time-pos"], a.props["duration"] = 0.1, 200
	p.poll(bg)
	eq(t, "seeks", seeks(a.take()), nil)
}

func TestAPlayerThatStartedLateDoesNotSeekBackwards(t *testing.T) {
	p, a, _ := newTestPlayer2(t)
	p.SetTrim(known(map[string][3]float64{"a": {200, 4, 190}}))
	_ = p.QueueMany(bg, []string{"a", "b"})
	a.take()
	a.props["time-pos"], a.props["duration"] = 30, 200 // the analysis arrived late
	p.poll(bg)
	eq(t, "seeks", seeks(a.take()), nil)
}

func TestEachSongIsTrimmedAtItsOwnStart(t *testing.T) {
	p, a, _ := newTestPlayer2(t)
	p.SetTrim(known(map[string][3]float64{"a": {200, 4, 190}, "b": {200, 6, 190}}))
	_ = p.QueueMany(bg, []string{"a", "b"})
	a.take()
	a.props["time-pos"], a.props["duration"] = 0.2, 200
	p.poll(bg)
	eq(t, "first", seeks(a.take()), []string{"seek 3.95 absolute"})
	_ = p.Skip(bg)
	a.take()
	a.props["time-pos"] = 0.2
	p.poll(bg)
	eq(t, "second", seeks(a.take()), []string{"seek 5.95 absolute"})
}

func TestTheSongEndsWhereTheSoundEnds(t *testing.T) {
	p, a, b := newTestPlayer2(t)
	p.SetTrim(known(map[string][3]float64{"a": {200, 0, 190}}))
	_ = p.QueueMany(bg, []string{"a", "b"})
	a.props["time-pos"], a.props["duration"] = 189, 200
	p.poll(bg) // preloads b
	drain(p)
	b.take()
	p.poll(bg)
	eq(t, "still sounding", drain(p), nil)
	a.props["time-pos"] = 190.4
	p.poll(bg)
	eq(t, "the next song starts", drain(p), state("a,b*", 1))
	eq(t, "from the preloaded deck", b.take(), []string{"set_property pause false"})
}

func TestAShortSilentTailIsNotWorthSkipping(t *testing.T) {
	p, a, _ := newTestPlayer2(t)
	p.SetTrim(known(map[string][3]float64{"a": {200, 0, 199}}))
	_ = p.QueueMany(bg, []string{"a", "b"})
	drain(p)
	a.props["time-pos"], a.props["duration"] = 199.5, 200
	p.poll(bg)
	eq(t, "events", drain(p), nil)
}

func TestNoTrimmingWhileAutoDJPlans(t *testing.T) {
	p, a, _ := newTestPlayer2(t)
	songs := known(map[string][3]float64{"a": {200, 4, 190}})
	p.SetTrim(songs)
	p.SetAutoDJ(true, songs)
	_ = p.QueueMany(bg, []string{"a", "b"})
	a.take()
	a.props["time-pos"], a.props["duration"] = 0.2, 200
	p.poll(bg)
	eq(t, "seeks", seeks(a.take()), nil)
}

func TestNothingIsTrimmedOfASongNobodyKnows(t *testing.T) {
	p, a, _ := newTestPlayer2(t)
	p.SetTrim(known(map[string][3]float64{}))
	_ = p.QueueMany(bg, []string{"a", "b"})
	drain(p)
	a.take()
	a.props["time-pos"], a.props["duration"] = 0.2, 200
	p.poll(bg)
	a.props["time-pos"] = 199.9
	p.poll(bg)
	eq(t, "events", drain(p), nil)
	eq(t, "seeks", seeks(a.take()), nil)
}

func TestAMostlySilentSongIsNotTrimmed(t *testing.T) {
	p, a, _ := newTestPlayer2(t)
	p.SetTrim(known(map[string][3]float64{"a": {200, 100, 105}})) // 5 s of sound in 200
	_ = p.QueueMany(bg, []string{"a", "b"})
	a.take()
	a.props["time-pos"], a.props["duration"] = 0.2, 200
	p.poll(bg)
	eq(t, "seeks", seeks(a.take()), nil)
}

func TestThePreloadStartsBeforeTheSoundEndsNotBeforeTheFileEnds(t *testing.T) {
	p, a, b := newTestPlayer2(t)
	p.SetTrim(known(map[string][3]float64{"a": {300, 0, 150}})) // a long silent tail
	_ = p.QueueMany(bg, []string{"a", "b"})
	a.props["time-pos"], a.props["duration"] = 100, 300
	p.poll(bg)
	eq(t, "50 s of sound left", b.take(), nil)
	a.props["time-pos"] = 140
	p.poll(bg)
	eq(t, "10 s of sound left", b.take(), []string{"set_property pause true", "loadfile b replace"})
}

func TestASeekThatHasNotLandedYetIsNotRepeated(t *testing.T) {
	p, a, _ := newTestPlayer2(t)
	p.SetTrim(known(map[string][3]float64{"a": {200, 4, 190}}))
	_ = p.QueueMany(bg, []string{"a", "b"})
	a.take()
	a.props["time-pos"], a.props["duration"] = 0.4, 200
	p.poll(bg)
	p.poll(bg) // mpv still reports the old position
	p.poll(bg)
	eq(t, "seeks", seeks(a.take()), []string{"seek 3.95 absolute"})
}

func TestTheSongRingsOutBeforeItGoesOn(t *testing.T) {
	p, a, _ := newTestPlayer2(t)
	p.SetTrim(known(map[string][3]float64{"a": {200, 0, 190}}))
	_ = p.QueueMany(bg, []string{"a", "b"})
	drain(p)
	a.props["time-pos"], a.props["duration"] = 190.1, 200
	p.poll(bg)
	eq(t, "just after the end of the sound", drain(p), nil)
}

func TestAutoDJPreloadsByTheFileNotByTheSound(t *testing.T) {
	p, a, b := newTestPlayer2(t)
	songs := known(map[string][3]float64{"a": {300, 0, 150}})
	p.SetTrim(songs)
	p.SetAutoDJ(true, songs)
	_ = p.QueueMany(bg, []string{"a", "b"})
	a.props["time-pos"], a.props["duration"] = 125, 300 // 175 s of file left, 25 s of sound
	p.poll(bg)
	eq(t, "autodj plans by itself", b.take(), nil)
}

func TestAMostlySilentSongIsPreloadedByItsFileToo(t *testing.T) {
	p, a, b := newTestPlayer2(t)
	p.SetTrim(known(map[string][3]float64{"a": {300, 100, 105}}))
	_ = p.QueueMany(bg, []string{"a", "b"})
	a.props["time-pos"], a.props["duration"] = 140, 300
	p.poll(bg)
	eq(t, "preload", b.take(), nil)
}
