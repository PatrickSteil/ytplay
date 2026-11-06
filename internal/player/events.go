package player

import (
	"encoding/json"

	"github.com/PatrickSteil/ytplay/internal/mpv"
)

// playlistEntry is one element of the "playlist" property, in the shape mpv
// sends it (the UI parses exactly this).
type playlistEntry struct {
	Filename string `json:"filename"`
	Current  bool   `json:"current"`
	Playing  bool   `json:"playing"`
}

func propertyEvent(prop string, value any) mpv.Event {
	data, _ := json.Marshal(value)
	return mpv.Event{Name: "property-change", Property: prop, Data: data}
}

// stateEvents is what the UI needs to show the queue: the playlist, the
// position in it (null when nothing plays) and whether the player is idle.
func (q *queue) stateEvents() []mpv.Event {
	entries := make([]playlistEntry, len(q.urls))
	for i, u := range q.urls {
		entries[i] = playlistEntry{Filename: u, Current: i == q.pos, Playing: i == q.pos}
	}
	var pos any // nil marshals to null
	if q.pos >= 0 {
		pos = q.pos
	}
	return []mpv.Event{
		propertyEvent("playlist", entries),
		propertyEvent("playlist-pos", pos),
		propertyEvent("idle-active", q.pos < 0),
	}
}

// emit hands an event to the UI. It blocks while the UI is not reading, and
// gives up when the player is closed.
func (p *Player) emit(ev mpv.Event) {
	select {
	case p.events <- ev:
	case <-p.stopped:
	}
}

// emitState tells the UI the queue and the position in it.
func (p *Player) emitState() {
	for _, ev := range p.q.stateEvents() {
		p.emit(ev)
	}
}
