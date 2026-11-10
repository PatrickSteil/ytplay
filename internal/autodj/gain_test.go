package autodj

import (
	"math"
	"testing"
)

func song(lufs, peak float64) Song { return Song{Loudness: lufs, Peak: peak} }

func TestMatchGainBringsTheNextSongToTheLevelOfThePlayingOne(t *testing.T) {
	// b is 4 LU louder than a: it is turned down by 4 dB
	if got := MatchGain(song(-14, 0.5), song(-10, 0.5), 0); math.Abs(got-(-4)) > 1e-9 {
		t.Errorf("gain = %v dB, want -4", got)
	}
	// b is 3 LU quieter: turned up by 3 dB
	if got := MatchGain(song(-14, 0.5), song(-17, 0.5), 0); math.Abs(got-3) > 1e-9 {
		t.Errorf("gain = %v dB, want +3", got)
	}
}

func TestMatchGainCountsTheGainTheTheDeckAlreadyHas(t *testing.T) {
	// a plays 3 dB down (it sounds like -17), b is at -17: no change needed
	if got := MatchGain(song(-14, 0.5), song(-17, 0.5), -3); math.Abs(got) > 1e-9 {
		t.Errorf("gain = %v dB, want 0", got)
	}
}

func TestMatchGainIsLimited(t *testing.T) {
	if got := MatchGain(song(-30, 0.5), song(-4, 0.5), 0); got != -MaxCut {
		t.Errorf("a much louder song is turned down by %v dB at most, got %v", MaxCut, got)
	}
	if got := MatchGain(song(-14, 0.1), song(-30, 0.1), 0); got != MaxBoost {
		t.Errorf("a much quieter song is turned up by %v dB at most, got %v", MaxBoost, got)
	}
}

func TestMatchGainNeverPushesAPeakOverFullScale(t *testing.T) {
	// b's peak is 0.8: at most 20*log10(1/0.8) = 1.94 dB of boost (less the headroom)
	got := MatchGain(song(-10, 0.5), song(-16, 0.8), 0)
	if want := -20*math.Log10(0.8) - PeakHeadroom; math.Abs(got-want) > 1e-9 {
		t.Errorf("gain = %v dB, want %v", got, want)
	}
	// a song already at full scale is not boosted at all, but may be cut
	if got := MatchGain(song(-10, 0.5), song(-16, 1.0), 0); got > 0 {
		t.Errorf("a song with full-scale peaks was boosted by %v dB", got)
	}
	if got := MatchGain(song(-16, 0.5), song(-10, 1.0), 0); got >= 0 {
		t.Errorf("a louder song should still be cut, got %v", got)
	}
}

func TestMatchGainWithUnknownLoudnessLeavesTheNextSongAlone(t *testing.T) {
	for name, c := range map[string][2]Song{
		"a unknown": {song(0, 0.5), song(-14, 0.5)},
		"b unknown": {song(-14, 0.5), song(0, 0.5)},
	} {
		if got := MatchGain(c[0], c[1], -2); got != 0 {
			t.Errorf("%s: gain = %v dB, want 0", name, got)
		}
	}
}
