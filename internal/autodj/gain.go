package autodj

import "math"

const (
	// MaxCut and MaxBoost are the most (in dB) that the next song is turned down
	// or up to match the playing one: more than that is no longer the same song
	// mixed at the same level but a different dynamic range.
	MaxCut   = 12.0
	MaxBoost = 6.0
	// PeakHeadroom is how far (dB) below full scale the loudest sample of a
	// boosted song is kept.
	PeakHeadroom = 0.5
)

// MatchGain is the gain, in dB, that the next song b is played with so that it
// is as loud as the playing song a sounds. gainA is the gain a itself is played
// with (it is turned down if the song before it was louder, say), so a sounds
// Loudness+gainA. The result is limited to MaxCut and MaxBoost, and a boost
// never brings b's loudest sample over full scale. It is 0 if either loudness
// is unknown: the song is then played as it is.
func MatchGain(a, b Song, gainA float64) float64 {
	if a.Loudness == 0 || b.Loudness == 0 {
		return 0
	}
	gain := a.Loudness + gainA - b.Loudness
	limit := MaxBoost
	if b.Peak > 0 {
		limit = math.Min(limit, -20*math.Log10(b.Peak)-PeakHeadroom)
	}
	return math.Max(-MaxCut, math.Min(gain, math.Max(limit, 0)))
}
