package player

import "context"

// Silence at the ends of a song is not played: the player seeks past what is
// silent at the start, and moves on at the end of the sound, using the audible
// range the analysis measured (autodj, when on, plans its own handovers).
var (
	// trimMin is how much silence at the start is worth skipping, trimLead how
	// much of it is kept so that the first sound is not clipped.
	trimMin  = 1.0
	trimLead = 0.05
	// trimMinEnd is how much silence at the end is worth skipping, trimHold how
	// long after the end of the sound the song is let ring before it goes on.
	trimMinEnd = 1.5
	trimHold   = 0.2
	// trimMinAudible is the least sound a song must have to be trimmed at all:
	// a song that is mostly silence was probably measured wrongly.
	trimMinAudible = 10.0
)

// trimmed is the song whose start has been dealt with.
type trimmed struct {
	pos   int
	entry int64
	ok    bool
}

// SetTrim tells the player where songs start and end sounding, by URL, so that
// it can skip their silent ends (nil: no trimming).
func (p *Player) SetTrim(songs Analyses) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.trim = songs
}

// trimEnd is the time at which the sound of the song at url ends, if the song
// is long (total seconds) and quiet enough at the end for it to be worth going
// on from there; 0 otherwise. The caller holds p.mu.
func (p *Player) trimEnd(url string, total float64) float64 {
	if p.trim == nil || p.autoDJ {
		return 0
	}
	s, ok := p.trim(url)
	e := s.Edges
	if !ok || e.AudioEnd <= 0 || e.AudioEnd-e.AudioStart < trimMinAudible || total-e.AudioEnd < trimMinEnd {
		return 0
	}
	return e.AudioEnd
}

// pollTrim skips the silence at the start of the playing song (once), and goes
// on to the next song when the sound of the playing one has ended. The caller
// holds p.mu.
func (p *Player) pollTrim(ctx context.Context) {
	if p.trim == nil || p.q.pos < 0 {
		return
	}
	url := p.q.urls[p.q.pos]
	s, ok := p.trim(url)
	if !ok {
		return
	}
	d := p.decks[p.active]
	pos, err := d.GetFloat(ctx, "time-pos")
	if err != nil {
		return
	}
	e := s.Edges
	if e.AudioEnd > 0 && e.AudioEnd-e.AudioStart < trimMinAudible {
		return
	}
	if key := (trimmed{p.q.pos, p.entryID, true}); p.trimmedStart != key {
		p.trimmedStart = key
		if pos < e.AudioStart-trimMin { // at least trimMin of silence still to come
			_ = p.cmd(ctx, "seek", e.AudioStart-trimLead, "absolute")
			return
		}
	}
	total, err := d.GetFloat(ctx, "duration")
	if err != nil || total <= 0 {
		total = s.Length
	}
	if end := p.trimEnd(url, total); end > 0 && pos >= end+trimHold {
		p.playFrom(ctx, p.q.pos+1)
	}
}
