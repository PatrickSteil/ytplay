package ui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PatrickSteil/ytplay/internal/yt"
)

// A link to a YouTube playlist or video, typed after :load or pasted into the
// search bar, adds what it points to to the queue.

const linkHelp = "Give a link to a YouTube or SoundCloud playlist, set or song, as in :load https://www.youtube.com/playlist?list=..."

// startLoad loads the songs of a link, in the background.
func (m Model) startLoad(l yt.Link) (Model, tea.Cmd) {
	say := func(text string, isErr bool) (Model, tea.Cmd) {
		status := m.setStatus(text, isErr)
		return m, status
	}
	switch {
	case m.deps.Load == nil:
		return say("Loading links is not available", true)
	case m.loadBusy:
		return say("Still loading the last link…", false)
	}
	m.loadBusy = true
	what := "the video"
	if l.Kind == yt.PlaylistLink {
		what = "the playlist"
	}
	m.setStickyStatus("Loading " + what + "…")
	load, ctx := m.deps.Load, m.queueCtx
	return m, func() tea.Msg {
		loaded, err := load(ctx, l)
		return linkLoadedMsg{link: l, loaded: loaded, err: err}
	}
}

// loadFromText starts loading if text is a link to YouTube (ok is false if it
// is not, so the caller can treat it as a search term).
func (m Model) loadFromText(text string) (next Model, cmd tea.Cmd, ok bool) {
	l := yt.ParseLink(text)
	if l.Kind == yt.NotALink {
		return m, nil, false
	}
	next, cmd = m.startLoad(l)
	return next, cmd, true
}

// handleLinkLoaded queues what the link held.
func (m Model) handleLinkLoaded(msg linkLoadedMsg) (Model, tea.Cmd) {
	m.loadBusy = false
	if msg.err != nil {
		status := m.setStatus("Could not load the link: "+msg.err.Error(), true)
		return m, status
	}
	urls := make([]string, len(msg.loaded.Entries))
	for i, r := range msg.loaded.Entries {
		m.tracks[r.ID] = r
		urls[i] = r.URL()
	}
	what, text := "addition", ""
	if msg.link.Kind == yt.PlaylistLink {
		what = "playlist"
		songs := "songs"
		if len(urls) == 1 {
			songs = "song"
		}
		from := "the playlist"
		if msg.loaded.Title != "" {
			from = msg.loaded.Title
		}
		text = fmt.Sprintf("Added %d %s from %s", len(urls), songs, from)
		if msg.loaded.Truncated {
			text += fmt.Sprintf(" (the first %d)", len(urls))
		}
	} else if len(msg.loaded.Entries) > 0 {
		text = "Queued: " + msg.loaded.Entries[0].Title
	}
	if m.currentEntry() != nil {
		m.rememberQueue(what)
	}
	p := m.deps.Player
	add := m.action(false, func(ctx context.Context) error { return p.QueueMany(ctx, urls) })
	status := m.setStatus(text, false)
	return m, tea.Batch(add, status)
}

func cmdLoad(m Model, arg string) (Model, tea.Cmd) {
	l := yt.ParseLink(arg)
	if l.Kind == yt.NotALink {
		status := m.setStatus(linkHelp, true)
		return m, status
	}
	return m.startLoad(l)
}
