package ui

import (
	"fmt"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
)

// The comments of the playing song are shown a page at a time, and :comments goes
// to the next page, fetching more from YouTube when the ones fetched are used up.
const (
	firstComments = 10  // how many the first fetch asks for
	commentsStep  = 20  // how many more each further fetch asks for
	commentsMax   = 100 // and the most that is asked for
)

// maybeMoreComments says whether YouTube may have more comments than were
// fetched: as many came back as were asked for, and that was not the limit.
func (st trackStats) maybeMoreComments() bool {
	return len(st.info.Comments) >= st.commentsAsked && st.commentsAsked < commentsMax
}

// commentsHeader is "Top comments 4–6 of 10+ · :comments for more" for the page
// that starts at off and has shown comments on it.
func (m Model) commentsHeader(st trackStats, off, shown int) string {
	n := len(st.info.Comments)
	which := strconv.Itoa(off + 1)
	if shown > 1 {
		which = fmt.Sprintf("%d–%d", off+1, off+shown)
	}
	of := strconv.Itoa(n)
	if st.maybeMoreComments() {
		of += "+"
	}
	h := bold.Render("Top comments") + dim.Render(" "+which+" of "+of)
	if off+shown < n || st.maybeMoreComments() {
		h += dim.Render(" · :comments for more")
	}
	return h
}

// commentsShown is how many comments the panel shows now (0 in the stacked layout,
// which has no room for them).
func (m Model) commentsShown() int {
	if !m.wide() {
		return 0
	}
	_, n := m.leftBodyN(m.colInnerHeight())
	return n
}

func cmdComments(m Model, _ string) (Model, tea.Cmd) {
	id := m.currentID()
	st := m.stats[id]
	say := func(text string, isErr bool) (Model, tea.Cmd) {
		status := m.setStatus(text, isErr)
		return m, status
	}
	switch {
	case id == "":
		return say("Nothing is playing", false)
	case m.deps.Info == nil:
		return say("Comments are not available", true)
	case !st.hasInfo:
		return say("The comments have not loaded yet", false)
	case !m.wide():
		return say("Comments are shown when the window is wide enough for three columns", false)
	case len(st.info.Comments) == 0:
		return say("This song has no comments", false)
	case st.commentsBusy:
		return say("Still loading more comments…", false)
	}
	shown := m.commentsShown()
	if shown == 0 {
		return say("There is no room for comments: make the window taller", false)
	}
	n := len(st.info.Comments)
	if next := st.commentsOff + shown; next < n { // the next page is already here
		st.commentsOff = next
		m.stats[id] = st
		return m, nil
	}
	if !st.maybeMoreComments() { // that was the last one
		st.commentsOff = 0
		m.stats[id] = st
		return say("That was the last comment: back to the first", false)
	}
	asked := min(st.commentsAsked+commentsStep, commentsMax)
	st.commentsBusy = true
	m.stats[id] = st
	fetch, ctx := m.deps.Info, m.queueCtx
	load := func() tea.Msg {
		info, err := fetch(ctx, id, asked)
		return moreCommentsMsg{id: id, info: info, asked: asked, err: err}
	}
	status := m.setStatus("Loading more comments…", false)
	return m, tea.Batch(load, status)
}

// handleMoreComments takes in the comments that were fetched: the page goes to
// the first new one. A failed fetch leaves what there was.
func (m Model) handleMoreComments(msg moreCommentsMsg) (Model, tea.Cmd) {
	st := m.stats[msg.id]
	st.commentsBusy = false
	if msg.err != nil {
		m.stats[msg.id] = st
		status := m.setStatus("Could not load more comments: "+msg.err.Error(), true)
		return m, status
	}
	had := len(st.info.Comments)
	st.info, st.commentsAsked = msg.info, msg.asked
	text := "No more comments"
	if got := len(msg.info.Comments); got > had {
		text = fmt.Sprintf("Loaded %d more comments", got-had)
	}
	m.stats[msg.id] = st
	if got := len(msg.info.Comments); got > had && msg.id == m.currentID() {
		// The new ones go below the old ones; if they are out of view, scroll to
		// the first of them.
		if st.commentsOff+m.commentsShown() <= had {
			st.commentsOff = had
			m.stats[msg.id] = st
		}
	}
	status := m.setStatus(text, false)
	return m, status
}
