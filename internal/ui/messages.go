package ui

import (
	"time"

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
