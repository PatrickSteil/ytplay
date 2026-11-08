package ui

import (
	"time"

	"github.com/PatrickSteil/ytplay/internal/analyze"
	"github.com/PatrickSteil/ytplay/internal/mpv"
	"github.com/PatrickSteil/ytplay/internal/yt"
)

// searchDoneMsg is the result of a search cmd.
// NOTE: results are matched by seq rather than by query alone, so re-running
// the same query after cancelling it cannot be confused with the old run.
type searchDoneMsg struct {
	seq     int
	query   string
	results []yt.Result
	failed  map[yt.Source]error // a source that could not be searched (the rest is in results)
	err     error
}

// statusClearMsg clears the status line if it has not changed since `at`.
type statusClearMsg struct{ at time.Time }

const statusTTL = 4 * time.Second

// MpvEventMsg carries an mpv event; main forwards client.Events() with
// program.Send.
type MpvEventMsg struct{ Event mpv.Event }

// MpvExitedMsg tells the UI that mpv died; the UI quits.
type MpvExitedMsg struct{}

// tickMsg fires every 500 ms and triggers a position poll.
type tickMsg struct{}

// positionMsg is the result of polling time-pos and duration.
type positionMsg struct {
	elapsed, total float64
	err            error // property unavailable (idle or still loading)
}

// actionErrMsg reports a failed player action.
type actionErrMsg struct{ err error }

const (
	tickEvery     = 500 * time.Millisecond
	actionTimeout = 3 * time.Second
)

// castListMsg is the answer to looking for speakers.
type castListMsg struct {
	names []string
	err   error
}

// castDoneMsg is the answer to starting or ending casting.
type castDoneMsg struct {
	name string // the speaker, "" when casting ended
	off  bool
	err  error
}

// yankDoneMsg is the result of copying a link.
type yankDoneMsg struct {
	title string
	err   error
}

// coverMsg is the result of a cover download. rendered is empty on failure.
type coverMsg struct {
	id       string
	rendered string
	err      error
}

// queueEditDoneMsg reports that a remove / move command finished.
type queueEditDoneMsg struct{ err error }

// radioDoneMsg is the result of looking for songs similar to the queue.
type radioDoneMsg struct {
	results []yt.Result
	err     error
}

// infoMsg is the result of fetching description, stats and comments.
// linkLoadedMsg is the answer to loading a YouTube link.
type linkLoadedMsg struct {
	link   yt.Link
	loaded yt.Loaded
	err    error
}

// moreCommentsMsg is the answer to a fetch of more comments of a song.
type moreCommentsMsg struct {
	id    string
	info  yt.Info
	asked int // how many comments were asked for
	err   error
}

type infoMsg struct {
	id   string
	info yt.Info
	err  error
}

// analysisMsg is the result of analysing a song's tempo and key.
type analysisMsg struct {
	id  string
	res analyze.Result
	err error
}

// statsDelay is how long a track must keep playing before its stats are
// looked up, so skipping through songs does not start a lookup for each one.
const statsDelay = 1200 * time.Millisecond

// downloadDoneMsg is the result of saving a song to a file.
type downloadDoneMsg struct {
	path string
	err  error
}
