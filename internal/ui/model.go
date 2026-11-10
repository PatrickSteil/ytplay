// Package ui is the Bubble Tea front end.
package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/PatrickSteil/ytplay/internal/analyze"
	"github.com/PatrickSteil/ytplay/internal/autodj"
	"github.com/PatrickSteil/ytplay/internal/mpv"
	"github.com/PatrickSteil/ytplay/internal/yt"
)

type mode int

const (
	modeNormal   mode = iota // no widget focused
	modeSearch               // search bar focused
	modeResults              // results list focused
	modeQueue                // queue panel focused (remove / reorder)
	modeConfirm              // "clear the queue?" popup
	modeDownload             // "save to file" popup
	modeHelp                 // help popup
	modeDJ                   // DJ popup: queue tempo, sort by BPM, set a BPM
	modeCommand              // the command line (":") is open
)

// Actions is what the UI needs from the player (implemented by
// *player.Player).
type Actions interface {
	Queue(ctx context.Context, url string) error
	PlayNow(ctx context.Context, url string) error
	Skip(ctx context.Context) error
	// Prev goes back: to the song before, or to the start of the playing one.
	Prev(ctx context.Context) error
	SeekPercent(ctx context.Context, n int) error
	// SeekTo seeks to a time in the playing song, SeekBy by a number of seconds.
	SeekTo(ctx context.Context, seconds float64) error
	SeekBy(ctx context.Context, seconds float64) error
	// SetVolume sets the volume, 0 to 100 percent of full.
	SetVolume(ctx context.Context, percent float64) error
	TogglePause(ctx context.Context) error
	QueueMany(ctx context.Context, urls []string) error
	ClearQueue(ctx context.Context) error
	Remove(ctx context.Context, index int) error
	MoveUp(ctx context.Context, index int) error
	MoveDown(ctx context.Context, index int) error
	// ReorderUpcoming puts the songs after the playing one in the given
	// order; urls must be exactly the upcoming songs.
	ReorderUpcoming(ctx context.Context, urls []string) error
	// ArrangeFromCurrent orders the playing song and the upcoming ones; if
	// another song comes first it plays now.
	ArrangeFromCurrent(ctx context.Context, urls []string) error
	// SetUpcoming replaces the songs after the playing one, to bring back an
	// older queue (undo).
	SetUpcoming(ctx context.Context, urls []string) error
}

// Deps are the things the UI calls out to.
type Deps struct {
	// Search looks up a name; failed names a source that could not be searched
	// while the other's results are returned.
	Search func(ctx context.Context, query string) (results []yt.Result, failed map[yt.Source]error, err error)
	Player Actions
	// Position reads time-pos and duration; it errors while nothing is loaded.
	Position func(ctx context.Context) (elapsed, total float64, err error)
	// Similar returns up to n songs similar to the seed videos, none of them
	// in exclude (nil: radio is unavailable).
	Similar func(ctx context.Context, seeds []string, exclude map[string]bool, n int) ([]yt.Result, error)
	// Publish is told what is playing whenever it changes (for MPRIS). It is
	// called from Update, so it must return quickly and never block.
	Publish func(NowPlaying)
	// Info fetches description, stats and the top comments of a video, as many
	// comments as asked for (nil: none).
	Info func(ctx context.Context, id string, comments int) (yt.Info, error)
	// Analyze estimates tempo and key of a video of the given length in
	// seconds (0 if unknown) (nil: none). level is how much is wanted: the
	// playing song gets the cheap Light analysis until the DJ popup has been
	// opened, every song the full one (with beat grids) after that.
	Analyze func(ctx context.Context, id string, duration float64, level analyze.Level) (analyze.Result, error)
	// Load lists the songs a YouTube link (a playlist or a video) holds (nil: none).
	Load func(ctx context.Context, link yt.Link) (yt.Loaded, error)
	// SetAutoDJ switches the automatic transitions on or off. songs looks up what
	// the analyses found out about a song by its URL; it may be called from any
	// goroutine (nil: the player cannot do autodj).
	SetAutoDJ func(on bool, songs func(url string) (autodj.Song, bool))
	// Download saves the audio of a video to a file (nil: no downloads).
	Download func(ctx context.Context, id, path string) error
	// Cover returns the rendered cover for a video ID (nil: no covers).
	// Devices lists the speakers on the network (cast devices) by name, Cast moves
	// playback to the one called name (a part of the name will do) and returns its
	// full name, CastOff brings it back. nil: no casting.
	Devices func(ctx context.Context) ([]string, error)
	Cast    func(ctx context.Context, name string) (string, error)
	CastOff func(ctx context.Context) error
	// SetTrim hands the player the lookup of what is known about songs (where
	// their sound starts and ends), so that it can skip silence. Called at start.
	SetTrim func(songs func(url string) (autodj.Song, bool))
	// Copy puts text on the clipboard (nil: no clipboard).
	Copy  func(text string) error
	Cover func(ctx context.Context, id, thumb string) (string, error)
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
	info                yt.Info
	hasInfo, infoFailed bool
	// The comments are paged: commentsOff is the first one shown, commentsAsked
	// how many the last fetch asked for (as many came back unless the video has
	// no more), commentsBusy that more are being fetched.
	commentsOff, commentsAsked int
	commentsBusy               bool
	res                        analyze.Result
	hasRes, analysisFailed     bool
	analysisRetried            bool   // the failed analysis was tried once more
	analysisErr                string // why the analysis failed, once it is given up
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
	fullAsked      map[string]bool // songs a full analysis was started for (the DJ features need it)
	// Songs further down the queue are analysed in the background, a few at a
	// time, so their tempo is known by the time it is wanted (to sort by it).
	queueRunning map[string]bool // analyses started for queued songs and not finished
	queueCtx     context.Context // lives until the app quits, unlike statsCancel
	queueCancel  context.CancelFunc
	statsCancel  context.CancelFunc // cancels the lookups of the previous track
	statsID      string             // the track statsCancel belongs to
	// Preview of the selected search result (wide layout): lookups only start
	// once the selection has rested for previewDelay.
	previewDelay  time.Duration
	previewCancel context.CancelFunc // cancels the lookup for the result left behind
	previewID     string             // song the running preview lookup is for
	statsDelay    time.Duration
	coverAsked    map[string]bool // IDs already requested (success or failure): never retried

	// Queue editing. queueCursor indexes the entries after the current one.
	queueCursor int
	queueOff    int  // first visible queue row
	queueReturn mode // where Esc / Tab goes back to
	queueBusy   bool // an edit command is in flight
	sorted      bool // the queue was sorted by BPM and not edited by hand since: radio songs are sorted in
	// resortIDs are the songs a radio added that are waited for (to be in the
	// queue and analysed) before the queue is sorted again.
	resortIDs   map[string]bool
	confirmFrom mode // where the clear-queue popup returns to
	radioBusy   bool // a similar-songs lookup is running

	// DJ popup. Everything about tempo beyond the playing song's own BPM line
	// lives here, and queued songs are only analysed once it has been opened.
	djFrom   mode // where the popup returns to
	djActive bool // the popup was opened once: queued songs are analysed from now on
	djCursor int  // 0 is the playing song, 1.. the upcoming ones
	djOff    int  // first visible row
	// The command line: where it was opened from (and goes back to), what was run
	// before in this session, and where in that history Up and Down are.
	cmdInput   textinput.Model
	cmdFrom    mode
	cmdHistory []string
	cmdHistPos int
	cmdDraft   string
	helpOff    int    // how far the help popup is scrolled
	loadBusy   bool   // a link is being loaded
	castName   string // the speaker being played on, "" when playing here
	castBusy   bool   // looking for speakers or connecting to one
	// undo are the upcoming queues before the last edits, newest last; they are
	// forgotten when the song changes (what is "upcoming" is then another list).
	undo []undoEntry
	// volume is the level the user chose (percent); muted sends 0 instead of it.
	volume float64
	muted  bool
	// bpmSet is the BPM the user set by hand, by video ID. It wins over the
	// detected one and lasts until ytplay quits.
	bpmSet map[string]float64
	// autoDJ is whether the next song starts under the end of the playing one;
	// songs is what the player may look up about analysed songs for that.
	autoDJ bool
	songs  *analysisStore

	// Download popup.
	helpFrom mode // where the help popup returns to

	dlInput   textinput.Model
	dlFrom    mode
	dlTrack   yt.Result
	dlOptions []string // completion candidates shown after Tab
	dlWarn    string   // e.g. "file exists"; Enter again confirms

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
	di := textinput.New()
	di.Prompt = "> "
	di.CharLimit = 512
	ci := textinput.New()
	ci.Prompt = ":"
	ci.CharLimit = 200
	queueCtx, queueCancel := context.WithCancel(context.Background())
	return Model{
		deps: d, input: ti, dlInput: di, cmdInput: ci, bpmSet: map[string]float64{}, songs: newAnalysisStore(), width: 80, height: 24,
		queueRunning: map[string]bool{}, queueCtx: queueCtx, queueCancel: queueCancel,
		tracks: map[string]yt.Result{}, pos: -1, idle: true,
		covers: map[string]string{}, coverAsked: map[string]bool{},
		stats: map[string]trackStats{}, infoAsked: map[string]bool{}, analysisAsked: map[string]bool{}, fullAsked: map[string]bool{}, volume: 100,
		statsDelay: statsDelay, previewDelay: previewDelay,
	}
}

func (m Model) Init() tea.Cmd {
	if m.deps.SetTrim != nil {
		m.deps.SetTrim(m.songs.lookup)
	}
	return tick()
}

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
		Active: true, ID: yt.KeyOf(e.Filename), Title: title, Channel: channel,
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
		return m, m.schedulePreview()

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

	case downloadDoneMsg:
		if msg.err != nil {
			return m, m.setStatus("Download failed: "+msg.err.Error(), true)
		}
		return m, m.setStatus("Saved "+msg.path, false)

	case infoMsg:
		if errors.Is(msg.err, context.Canceled) {
			if !m.statsLive(msg.id) {
				delete(m.infoAsked, msg.id) // interrupted: try again next time
			}
			return m, nil
		}
		st := m.stats[msg.id]
		if msg.err != nil {
			st.infoFailed = true
		} else {
			st.info, st.hasInfo, st.commentsAsked = msg.info, true, firstComments
		}
		m.stats[msg.id] = st
		return m, nil

	case previewMsg:
		return m.handlePreview(msg)

	case moreCommentsMsg:
		return m.handleMoreComments(msg)

	case linkLoadedMsg:
		return m.handleLinkLoaded(msg)

	case analysisMsg:
		delete(m.queueRunning, msg.id)
		if errors.Is(msg.err, context.Canceled) {
			if !m.statsLive(msg.id) {
				delete(m.analysisAsked, msg.id)
				delete(m.fullAsked, msg.id)
			}
			return m, nil
		}
		st := m.stats[msg.id]
		if msg.err != nil && !st.analysisRetried {
			// Once more, after a moment. The song stays marked as asked for until
			// then, so nothing else asks again in the meantime.
			st.analysisRetried = true
			m.stats[msg.id] = st
			id := msg.id
			return m, tea.Tick(analysisRetryDelay, func(time.Time) tea.Msg { return retryAnalysisMsg{id: id} })
		}
		if msg.err != nil {
			st.analysisFailed, st.analysisErr = true, msg.err.Error()
		} else {
			st.res, st.hasRes = msg.res, true
			m.songs.put(msg.id, autodj.Song{Length: m.tracks[msg.id].Duration, Edges: msg.res.Edges, Loudness: msg.res.Loudness, Peak: msg.res.Peak})
		}
		m.stats[msg.id] = st
		queued := m.startQueuedAnalyses() // a slot is free
		m, sorted := m.maybeResort()
		return m, tea.Batch(queued, sorted)

	case retryAnalysisMsg:
		return m.handleRetryAnalysis(msg)

	case castListMsg:
		m.castBusy = false
		switch {
		case msg.err != nil:
			return m, m.setStatus("Could not look for speakers: "+msg.err.Error(), true)
		case len(msg.names) == 0:
			return m, m.setStatus("No speakers found on the network", true)
		}
		return m, m.setStatus("Speakers: "+strings.Join(msg.names, ", ")+" (:cast NAME plays there, :cast off comes back)", false)

	case castDoneMsg:
		m.castBusy = false
		switch {
		case msg.err != nil:
			return m, m.setStatus("Could not cast: "+msg.err.Error(), true)
		case msg.off:
			return m, m.setStatus("Playing here again", false)
		}
		return m, m.setStatus("Casting to "+msg.name, false)

	case yankDoneMsg:
		if msg.err != nil {
			return m, m.setStatus("Could not copy the link: "+msg.err.Error(), true)
		}
		return m, m.setStatus("Copied the link of "+msg.title, false)

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
	case "cast":
		m.castName = ev.Reason
		if ev.Reason == "lost" {
			m.castName = ""
			return m, m.setStatus("Lost the speaker: playing here again", true)
		}
	case "end-file":
		if ev.Reason == "error" {
			title := "track"
			if e := m.currentEntry(); e != nil {
				title, _, _ = m.entryInfo(*e)
			}
			text := "Could not play: " + title
			if e := m.currentEntry(); e != nil && yt.SourceOf(yt.KeyOf(e.Filename)) == yt.SoundCloud {
				text += " (SoundCloud: it may be protected or removed)"
			}
			return m, m.setStatus(text, true)
		}
	}
	m.clampQueue()
	m.clampDJ()
	queued := m.startQueuedAnalyses()
	m, sorted := m.maybeResort()
	queued = tea.Batch(queued, sorted)
	if cur := m.currentID(); cur != prev {
		m.elapsed, m.total = 0, 0
		m.undo = nil
		m.loading = cur != ""
		return m, tea.Batch(m.fetchCover(cur), m.fetchStats(cur), queued)
	}
	return m, queued
}

// queueAnalysisWorkers is how many songs are analysed completely at the same
// time once the DJ popup was opened: queued ones, and the playing one if it still
// needs upgrading. Memory is what limits it: a song's audio takes up to 100 MB
// while it is being fetched and analysed.
const queueAnalysisWorkers = 4

// needsFull says whether the DJ features still need a full analysis of song id:
// it was never asked about, or only analysed lightly. A failed analysis is not
// retried (a song asked about with no result has nothing to upgrade, and a failed
// full one has fullAsked set), and one still running is waited for: its result
// arrives, and if it is a light one this is asked again.
func (m *Model) needsFull(id string) bool {
	st := m.stats[id]
	switch {
	case m.fullAsked[id]:
		return false
	case !m.analysisAsked[id]:
		return true
	case st.hasRes:
		return st.res.Level == analyze.Light
	}
	return false
}

// startQueuedAnalyses starts the full analysis of the playing song (if it only
// has a light one) and of upcoming songs, nearest first, while fewer than
// queueAnalysisWorkers run. A song is asked about once: a failure is not retried.
func (m *Model) startQueuedAnalyses() tea.Cmd {
	if m.deps.Analyze == nil {
		return nil
	}
	if !m.djActive {
		return m.analyseNextLight() // beat grids and the whole queue are only analysed for the DJ popup
	}
	wanted := m.upcoming()
	if cur := m.currentEntry(); cur != nil {
		wanted = append([]playlistEntry{*cur}, wanted...)
	}
	var cmds []tea.Cmd
	for _, e := range wanted {
		if len(m.queueRunning) >= queueAnalysisWorkers {
			break
		}
		id := yt.KeyOf(e.Filename)
		if id == "" || !m.needsFull(id) {
			continue
		}
		m.analysisAsked[id], m.fullAsked[id] = true, true
		m.queueRunning[id] = true
		analyse, duration, ctx := m.deps.Analyze, m.tracks[id].Duration, m.queueCtx
		cmds = append(cmds, func() tea.Msg {
			res, err := analyse(ctx, id, duration, analyze.Full)
			if ctx.Err() != nil {
				err = context.Canceled
			}
			return analysisMsg{id: id, res: res, err: err}
		})
	}
	return tea.Batch(cmds...)
}

// handleRetryAnalysis analyses a song once more after its analysis failed, if it
// is still playing or queued and still has no result.
func (m Model) handleRetryAnalysis(msg retryAnalysisMsg) (Model, tea.Cmd) {
	id, st := msg.id, m.stats[msg.id]
	if m.deps.Analyze == nil || st.hasRes || st.analysisFailed || m.queueRunning[id] {
		return m, nil
	}
	wanted := id == m.currentID()
	for _, e := range m.upcoming() {
		wanted = wanted || yt.KeyOf(e.Filename) == id
	}
	if !wanted {
		return m, nil
	}
	delete(m.analysisAsked, id)
	delete(m.fullAsked, id)
	if m.djActive || id != m.currentID() {
		// the DJ features, or the song after the playing one: the usual ways
		return m, m.startQueuedAnalyses()
	}
	// the playing song, lightly: all the header shows is tempo and key
	m.analysisAsked[id], m.queueRunning[id] = true, true
	analyse, duration, ctx := m.deps.Analyze, m.tracks[id].Duration, m.queueCtx
	return m, func() tea.Msg {
		res, err := analyse(ctx, id, duration, analyze.Light)
		if ctx.Err() != nil {
			err = context.Canceled
		}
		return analysisMsg{id: id, res: res, err: err}
	}
}

// analyseNextLight analyses the song after the playing one, lightly, once the
// playing song's own analysis is done, so that where its sound starts is known
// when it comes on and its silent start can be skipped. Asked once.
func (m *Model) analyseNextLight() tea.Cmd {
	up := m.upcoming()
	if len(up) == 0 || m.currentEntry() == nil {
		return nil
	}
	cur, id := m.currentID(), yt.KeyOf(up[0].Filename)
	if id == "" || m.analysisAsked[id] {
		return nil
	}
	if st := m.stats[cur]; cur != "" && !st.hasRes && !st.analysisFailed {
		return nil // the playing song first (its analysis is not asked for until a moment after it starts)
	}
	m.analysisAsked[id] = true
	m.queueRunning[id] = true
	analyse, duration, ctx := m.deps.Analyze, m.tracks[id].Duration, m.queueCtx
	return func() tea.Msg {
		res, err := analyse(ctx, id, duration, analyze.Light)
		if ctx.Err() != nil {
			err = context.Canceled
		}
		return analysisMsg{id: id, res: res, err: err}
	}
}

// statsLive says whether the lookups of track id are the ones running now, so a
// report that an earlier, cancelled attempt at them was interrupted is old news.
func (m Model) statsLive(id string) bool { return m.statsCancel != nil && m.statsID == id }

// fetchStats starts the description and tempo/key lookups for the current
// track (after a short delay) and cancels those of the previous one.
func (m *Model) fetchStats(id string) tea.Cmd {
	if m.statsCancel != nil {
		m.statsCancel()
		m.statsCancel = nil
		// What the cancelled track had not got yet is asked for again when it
		// returns. This is done here, not when the cancelled lookups report back:
		// if the same track starts again at once (the queue was replaced with it)
		// that would be too late, and it would never be analysed.
		prev, st := m.statsID, m.stats[m.statsID]
		if !st.hasInfo && !st.infoFailed {
			delete(m.infoAsked, prev)
		}
		if !st.hasRes && !st.analysisFailed && !m.queueRunning[prev] {
			delete(m.analysisAsked, prev)
			delete(m.fullAsked, prev)
		}
		m.statsID = ""
	}
	wantInfo := id != "" && m.deps.Info != nil && !m.infoAsked[id]
	wantAnalysis := id != "" && m.deps.Analyze != nil && !m.analysisAsked[id]
	if !wantInfo && !wantAnalysis {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.statsCancel, m.statsID = cancel, id
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
			info, err := fetch(ctx, id, firstComments)
			if ctx.Err() != nil {
				err = context.Canceled
			}
			return infoMsg{id: id, info: info, err: err}
		})
	}
	if wantAnalysis {
		m.analysisAsked[id] = true
		level := analyze.Light // all the header shows is tempo and key
		if m.djActive {
			level = analyze.Full
			m.fullAsked[id] = true
		}
		analyse, duration := m.deps.Analyze, m.tracks[id].Duration
		cmds = append(cmds, func() tea.Msg {
			if !wait() {
				return analysisMsg{id: id, err: context.Canceled}
			}
			res, err := analyse(ctx, id, duration, level)
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
	fetch, thumb := m.deps.Cover, m.tracks[id].Thumb // thumb is "" for YouTube: its URL follows from the id
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		rendered, err := fetch(ctx, id, thumb)
		return coverMsg{id: id, rendered: rendered, err: err}
	}
}

// selectedResult is the highlighted search result.
func (m Model) selectedResult() (yt.Result, bool) {
	if m.selected < 0 || m.selected >= len(m.results) {
		return yt.Result{}, false
	}
	return m.results[m.selected], true
}

// schedulePreview asks for the selected result's cover and description once
// the selection has stayed put for previewDelay, so holding j or k does not
// start a download and a yt-dlp run for every row passed. Nothing is shown in
// the stacked layout, so nothing is fetched there.
func (m Model) schedulePreview() tea.Cmd {
	r, ok := m.selectedResult()
	if !ok || !m.wide() || m.mode != modeResults {
		return nil
	}
	return tea.Tick(m.previewDelay, func(time.Time) tea.Msg { return previewMsg{id: r.ID} })
}

// cancelPreview stops the description lookup for a result that is no longer
// selected. A lookup for the playing song is left alone: it is that song's
// stats now.
func (m *Model) cancelPreview() {
	if m.previewCancel != nil && m.previewID != m.currentID() {
		m.previewCancel()
	}
	m.previewCancel, m.previewID = nil, ""
}

func (m Model) handlePreview(msg previewMsg) (Model, tea.Cmd) {
	r, ok := m.selectedResult()
	if !ok || r.ID != msg.id || !m.wide() {
		return m, nil // the selection moved on
	}
	m.cancelPreview()
	if _, known := m.tracks[r.ID]; !known {
		m.tracks[r.ID] = r // its artwork address (SoundCloud) and length are needed
	}
	cmds := []tea.Cmd{m.fetchCover(r.ID)}
	if m.deps.Info != nil && !m.infoAsked[r.ID] {
		ctx, cancel := context.WithCancel(context.Background())
		m.previewCancel, m.previewID = cancel, r.ID
		m.infoAsked[r.ID] = true
		fetch, id := m.deps.Info, r.ID
		cmds = append(cmds, func() tea.Msg {
			info, err := fetch(ctx, id, firstComments)
			if ctx.Err() != nil {
				err = context.Canceled
			}
			return infoMsg{id: id, info: info, err: err}
		})
	}
	return m, tea.Batch(cmds...)
}

// currentID is the video ID of the current entry ("" when idle).
func (m Model) currentID() string {
	if e := m.currentEntry(); e != nil {
		return yt.KeyOf(e.Filename)
	}
	return ""
}

// entryInfo resolves display data for a playlist entry, preferring what we
// know from search results over mpv's own title (which is the URL while the
// stream is still loading).
func (m Model) entryInfo(e playlistEntry) (title, channel string, dur float64) {
	if r, ok := m.tracks[yt.KeyOf(e.Filename)]; ok {
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
	if m.mode == modeHelp {
		maxOff := max(len(m.helpRows())-m.helpWindow(), 0)
		switch k {
		case keyEsc, keyEnter:
			m.mode = m.helpFrom
			m.clampQueue()
		case keyDown, keyShiftDown:
			m.helpOff = min(m.helpOff+1, maxOff)
		case keyUp, keyShiftUp:
			m.helpOff = max(m.helpOff-1, 0)
		}
		return m, nil
	}
	if m.mode == modeCommand {
		return m.handleCommandKey(msg)
	}
	if k == keyCommand && m.commandAllowed() {
		return m.openCommandLine()
	}
	if m.mode == modeDJ {
		return m.handleDJKey(msg)
	}
	if m.mode == modeDownload {
		return m.handleDownloadKey(msg)
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
	m.queueCancel()
	if m.searchCancel != nil {
		m.searchCancel()
	}
	if m.previewCancel != nil {
		m.previewCancel()
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
		if l := yt.ParseLink(q); l.Kind != yt.NotALink { // a link is loaded, not searched for
			m.input.Blur()
			m.mode = modeNormal
			if len(m.results) > 0 {
				m.mode = modeResults
			}
			return m.startLoad(l)
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
		res, failed, err := search(ctx, q)
		return searchDoneMsg{seq: seq, query: q, results: res, failed: failed, err: err}
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
	for _, src := range []yt.Source{yt.YouTube, yt.SoundCloud} {
		if err := msg.failed[src]; err != nil {
			name := map[yt.Source]string{yt.YouTube: "YouTube", yt.SoundCloud: "SoundCloud"}[src]
			return m, tea.Batch(m.setStatus(fmt.Sprintf("%s search failed: %v (showing the rest)", name, err), true), m.schedulePreview())
		}
	}
	return m, m.schedulePreview()
}

// handleResultsKey handles keys specific to the results list. handled is
// false when the key should fall through to the global keys.
func (m Model) handleResultsKey(k string) (Model, tea.Cmd, bool) {
	switch k {
	case keyDown:
		if m.selected < len(m.results)-1 {
			m.selected++
		}
		m.scrollIntoView()
		return m, m.schedulePreview(), true
	case keyUp:
		if m.selected > 0 {
			m.selected--
		}
		m.scrollIntoView()
		return m, m.schedulePreview(), true
	case keyEnter:
		if len(m.results) == 0 {
			return m, nil, true
		}
		next, cmd := m.playSelected()
		return next, cmd, true
	case keyEsc:
		m.mode = modeNormal
		return m, nil, true
	}
	return m, nil, false
}

// openSearch focuses the search bar, with the last query in it.
func (m Model) openSearch() (Model, tea.Cmd) {
	m.mode = modeSearch
	m.input.SetValue(m.query)
	m.input.CursorEnd()
	m.replaceOnType = m.query != ""
	focus := m.input.Focus()
	return m, focus
}

// queueSelected appends the selected result to the queue. There must be one.
func (m Model) queueSelected() (Model, tea.Cmd) {
	if m.currentEntry() != nil {
		m.rememberQueue("addition")
	}
	r := m.results[m.selected]
	m.tracks[r.ID] = r
	url, p := r.URL(), m.deps.Player
	return m, tea.Batch(
		m.action(false, func(ctx context.Context) error { return p.Queue(ctx, url) }),
		m.setStatus("Queued: "+r.Title, false),
	)
}

// downloadPlaying opens the save-to-file popup for the playing song.
func (m Model) downloadPlaying() (Model, tea.Cmd) {
	e := m.currentEntry()
	if e == nil {
		return m, m.setStatus("Nothing is playing", false)
	}
	title, channel, dur := m.entryInfo(*e)
	id := yt.KeyOf(e.Filename)
	return m.openDownload(yt.Result{ID: id, Title: title, Channel: channel, Duration: dur})
}

// askClear asks whether to clear the queue.
func (m Model) askClear() (Model, tea.Cmd) {
	if len(m.upcoming()) == 0 {
		return m, m.setStatus("Queue is empty", false)
	}
	m.confirmFrom, m.mode = m.mode, modeConfirm
	return m, nil
}

func (m Model) handleGlobalKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case keySearch:
		return m.openSearch()
	case keyLeft, keyRight:
		return m.seekKey(k)
	case keyUndo:
		return m.undoQueue()
	case keyVolUp:
		return m.changeVolume(volStep)
	case keyVolDown:
		return m.changeVolume(-volStep)
	case keyQueueFocus:
		if len(m.upcoming()) == 0 {
			return m, m.setStatus("Queue is empty", false)
		}
		m.queueReturn = m.mode
		m.mode = modeQueue
		m.queueCursor, m.queueOff = 0, 0
		return m, nil
	case keyPause:
		p := m.deps.Player
		return m, m.action(false, func(ctx context.Context) error { return p.TogglePause(ctx) })
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
	case keyMoveDown:
		k = keyShiftDown
	case keyMoveUp:
		k = keyShiftUp
	}
	switch k {
	case keyEnter:
		next, cmd := m.playQueueEntry()
		return next, cmd, true
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
	if k == keyRemove {
		m.rememberQueue("removal")
	} else {
		m.rememberQueue("move")
	}
	m.sorted = false
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

// seekStep and volStep are what the arrow keys and + - move by.
const (
	seekStep = 5.0 // seconds
	volStep  = 5.0 // percent
)

// undoDepth is how many queue edits can be undone.
const undoDepth = 20

// undoEntry is the upcoming queue as it was before an edit.
type undoEntry struct {
	what    string // what the edit was, for the status line
	entries []playlistEntry
}

// rememberQueue remembers the upcoming queue before an edit, so :undo can bring
// it back.
func (m *Model) rememberQueue(what string) {
	e := undoEntry{what: what, entries: slices.Clone(m.upcoming())}
	m.undo = append(slices.Clone(m.undo), e)
	if len(m.undo) > undoDepth {
		m.undo = m.undo[len(m.undo)-undoDepth:]
	}
}

// undoQueue brings back the upcoming queue from before the last edit.
func (m Model) undoQueue() (Model, tea.Cmd) {
	if m.queueBusy {
		return m, nil // an edit is still on its way
	}
	n := len(m.undo)
	if n == 0 || m.currentEntry() == nil {
		status := m.setStatus("Nothing to undo", false)
		return m, status
	}
	e := m.undo[n-1]
	m.undo = slices.Clone(m.undo[:n-1])
	m.playlist = append(slices.Clone(m.playlist[:m.pos+1]), e.entries...)
	m.sorted = false
	m.clampQueue()
	m.clampDJ()
	m.queueBusy = true
	urls := make([]string, len(e.entries))
	for i, entry := range e.entries {
		urls[i] = entry.Filename
	}
	p := m.deps.Player
	restore := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return queueEditDoneMsg{err: p.SetUpcoming(ctx, urls)}
	}
	status := m.setStatus("Undid the "+e.what, false)
	return m, tea.Batch(restore, status)
}

// playSelected plays the selected result now. There must be one.
func (m Model) playSelected() (Model, tea.Cmd) {
	r := m.results[m.selected]
	m.tracks[r.ID] = r
	url, p := r.URL(), m.deps.Player
	return m, tea.Batch(
		m.action(false, func(ctx context.Context) error { return p.PlayNow(ctx, url) }),
		m.setStatus("Playing: "+r.Title, false),
	)
}

// playQueueEntry plays the highlighted queue entry now: it leaves its place and
// the songs around it keep their order.
func (m Model) playQueueEntry() (Model, tea.Cmd) {
	up := m.upcoming()
	if m.queueBusy || len(up) == 0 {
		return m, nil
	}
	idx := m.pos + 1 + min(m.queueCursor, len(up)-1)
	e := m.playlist[idx]
	title, _, _ := m.entryInfo(e)
	url, p := e.Filename, m.deps.Player
	return m, tea.Batch(
		m.action(false, func(ctx context.Context) error {
			if err := p.Remove(ctx, idx); err != nil {
				return err
			}
			return p.PlayNow(ctx, url)
		}),
		m.setStatus("Playing: "+safe(title), false),
	)
}

// seekKey is the left and right arrows: seek seekStep seconds back or forward.
func (m Model) seekKey(k string) (Model, tea.Cmd) {
	d := seekStep
	if k == keyLeft {
		d = -seekStep
	}
	p := m.deps.Player
	return m, m.action(true, func(ctx context.Context) error { return p.SeekBy(ctx, d) })
}

// setVolume sets the volume and says so. The level is what the user chose;
// setting one ends a mute.
func (m Model) setVolume(v float64) (Model, tea.Cmd) {
	m.volume, m.muted = math.Max(0, math.Min(v, 100)), false
	return m.sendVolume("Volume " + strconv.FormatFloat(m.volume, 'f', -1, 64) + "%")
}

// changeVolume moves the volume by delta percent.
func (m Model) changeVolume(delta float64) (Model, tea.Cmd) { return m.setVolume(m.volume + delta) }

// sendVolume tells the player the volume that is in effect (0 when muted).
func (m Model) sendVolume(status string) (Model, tea.Cmd) {
	v := m.volume
	if m.muted {
		v = 0
	}
	p := m.deps.Player
	set := m.action(false, func(ctx context.Context) error { return p.SetVolume(ctx, v) })
	say := m.setStatus(status, false)
	return m, tea.Batch(set, say)
}

// tempoOf is the BPM used for a song: the one the user set, else the detected
// one, 0 if there is none (not analysed yet, failed, or no steady beat).
func (m Model) tempoOf(id string) float64 {
	if b, ok := m.bpmSet[id]; ok {
		return b
	}
	if st := m.stats[id]; st.hasRes {
		return st.res.BPM
	}
	return 0
}

// djRowCount is the number of rows in the DJ popup: the playing song and the
// upcoming ones.
func (m Model) djRowCount() int {
	if m.currentEntry() == nil {
		return 0
	}
	return 1 + len(m.upcoming())
}

// djEntry is the playlist entry of row r (0 is the playing song).
func (m Model) djEntry(r int) playlistEntry {
	if r == 0 {
		return *m.currentEntry()
	}
	return m.upcoming()[r-1]
}

// djWindow is how many rows the popup shows at once on this terminal.
func (m Model) djWindow() int {
	overhead := 4 // top border, autodj line, keys line, bottom border
	return max(min(m.djRowCount(), m.height-2-overhead), 1)
}

func (m *Model) scrollDJ() {
	win := m.djWindow()
	if m.djCursor < m.djOff {
		m.djOff = m.djCursor
	}
	if m.djCursor >= m.djOff+win {
		m.djOff = m.djCursor - win + 1
	}
	m.djOff = max(min(m.djOff, m.djRowCount()-win), 0)
}

// clampDJ keeps the popup's selection valid after the playlist changed.
func (m *Model) clampDJ() {
	m.djCursor = max(min(m.djCursor, m.djRowCount()-1), 0)
	m.scrollDJ()
}

// openDJ shows the DJ popup. From now on queued songs are analysed in the
// background, so their tempo is there by the time it is wanted.
func (m Model) openDJ() (Model, tea.Cmd) {
	m.djFrom, m.mode = m.mode, modeDJ
	m.djActive = true
	m.djCursor, m.djOff = min(1, len(m.upcoming())), 0 // the first queued song
	m.scrollDJ()
	return m, m.startQueuedAnalyses()
}

func (m Model) handleDJKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEsc:
		m.mode = m.djFrom
		m.clampQueue()
	case keyDown, keyShiftDown:
		m.djCursor = min(m.djCursor+1, max(m.djRowCount()-1, 0))
		m.scrollDJ()
	case keyUp, keyShiftUp:
		m.djCursor = max(m.djCursor-1, 0)
		m.scrollDJ()
	case keyUndo:
		return m.undoQueue()
	case keyVolUp:
		return m.changeVolume(volStep)
	case keyVolDown:
		return m.changeVolume(-volStep)
	}
	return m, nil
}

// toggleAutoDJ switches autodj on or off (the player is told from a cmd: it may
// have to stop a transition, which takes a moment).
func (m Model) toggleAutoDJ() (Model, tea.Cmd) {
	if m.deps.SetAutoDJ == nil {
		return m, m.setStatus("Autodj is not available", true)
	}
	m.autoDJ = !m.autoDJ
	on, set, songs := m.autoDJ, m.deps.SetAutoDJ, m.songs.lookup
	status := "Autodj off"
	if on {
		status = "Autodj on: the next song starts under the end of this one"
	}
	cmds := []tea.Cmd{func() tea.Msg { set(on, songs); return nil }, m.setStatus(status, false)}
	if on {
		// autodj plans from the beat grids: the playing song and the queue are
		// analysed completely from now on, as when the DJ popup is opened
		m.djActive = true
		cmds = append(cmds, m.startQueuedAnalyses())
	}
	return m, tea.Batch(cmds...)
}

// nextTransition says in words how the playing song will hand over to the next.
func (m Model) nextTransition() string {
	cur, up := m.currentEntry(), m.upcoming()
	if cur == nil || len(up) == 0 {
		return "nothing queued after this song"
	}
	a, _ := m.songs.lookup(cur.Filename)
	b, _ := m.songs.lookup(up[0].Filename)
	return autodj.For(a, b).Why
}

// Limits of a BPM set by hand.
const (
	minSetBPM = 30
	maxSetBPM = 300
)

// parseBPM reads the text of a BPM set by hand: empty means go back to the
// detected one (clear). warn is what is wrong with it, if anything.
func parseBPM(text string) (v float64, clear bool, warn string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, true, ""
	}
	v, err := strconv.ParseFloat(text, 64)
	switch {
	case err != nil:
		return 0, false, "That is not a number"
	case v < minSetBPM || v > maxSetBPM:
		return 0, false, fmt.Sprintf("BPM must be between %d and %d", minSetBPM, maxSetBPM)
	}
	return v, false, ""
}

// setBPM sets the BPM of the popup's selected song to text (see parseBPM). A bad
// text changes nothing and is returned as warn.
func (m Model) setBPM(text string) (next Model, status, warn string) {
	v, clear, warn := parseBPM(text)
	if warn != "" {
		return m, "", warn
	}
	e := m.djEntry(m.djCursor)
	id := yt.KeyOf(e.Filename)
	title, _, _ := m.entryInfo(e)
	if clear {
		delete(m.bpmSet, id)
		status = "BPM of " + safe(title) + " is the detected one again"
	} else {
		m.bpmSet[id] = v
		status = fmt.Sprintf("Set BPM of %s to %s", safe(title), strconv.FormatFloat(v, 'f', -1, 64))
	}
	m.sorted = false
	return m, status, ""
}

// maybeResort sorts the queue again, in the direction it was sorted in, once
// every song a radio added is in the queue and analysed (or failed to be): the
// new songs are sorted in without :sort being pressed twice.
func (m Model) maybeResort() (Model, tea.Cmd) {
	if len(m.resortIDs) == 0 || m.queueBusy {
		return m, nil
	}
	if !m.autoDJ || !m.sorted {
		m.resortIDs = nil
		return m, nil
	}
	inQueue := map[string]bool{}
	for _, e := range m.upcoming() {
		inQueue[yt.KeyOf(e.Filename)] = true
	}
	for id := range m.resortIDs {
		st := m.stats[id]
		if !inQueue[id] || (!st.hasRes && !st.analysisFailed && m.deps.Analyze != nil) {
			return m, nil
		}
	}
	m.resortIDs = nil
	return m.sortQueueByBPM()
}

// sortQueueByBPM arranges the upcoming songs so that each follows the one
// before it smoothly (see autodj.Arrange): the playing song is fixed, and the
// songs after it drift in tempo and key from it instead of jumping to the
// slowest or the fastest. Songs without a known BPM go last in their old
// order. The selection stays on its song. Like the other queue edits it
// updates the list at once and lets the player's events confirm it.
func (m Model) sortQueueByBPM() (Model, tea.Cmd) {
	up := m.upcoming()
	if m.queueBusy || len(up) < 2 {
		return m, nil
	}
	m.rememberQueue("sort")
	// tracks[0] is the playing song, tracks[1+i] the i-th upcoming one
	tracks := make([]autodj.Track, 1+len(up))
	rest := make([]int, len(up))
	pending, noBPM := 0, 0
	trackOf := func(id string) autodj.Track {
		t := autodj.Track{BPM: m.tempoOf(id)}
		if st := m.stats[id]; st.hasRes {
			t.Camelot = st.res.Camelot
		}
		return t
	}
	tracks[0] = trackOf(yt.KeyOf(m.currentEntry().Filename))
	for i, e := range up {
		id := yt.KeyOf(e.Filename)
		st := m.stats[id]
		tracks[1+i], rest[i] = trackOf(id), 1+i
		switch {
		case tracks[1+i].BPM > 0:
		case !st.hasRes && !st.analysisFailed && m.deps.Analyze != nil:
			pending++
		default:
			noBPM++
		}
	}
	// A song that has hardly started is not worth keeping: the whole queue is
	// arranged, the playing song included, and the best opener plays.
	free := m.elapsed < freshSeconds && tracks[0].BPM > 0
	type item struct{ idx int }
	var order []item
	if free {
		for _, k := range autodj.Arrange(tracks, -1, append([]int{0}, rest...)) {
			order = append(order, item{k}) // relative to the playing song
		}
	} else {
		for _, k := range autodj.Arrange(tracks, 0, rest) {
			order = append(order, item{k - 1})
		}
	}

	pl := append([]playlistEntry(nil), m.playlist...) // never mutate the shared slice
	base, shift := m.pos+1, 1                         // shift: the row of the popup is one more than the upcoming index
	if free {
		base, shift = m.pos, 0
	}
	urls := make([]string, len(order))
	cursor := m.djCursor
	for k, it := range order {
		pl[base+k] = m.playlist[base+it.idx]
		urls[k] = pl[base+k].Filename
		if m.djCursor >= 1 && it.idx == m.djCursor-shift {
			cursor = k + shift // the selection stays on the same song
		}
	}
	if free && urls[0] != m.playlist[m.pos].Filename {
		m.elapsed = 0 // another song starts
	}
	m.playlist, m.djCursor, m.sorted = pl, cursor, true
	m.scrollDJ()
	m.queueBusy = true

	status := "Queue arranged to flow from the playing song"
	if free {
		status = "Queue arranged from the best opener"
	}
	var notes []string
	if pending > 0 {
		notes = append(notes, fmt.Sprintf("%d still being analysed", pending))
	}
	if noBPM > 0 {
		notes = append(notes, fmt.Sprintf("%d without a BPM", noBPM))
	}
	if len(notes) > 0 {
		status += " (" + strings.Join(notes, ", ") + " at the end)"
	}
	p := m.deps.Player
	return m, tea.Batch(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		if free {
			return queueEditDoneMsg{err: p.ArrangeFromCurrent(ctx, urls)}
		}
		return queueEditDoneMsg{err: p.ReorderUpcoming(ctx, urls)}
	}, m.setStatus(status, false))
}

// :radio looks for radioDefault similar songs unless told how many (at most
// radioMax).
const (
	radioDefault = 10
	radioMax     = 50
)

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
		if id := yt.KeyOf(e.Filename); id != "" && !seen[id] {
			seen[id] = true
			seeds = append(seeds, id)
		}
	}
	return seeds
}

func (m Model) startRadio(n int) (Model, tea.Cmd) {
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
		exclude[yt.KeyOf(e.Filename)] = true
	}
	m.radioBusy = true
	m.setStickyStatus("Finding similar songs…")
	similar := m.deps.Similar
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := similar(ctx, seeds, exclude, n)
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
	if m.currentEntry() != nil {
		m.rememberQueue("radio")
	}
	if m.sorted { // keep it sorted: the new songs are sorted in once they are analysed (if autodj is still on)
		m.resortIDs = map[string]bool{}
		for _, r := range msg.results {
			m.resortIDs[r.ID] = true
		}
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
		m.rememberQueue("clearing of the queue")
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

// openDownload shows the "save to file" popup for a track.
func (m Model) openDownload(r yt.Result) (Model, tea.Cmd) {
	if m.deps.Download == nil {
		return m, m.setStatus("Downloads are not available", true)
	}
	m.dlFrom, m.mode, m.dlTrack = m.mode, modeDownload, r
	m.dlOptions, m.dlWarn = nil, ""
	m.dlInput.SetValue(defaultDownloadPath(r.Title))
	m.dlInput.CursorEnd()
	m.dlInput.Width = max(min(64, m.width)-6, 10)
	return m, m.dlInput.Focus()
}

// handleDownloadKey handles the popup: Tab completes the path, Enter saves
// (twice if the file exists), Esc cancels.
func (m Model) handleDownloadKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEsc:
		m.mode = m.dlFrom
		m.dlInput.Blur()
		m.clampQueue()
		return m, nil
	case keyComplete:
		done, opts := completePath(m.dlInput.Value())
		m.dlInput.SetValue(done)
		m.dlInput.CursorEnd()
		m.dlOptions, m.dlWarn = nil, ""
		if len(opts) > 1 {
			m.dlOptions = opts
		}
		return m, nil
	case keyEnter:
		path := strings.TrimSpace(m.dlInput.Value())
		if path == "" || strings.HasSuffix(path, "/") {
			m.dlWarn = "Enter a file name"
			return m, nil
		}
		path = expandHome(path)
		base, format := yt.SplitAudioPath(path)
		path = base + "." + format
		if _, err := os.Stat(path); err == nil && m.dlWarn != "File exists - Enter again to overwrite" {
			m.dlWarn = "File exists - Enter again to overwrite"
			return m, nil
		}
		m.mode = m.dlFrom
		m.dlInput.Blur()
		m.clampQueue()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return m, m.setStatus(err.Error(), true)
		}
		m.setStickyStatus("Downloading " + safe(m.dlTrack.Title) + "…")
		dl, id := m.deps.Download, m.dlTrack.ID
		return m, func() tea.Msg {
			return downloadDoneMsg{path: path, err: dl(context.Background(), id, path)}
		}
	}
	m.dlOptions, m.dlWarn = nil, ""
	var cmd tea.Cmd
	m.dlInput, cmd = m.dlInput.Update(msg)
	return m, cmd
}

// freshSeconds is how far into a song it has been playing for it to still count
// as not started: the queue is arranged around it only after that.
const freshSeconds = 3.0
