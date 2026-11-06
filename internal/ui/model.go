// Package ui is the Bubble Tea front end.
package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/PatrickSteil/ytplay/internal/analyze"
	"github.com/PatrickSteil/ytplay/internal/mpv"
	"github.com/PatrickSteil/ytplay/internal/yt"
)

type mode int

const (
	modeNormal  mode = iota // no widget focused
	modeSearch              // search bar focused
	modeResults             // results list focused
	modeQueue               // queue panel focused (remove / reorder)
	modeConfirm             // "clear the queue?" popup
)

// Actions is what the UI needs from the player (implemented by
// *player.Player).
type Actions interface {
	Queue(ctx context.Context, url string) error
	PlayNow(ctx context.Context, url string) error
	Skip(ctx context.Context) error
	SeekPercent(ctx context.Context, n int) error
	TogglePause(ctx context.Context) error
	QueueMany(ctx context.Context, urls []string) error
	ClearQueue(ctx context.Context) error
	Remove(ctx context.Context, index int) error
	MoveUp(ctx context.Context, index int) error
	MoveDown(ctx context.Context, index int) error
}

// Deps are the things the UI calls out to.
type Deps struct {
	Search func(ctx context.Context, query string) ([]yt.Result, error)
	Player Actions
	// Position reads time-pos and duration; it errors while nothing is loaded.
	Position func(ctx context.Context) (elapsed, total float64, err error)
	// Similar returns up to n songs similar to the seed videos, none of them
	// in exclude (nil: radio is unavailable).
	Similar func(ctx context.Context, seeds []string, exclude map[string]bool, n int) ([]yt.Result, error)
	// Publish is told what is playing whenever it changes (for MPRIS). It is
	// called from Update, so it must return quickly and never block.
	Publish func(NowPlaying)
	// Info fetches description, stats and top comments of a video (nil: none).
	Info func(ctx context.Context, id string) (yt.Info, error)
	// Analyze estimates tempo and key of a video of the given length in
	// seconds (0 if unknown) (nil: none).
	Analyze func(ctx context.Context, id string, duration float64) (analyze.Result, error)
	// Cover returns the rendered cover for a video ID (nil: no covers).
	Cover func(ctx context.Context, id string) (string, error)
}

// NowPlaying is a snapshot of the current track for the desktop shell.
// It is comparable so changes can be detected with ==.
type NowPlaying struct {
	Active   bool
	ID       string
	Title    string
	Channel  string
	Duration float64
	Paused   bool
	HasNext  bool
}

// trackStats is what is known about one track beyond its search result.
type trackStats struct {
	info                   yt.Info
	hasInfo, infoFailed    bool
	res                    analyze.Result
	hasRes, analysisFailed bool
}

// playlistEntry is one element of mpv's observed "playlist" property.
type playlistEntry struct {
	Filename string `json:"filename"`
	Current  bool   `json:"current"`
	Playing  bool   `json:"playing"`
	Title    string `json:"title"`
}

// Model is the single Bubble Tea model.
type Model struct {
	deps Deps

	mode     mode
	input    textinput.Model
	results  []yt.Result
	selected int
	offset   int // first visible results row
	query    string

	player   Actions
	tracks   map[string]yt.Result // every track ever loaded, by video ID
	playlist []playlistEntry      // from the observed "playlist" property
	pos      int                  // playlist-pos, -1 when idle
	idle     bool
	paused   bool
	// loading is true while a track is current but mpv has no position yet.
	loading        bool
	elapsed, total float64           // seconds, from the 500 ms poll
	covers         map[string]string // rendered cover by video ID
	stats          map[string]trackStats
	infoAsked      map[string]bool // lookups already started: never repeated unless cancelled
	analysisAsked  map[string]bool
	statsCancel    context.CancelFunc // cancels the lookups of the previous track
	statsDelay     time.Duration
	coverAsked     map[string]bool // IDs already requested (success or failure): never retried

	// Queue editing. queueCursor indexes the entries after the current one.
	queueCursor int
	queueOff    int  // first visible queue row
	queueReturn mode // where Esc / Tab goes back to
	queueBusy   bool // an edit command is in flight
	confirmFrom mode // where the clear-queue popup returns to
	radioBusy   bool // a similar-songs lookup is running

	lastPub NowPlaying // last snapshot handed to Deps.Publish

	searchSeq    int
	searchCancel context.CancelFunc
	// replaceOnType emulates a selected pre-filled query: the next printable
	// key replaces it, and Backspace clears it.
	// NOTE: textinput has no text selection, so this emulates one.
	replaceOnType bool

	status      string
	statusIsErr bool
	statusAt    time.Time

	width, height int
}

// New builds the initial model.
func New(d Deps) Model {
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.CharLimit = 256
	return Model{
		deps: d, input: ti, width: 80, height: 24,
		tracks: map[string]yt.Result{}, pos: -1, idle: true,
		covers: map[string]string{}, coverAsked: map[string]bool{},
		stats: map[string]trackStats{}, infoAsked: map[string]bool{}, analysisAsked: map[string]bool{},
		statsDelay: statsDelay,
	}
}

func (m Model) Init() tea.Cmd { return tick() }

func tick() tea.Cmd {
	return tea.Tick(tickEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

// setStatus shows a message that clears after statusTTL.
func (m *Model) setStatus(text string, isErr bool) tea.Cmd {
	m.status, m.statusIsErr, m.statusAt = text, isErr, time.Now()
	at := m.statusAt
	return tea.Tick(statusTTL, func(time.Time) tea.Msg { return statusClearMsg{at} })
}

// setStickyStatus shows a message that stays until replaced (e.g. "Searching…").
func (m *Model) setStickyStatus(text string) {
	m.status, m.statusIsErr, m.statusAt = text, false, time.Now()
}

// nowPlaying builds the snapshot published to the desktop shell.
func (m Model) nowPlaying() NowPlaying {
	e := m.currentEntry()
	if e == nil {
		return NowPlaying{}
	}
	title, channel, dur := m.entryInfo(*e)
	return NowPlaying{
		Active: true, ID: yt.VideoID(e.Filename), Title: title, Channel: channel,
		Duration: dur, Paused: m.paused, HasNext: len(m.upcoming()) > 0,
	}
}

// Update handles a message and then publishes the now-playing snapshot if it
// changed. Publishing synchronously (rather than from a cmd) keeps updates in
// order.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	nm := next.(Model)
	if nm.deps.Publish != nil {
		if np := nm.nowPlaying(); np != nm.lastPub {
			nm.lastPub = np
			nm.deps.Publish(np)
		}
	}
	return nm, cmd
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = m.inputWidth()
		m.scrollIntoView()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case statusClearMsg:
		if m.statusAt.Equal(msg.at) {
			m.status = ""
		}
		return m, nil

	case searchDoneMsg:
		return m.handleSearchDone(msg)

	case tickMsg:
		return m, m.pollCmd()

	case positionMsg:
		if msg.err != nil {
			m.elapsed, m.total = 0, 0
			m.loading = m.currentEntry() != nil
		} else {
			m.elapsed, m.total, m.loading = msg.elapsed, msg.total, false
		}
		return m, tick()

	case MpvEventMsg:
		return m.handleMpvEvent(msg.Event)

	case MpvExitedMsg:
		return m, m.quit()

	case queueEditDoneMsg:
		m.queueBusy = false
		if msg.err != nil {
			return m, m.setStatus(msg.err.Error(), true)
		}
		return m, nil

	case radioDoneMsg:
		return m.handleRadioDone(msg)

	case infoMsg:
		if errors.Is(msg.err, context.Canceled) {
			delete(m.infoAsked, msg.id) // interrupted: try again next time
			return m, nil
		}
		st := m.stats[msg.id]
		if msg.err != nil {
			st.infoFailed = true
		} else {
			st.info, st.hasInfo = msg.info, true
		}
		m.stats[msg.id] = st
		return m, nil

	case analysisMsg:
		if errors.Is(msg.err, context.Canceled) {
			delete(m.analysisAsked, msg.id)
			return m, nil
		}
		st := m.stats[msg.id]
		if msg.err != nil {
			st.analysisFailed = true
		} else {
			st.res, st.hasRes = msg.res, true
		}
		m.stats[msg.id] = st
		return m, nil

	case coverMsg:
		if msg.err == nil && msg.rendered != "" {
			m.covers[msg.id] = msg.rendered
		}
		return m, nil

	case actionErrMsg:
		return m, m.setStatus(msg.err.Error(), true)
	}
	return m, nil
}

// pollCmd reads the playback position without blocking Update.
func (m Model) pollCmd() tea.Cmd {
	position := m.deps.Position
	return func() tea.Msg {
		if position == nil {
			return positionMsg{err: errors.New("no position source")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		e, t, err := position(ctx)
		return positionMsg{elapsed: e, total: t, err: err}
	}
}

// currentEntry is the playlist entry being played (nil when idle).
func (m Model) currentEntry() *playlistEntry {
	if m.pos < 0 || m.pos >= len(m.playlist) {
		return nil
	}
	return &m.playlist[m.pos]
}

func (m Model) handleMpvEvent(ev mpv.Event) (Model, tea.Cmd) {
	prev := m.currentID()
	switch ev.Name {
	case "property-change":
		switch ev.Property {
		case "pause":
			_ = json.Unmarshal(ev.Data, &m.paused)
		case "idle-active":
			_ = json.Unmarshal(ev.Data, &m.idle)
		case "playlist-pos":
			var p *int
			if err := json.Unmarshal(ev.Data, &p); err != nil || p == nil {
				m.pos = -1
			} else {
				m.pos = *p
			}
		case "playlist":
			var pl []playlistEntry
			if err := json.Unmarshal(ev.Data, &pl); err == nil {
				m.playlist = pl
			}
		}
	case "end-file":
		if ev.Reason == "error" {
			title := "track"
			if e := m.currentEntry(); e != nil {
				title, _, _ = m.entryInfo(*e)
			}
			return m, m.setStatus("Could not play: "+title, true)
		}
	}
	m.clampQueue()
	if cur := m.currentID(); cur != prev {
		m.elapsed, m.total = 0, 0
		m.loading = cur != ""
		return m, tea.Batch(m.fetchCover(cur), m.fetchStats(cur))
	}
	return m, nil
}

// fetchStats starts the description and tempo/key lookups for the current
// track (after a short delay) and cancels those of the previous one.
func (m *Model) fetchStats(id string) tea.Cmd {
	if m.statsCancel != nil {
		m.statsCancel()
		m.statsCancel = nil
	}
	wantInfo := id != "" && m.deps.Info != nil && !m.infoAsked[id]
	wantAnalysis := id != "" && m.deps.Analyze != nil && !m.analysisAsked[id]
	if !wantInfo && !wantAnalysis {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.statsCancel = cancel
	delay := m.statsDelay
	// wait returns false if the track changed before the delay was over.
	wait := func() bool {
		if delay <= 0 {
			return ctx.Err() == nil
		}
		select {
		case <-time.After(delay):
			return true
		case <-ctx.Done():
			return false
		}
	}
	var cmds []tea.Cmd
	if wantInfo {
		m.infoAsked[id] = true
		fetch := m.deps.Info
		cmds = append(cmds, func() tea.Msg {
			if !wait() {
				return infoMsg{id: id, err: context.Canceled}
			}
			info, err := fetch(ctx, id)
			if ctx.Err() != nil {
				err = context.Canceled
			}
			return infoMsg{id: id, info: info, err: err}
		})
	}
	if wantAnalysis {
		m.analysisAsked[id] = true
		analyse, duration := m.deps.Analyze, m.tracks[id].Duration
		cmds = append(cmds, func() tea.Msg {
			if !wait() {
				return analysisMsg{id: id, err: context.Canceled}
			}
			res, err := analyse(ctx, id, duration)
			if ctx.Err() != nil {
				err = context.Canceled
			}
			return analysisMsg{id: id, res: res, err: err}
		})
	}
	return tea.Batch(cmds...)
}

// fetchCover starts a download for id unless one was already requested.
func (m Model) fetchCover(id string) tea.Cmd {
	if id == "" || m.deps.Cover == nil || m.coverAsked[id] {
		return nil
	}
	m.coverAsked[id] = true
	fetch := m.deps.Cover
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		rendered, err := fetch(ctx, id)
		return coverMsg{id: id, rendered: rendered, err: err}
	}
}

// currentID is the video ID of the current entry ("" when idle).
func (m Model) currentID() string {
	if e := m.currentEntry(); e != nil {
		return yt.VideoID(e.Filename)
	}
	return ""
}

// entryInfo resolves display data for a playlist entry, preferring what we
// know from search results over mpv's own title (which is the URL while the
// stream is still loading).
func (m Model) entryInfo(e playlistEntry) (title, channel string, dur float64) {
	if r, ok := m.tracks[yt.VideoID(e.Filename)]; ok {
		return r.Title, r.Channel, r.Duration
	}
	title = e.Title
	if title == "" {
		title = e.Filename
	}
	return title, "", 0
}

// action runs a player action as a cmd with a timeout. Errors that mpv itself
// reports for seeks (nothing loaded) are ignored when quietMPV is set.
func (m Model) action(quietMPV bool, f func(ctx context.Context) error) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		err := f(ctx)
		if err == nil {
			return nil
		}
		var ce *mpv.CommandError
		if quietMPV && errors.As(err, &ce) {
			return nil
		}
		return actionErrMsg{err: err}
	}
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if k == keyCtrlC {
		return m, m.quit()
	}
	if m.mode == modeConfirm {
		return m.handleConfirmKey(k)
	}
	if m.mode == modeSearch {
		return m.handleSearchKey(msg)
	}
	if m.mode == modeResults {
		if next, cmd, handled := m.handleResultsKey(k); handled {
			return next, cmd
		}
	}
	if m.mode == modeQueue {
		if next, cmd, handled := m.handleQueueKey(k); handled {
			return next, cmd
		}
	}
	return m.handleGlobalKey(k)
}

func (m *Model) quit() tea.Cmd {
	if m.searchCancel != nil {
		m.searchCancel()
	}
	if m.statsCancel != nil {
		m.statsCancel() // stops yt-dlp / mpv helpers started for the stats
	}
	return tea.Quit
}

func (m Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEnter:
		m.replaceOnType = false
		q := strings.TrimSpace(m.input.Value())
		if q == "" {
			return m, nil
		}
		m.setStickyStatus("Searching…")
		return m, m.startSearch(q)
	case keyEsc:
		m.replaceOnType = false
		m.input.Blur()
		if len(m.results) > 0 {
			m.mode = modeResults
		} else {
			m.mode = modeNormal
		}
		return m, nil
	}

	if m.replaceOnType {
		m.replaceOnType = false
		switch msg.Type {
		case tea.KeyRunes, tea.KeySpace:
			m.input.SetValue("")
		case tea.KeyBackspace, tea.KeyDelete:
			m.input.SetValue("")
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// startSearch cancels any running search and returns a cmd for the new one.
func (m *Model) startSearch(q string) tea.Cmd {
	if m.searchCancel != nil {
		m.searchCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.searchCancel = cancel
	m.searchSeq++
	m.query = q
	seq, search := m.searchSeq, m.deps.Search
	return func() tea.Msg {
		res, err := search(ctx, q)
		return searchDoneMsg{seq: seq, query: q, results: res, err: err}
	}
}

func (m Model) handleSearchDone(msg searchDoneMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.searchSeq {
		return m, nil // superseded by a newer search
	}
	switch {
	case msg.err != nil:
		return m, m.setStatus(fmt.Sprintf("Search failed: %v", msg.err), true)
	case len(msg.results) == 0:
		return m, m.setStatus(fmt.Sprintf("No results for %q", msg.query), true)
	}
	m.results = msg.results
	m.selected, m.offset = 0, 0
	m.mode = modeResults
	m.input.Blur()
	m.status = ""
	return m, nil
}

// handleResultsKey handles keys specific to the results list. handled is
// false when the key should fall through to the global keys.
func (m Model) handleResultsKey(k string) (Model, tea.Cmd, bool) {
	switch k {
	case keyDown, keyDownAlt:
		if m.selected < len(m.results)-1 {
			m.selected++
		}
		m.scrollIntoView()
		return m, nil, true
	case keyUp, keyUpAlt:
		if m.selected > 0 {
			m.selected--
		}
		m.scrollIntoView()
		return m, nil, true
	case keyEnter:
		if len(m.results) == 0 {
			return m, nil, true
		}
		r := m.results[m.selected]
		m.tracks[r.ID] = r
		url, p := r.URL(), m.deps.Player
		return m, tea.Batch(
			m.action(false, func(ctx context.Context) error { return p.PlayNow(ctx, url) }),
			m.setStatus("Playing: "+r.Title, false),
		), true
	case keyQueue:
		if len(m.results) == 0 {
			return m, nil, true
		}
		r := m.results[m.selected]
		m.tracks[r.ID] = r
		url, p := r.URL(), m.deps.Player
		return m, tea.Batch(
			m.action(false, func(ctx context.Context) error { return p.Queue(ctx, url) }),
			m.setStatus("Queued: "+r.Title, false),
		), true
	case keyEsc:
		m.mode = modeNormal
		return m, nil, true
	}
	return m, nil, false
}

func (m Model) handleGlobalKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case keySearch:
		m.mode = modeSearch
		m.input.SetValue(m.query)
		m.input.CursorEnd()
		m.replaceOnType = m.query != ""
		return m, m.input.Focus()
	case keyRadio:
		return m.startRadio()
	case keyClearQueue:
		if len(m.upcoming()) == 0 {
			return m, m.setStatus("Queue is empty", false)
		}
		m.confirmFrom, m.mode = m.mode, modeConfirm
		return m, nil
	case keyQueueFocus:
		if len(m.upcoming()) == 0 {
			return m, m.setStatus("Queue is empty", false)
		}
		m.queueReturn = m.mode
		m.mode = modeQueue
		m.queueCursor, m.queueOff = 0, 0
		return m, nil
	case keySkip:
		p := m.deps.Player
		return m, m.action(false, func(ctx context.Context) error { return p.Skip(ctx) })
	case keyPause:
		p := m.deps.Player
		return m, m.action(false, func(ctx context.Context) error { return p.TogglePause(ctx) })
	case keyQuit:
		return m, m.quit()
	}
	if n, ok := seekPercentForKey(k); ok {
		p := m.deps.Player
		return m, m.action(true, func(ctx context.Context) error { return p.SeekPercent(ctx, n) })
	}
	return m, nil
}

// scrollIntoView keeps the selected result inside the visible window.
func (m *Model) scrollIntoView() {
	vis := max(m.resultsVisible(), 1)
	if m.selected < m.offset {
		m.offset = m.selected
	}
	if m.selected >= m.offset+vis {
		m.offset = m.selected - vis + 1
	}
	m.offset = max(min(m.offset, len(m.results)-vis), 0)
}

// upcoming are the playlist entries after the current one.
func (m Model) upcoming() []playlistEntry {
	if m.currentEntry() == nil {
		return nil
	}
	return m.playlist[m.pos+1:]
}

// clampQueue keeps the queue cursor valid after the playlist changed, and
// leaves queue mode when nothing is left to edit.
func (m *Model) clampQueue() {
	if m.mode != modeQueue {
		return
	}
	n := len(m.upcoming())
	if n == 0 {
		m.mode = m.queueReturn
		return
	}
	m.queueCursor = min(m.queueCursor, n-1)
	m.scrollQueue()
}

func (m *Model) scrollQueue() {
	n := len(m.upcoming())
	if m.queueCursor < m.queueOff {
		m.queueOff = m.queueCursor
	}
	win := m.queueWindowSize()
	if m.queueCursor >= m.queueOff+win {
		m.queueOff = m.queueCursor - win + 1
	}
	m.queueOff = max(min(m.queueOff, n-win), 0)
}

// handleQueueKey handles keys of the focused queue panel. The playlist is
// updated optimistically so quick successive edits see the new order; mpv's
// own playlist events overwrite it with the truth.
func (m Model) handleQueueKey(k string) (Model, tea.Cmd, bool) {
	n := len(m.upcoming())
	switch k {
	case keyQueueFocus, keyEsc:
		m.mode = m.queueReturn
		return m, nil, true
	case keyDown:
		m.queueCursor = min(m.queueCursor+1, max(n-1, 0))
		m.scrollQueue()
		return m, nil, true
	case keyUp:
		m.queueCursor = max(m.queueCursor-1, 0)
		m.scrollQueue()
		return m, nil, true
	case keyRemove, keyShiftUp, keyShiftDown:
		// handled below
	default:
		return m, nil, false
	}
	if m.queueBusy || n == 0 {
		return m, nil, true
	}
	idx := m.pos + 1 + m.queueCursor // mpv playlist index of the selection
	p := m.deps.Player
	pl := append([]playlistEntry(nil), m.playlist...) // never mutate the shared slice
	var f func(ctx context.Context) error
	var status string
	switch k {
	case keyRemove:
		title, _, _ := m.entryInfo(pl[idx])
		status = "Removed: " + title
		m.playlist = append(pl[:idx], pl[idx+1:]...)
		f = func(ctx context.Context) error { return p.Remove(ctx, idx) }
		m.clampQueue()
		if m.mode == modeQueue {
			m.queueCursor = min(m.queueCursor, len(m.upcoming())-1)
		}
	case keyShiftUp:
		if m.queueCursor == 0 {
			return m, nil, true // would jump over the playing track
		}
		pl[idx], pl[idx-1] = pl[idx-1], pl[idx]
		m.playlist = pl
		m.queueCursor--
		f = func(ctx context.Context) error { return p.MoveUp(ctx, idx) }
	case keyShiftDown:
		if m.queueCursor >= n-1 {
			return m, nil, true
		}
		pl[idx], pl[idx+1] = pl[idx+1], pl[idx]
		m.playlist = pl
		m.queueCursor++
		f = func(ctx context.Context) error { return p.MoveDown(ctx, idx) }
	}
	m.scrollQueue()
	m.queueBusy = true
	cmds := []tea.Cmd{func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return queueEditDoneMsg{err: f(ctx)}
	}}
	if status != "" {
		cmds = append(cmds, m.setStatus(status, false))
	}
	return m, tea.Batch(cmds...), true
}

// radioCount is how many similar songs the radio key adds.
const radioCount = 20

// radioSeeds picks up to three songs to find similar ones for: the current
// one, plus one from the middle and the end of the upcoming queue, so the
// result reflects the whole queue and not just one song. Played entries are
// ignored and duplicates removed.
func radioSeeds(pl []playlistEntry, pos int) []string {
	if pos < 0 || pos >= len(pl) {
		return nil
	}
	var picks []playlistEntry
	picks = append(picks, pl[pos])
	if up := pl[pos+1:]; len(up) > 0 {
		if len(up) >= 3 {
			picks = append(picks, up[(len(up)-1)/2])
		}
		picks = append(picks, up[len(up)-1])
	}
	var seeds []string
	seen := map[string]bool{}
	for _, e := range picks {
		if id := yt.VideoID(e.Filename); id != "" && !seen[id] {
			seen[id] = true
			seeds = append(seeds, id)
		}
	}
	return seeds
}

func (m Model) startRadio() (Model, tea.Cmd) {
	if m.deps.Similar == nil || m.radioBusy {
		return m, nil
	}
	seeds := radioSeeds(m.playlist, m.pos)
	if len(seeds) == 0 {
		return m, m.setStatus("Play or queue something first", true)
	}
	// Everything already in the playlist, played songs included, is excluded.
	exclude := map[string]bool{}
	for _, e := range m.playlist {
		exclude[yt.VideoID(e.Filename)] = true
	}
	m.radioBusy = true
	m.setStickyStatus("Finding similar songs…")
	similar := m.deps.Similar
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := similar(ctx, seeds, exclude, radioCount)
		return radioDoneMsg{results: res, err: err}
	}
}

func (m Model) handleRadioDone(msg radioDoneMsg) (Model, tea.Cmd) {
	m.radioBusy = false
	switch {
	case msg.err != nil:
		return m, m.setStatus("Radio failed: "+msg.err.Error(), true)
	case len(msg.results) == 0:
		return m, m.setStatus("No new similar songs found", false)
	}
	urls := make([]string, len(msg.results))
	for i, r := range msg.results {
		m.tracks[r.ID] = r
		urls[i] = r.URL()
	}
	p := m.deps.Player
	return m, tea.Batch(
		m.action(false, func(ctx context.Context) error { return p.QueueMany(ctx, urls) }),
		m.setStatus(fmt.Sprintf("Added %d similar songs", len(urls)), false),
	)
}

// handleConfirmKey handles the "clear the queue?" popup: only y confirms, n
// or Esc decline, everything else is ignored.
func (m Model) handleConfirmKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case keyConfirm:
		m.mode = m.confirmFrom
		cur := m.currentEntry()
		if cur == nil || len(m.upcoming()) == 0 {
			m.clampQueue() // nothing left to clear (it changed while asking)
			return m, nil
		}
		// mpv keeps the playing entry; show that right away, events confirm it.
		m.playlist, m.pos = []playlistEntry{*cur}, 0
		m.clampQueue()
		p := m.deps.Player
		return m, tea.Batch(
			m.action(false, func(ctx context.Context) error { return p.ClearQueue(ctx) }),
			m.setStatus("Queue cleared", false),
		)
	case keyDeny, keyEsc:
		m.mode = m.confirmFrom
		m.clampQueue()
	}
	return m, nil
}
