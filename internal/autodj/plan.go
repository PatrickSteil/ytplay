// Package autodj plans how one song hands over to the next: a beat-matched
// overlap when the beats at both ends are clear, a plain crossfade otherwise.
// The planning is pure (analyses in, numbers out); the player carries a plan
// out on its decks, and Render mixes one offline so it can be heard.
package autodj

import (
	"fmt"
	"math"

	"github.com/PatrickSteil/ytplay/internal/analyze"
)

const (
	// Beats is how many beats (4 bars) the two songs of a beat-matched
	// transition play together.
	Beats = 16
	// MaxTempoGap is the largest difference between the two songs' tempi, as a
	// fraction, that is still closed by changing the speed of the next one.
	MaxTempoGap = 0.06
	// FadeSeconds is the length of a plain crossfade (shorter for a song that
	// is itself short).
	FadeSeconds = 6.0
	// EaseSeconds is how long the next song takes to return to its own speed
	// once the beat-matched overlap is over.
	EaseSeconds = 8.0
	// LastSeconds is how far from the end of the playing song's music a
	// beat-matched overlap may start: it lies inside the last 30 s, on the beat.
	LastSeconds = 30.0

	shortSong = 15.0 // a song with less audible music than this gets a shorter fade
)

// Song is what planning needs to know about a song. Edges is the zero value if
// the song has not been analysed.
type Song struct {
	Length float64 // seconds
	Edges  analyze.Edges
	// Loudness (LUFS, 0 if unknown) and Peak (largest sample, 1 is full scale)
	// say how loud the song is, for MatchGain.
	Loudness, Peak float64
}

// Kind is the sort of transition.
type Kind int

const (
	Crossfade Kind = iota // the next song fades in while the playing one fades out
	BeatMatch             // the same, with the next song sped up or slowed down so the beats coincide
)

func (k Kind) String() string {
	if k == BeatMatch {
		return "beat-match"
	}
	return "crossfade"
}

// Plan says when and how the next song starts. Times are seconds.
type Plan struct {
	Kind Kind
	// AStart is the position in the playing song at which the next one starts
	// to sound, BFrom the position in the next song it starts from.
	AStart, BFrom float64
	// Overlap is how long both sound (in seconds of the playing song), ASpeed the
	// speed of the playing song and BSpeed that of the next one during that time
	// (both 1 for a crossfade) and EaseBack how long the next song then takes to
	// return to speed 1 (0 for none). The gap between the two tempi is shared:
	// each song changes its speed by about half of it, which is less to hear.
	// ASpeed is 0 in a Plan nobody filled it in for: read it with PlayingSpeed.
	Overlap, ASpeed, BSpeed, EaseBack float64
	// GainB is the gain, in dB, the next song is played with so that it is as
	// loud as the playing one (MatchGain; the player fills it in, planning from
	// two songs' beats does not know what the decks are doing).
	GainB float64
	// Why says in words why the plan is what it is.
	Why string
}

// PlayingSpeed is the speed of the playing song during the overlap.
func (p Plan) PlayingSpeed() float64 {
	if p.ASpeed == 0 {
		return 1
	}
	return p.ASpeed
}

// Match is the speed the next song would have to play at for its beats to meet
// those of the playing song at normal speed: BSpeed relative to PlayingSpeed. Of
// the two, the next song covers Match times as much of its own time as the
// playing song covers of its.
func (p Plan) Match() float64 { return p.BSpeed / p.PlayingSpeed() }

func (s Song) analysed() bool { return s.Edges.AudioEnd > 0 }

func (s Song) audibleEnd() float64 {
	if s.Edges.AudioEnd > 0 {
		return s.Edges.AudioEnd
	}
	return s.Length
}

// For plans the transition from the playing song a to the next song b.
func For(a, b Song) Plan {
	if !a.analysed() || !b.analysed() {
		return crossfade(a, b, "crossfade: the songs are not analysed yet")
	}
	end, start := a.Edges.End, b.Edges.Start
	switch {
	case !end.Sure || end.Period <= 0:
		return crossfade(a, b, "crossfade: the end of the playing song has no beat that can be relied on")
	case !start.Sure || start.Period <= 0:
		return crossfade(a, b, "crossfade: the start of the next song has no beat that can be relied on")
	}
	speed, feel := matchSpeed(start.Period, end.Period)
	if gap := math.Abs(speed - 1); gap > MaxTempoGap {
		return crossfade(a, b, fmt.Sprintf("crossfade: the tempo differs by %.1f%% (at most %.0f%% is matched, also in double or half time)", gap*100, MaxTempoGap*100))
	}
	aStart := end.Anchor - Beats*end.Period
	if aStart < a.Edges.AudioStart {
		return crossfade(a, b, fmt.Sprintf("crossfade: the playing song's steady beat ends before %d beats are left", Beats))
	}
	// A song shorter than the window has no 30 s before its end: the window then
	// opens at its start, which the check above already holds aStart to.
	if aStart < a.audibleEnd()-LastSeconds {
		return crossfade(a, b, fmt.Sprintf("crossfade: %d beats of the playing song's steady beat do not fit in its last %.0f s", Beats, LastSeconds))
	}
	// The overlap lasts until the playing song has sounded its last beat out,
	// or until it ends if that comes first.
	overlap := math.Min(a.audibleEnd(), end.Anchor+end.Period) - aStart
	if start.Anchor+overlap*speed > b.audibleEnd() {
		return crossfade(a, b, "crossfade: the next song is too short for the overlap")
	}
	ease := 0.0
	if math.Abs(speed-1) > 1e-3 {
		ease = EaseSeconds
	}
	// Share the gap: the next song plays at sqrt(speed) and the playing one at
	// 1/sqrt(speed), whose ratio is still speed, so the beats still meet.
	half := math.Sqrt(speed)
	why := fmt.Sprintf("beat-matched over %d beats%s, next song at %.1f%% speed", Beats, feel, speed*100)
	if ease > 0 {
		why = fmt.Sprintf("beat-matched over %d beats%s, playing song at %.1f%% and next song at %.1f%% speed", Beats, feel, 100/half, 100*half)
	}
	return Plan{
		Kind: BeatMatch, AStart: aStart, BFrom: start.Anchor, Overlap: overlap,
		ASpeed: 1 / half, BSpeed: half, EaseBack: ease, Why: why,
	}
}

// matchSpeed is the speed at which the next song, whose beats are nextPeriod
// apart, must play for its beats to fall on the beats of the playing song, whose
// beats are playingPeriod apart. That is one beat to one beat, or, for a next
// song with twice the beats (140 against 70 BPM), two to one, or, for one with
// half the beats, one to two: whichever needs the smallest change of speed. 70
// and 140 BPM are not 100% apart, they are not apart at all. feel says in words
// (for a plan's Why) when the match is not one to one.
func matchSpeed(nextPeriod, playingPeriod float64) (speed float64, feel string) {
	speed = nextPeriod / playingPeriod
	if double := 2 * nextPeriod / playingPeriod; math.Abs(double-1) < math.Abs(speed-1) {
		speed, feel = double, " (double time)"
	}
	if half := nextPeriod / (2 * playingPeriod); math.Abs(half-1) < math.Abs(speed-1) {
		speed, feel = half, " (half time)"
	}
	return speed, feel
}

// crossfade is the plan used whenever beat-matching is not warranted: the next
// song starts FadeSeconds before the music of the playing one ends, at normal
// speed, from its first audible moment.
func crossfade(a, b Song, why string) Plan {
	end := a.audibleEnd()
	fade := math.Min(FadeSeconds, 0.4*(end-a.Edges.AudioStart))
	if end-a.Edges.AudioStart >= shortSong {
		fade = FadeSeconds
	}
	return Plan{Kind: Crossfade, AStart: end - fade, BFrom: b.Edges.AudioStart, Overlap: fade, BSpeed: 1, Why: why}
}
