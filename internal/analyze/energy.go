package analyze

// Energy is one number for how intense a song feels, 0 (calm) to 1 (full on),
// for ordering a queue by mood. Two things make it: how loud the song is
// (loudness, in LUFS: -18 and below is calm, -6 and above is as loud as music
// gets) and how much of the song is sustained (the share of the waveform that
// is at least half as loud as its loud parts: a song that is all drop against
// one that is mostly breakdown). Tempo is left out: the ordering knows it.
const (
	calmLUFS, loudLUFS = -18.0, -6.0
	sustainedLevel     = 128 // of 255: half of the 98th percentile
)

// energyOf is the energy of a song of the given loudness (lufs, 0 if unknown)
// and waveform. 0 if nothing can be told.
func energyOf(lufs float64, w Wave) float64 {
	loud, haveLoud := 0.0, lufs != 0
	if haveLoud {
		loud = min(max((lufs-calmLUFS)/(loudLUFS-calmLUFS), 0), 1)
	}
	dense, haveDense := 0.0, len(w.Level) > 0
	if haveDense {
		n := 0
		for _, v := range w.Level {
			if v >= sustainedLevel {
				n++
			}
		}
		dense = float64(n) / float64(len(w.Level))
	}
	switch {
	case haveLoud && haveDense:
		return 0.5*loud + 0.5*dense
	case haveLoud:
		return loud
	}
	return dense
}
