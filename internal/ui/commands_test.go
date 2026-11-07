package ui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/PatrickSteil/ytplay/internal/yt"
)

// typeCommand types a command line: ":", the text, Enter. It returns what the last
// key (Enter) produced.
func typeCommand(m Model, text string) (Model, tea.Cmd) {
	keys := []string{":"}
	for _, r := range text {
		keys = append(keys, string(r))
	}
	return send(m, append(keys, "enter")...)
}

// editCommand types a command and runs what it started, so the busy flags of
// queue edits are released.
func editCommand(m Model, text string) Model {
	m, cmd := typeCommand(m, text)
	return run(m, cmd)
}

func statusText(m Model) string { return ansi.Strip(m.statusLine()) }

// ---- the parser ----

func TestParseCommandLine(t *testing.T) {
	for line, want := range map[string][2]string{
		"find daft punk": {"find", "daft punk"},
		"  f   x y ":     {"f", "x y"},
		"skip":           {"skip", ""},
		"bpm 128":        {"bpm", "128"},
		"":               {"", ""},
		"   ":            {"", ""},
	} {
		if word, arg := parseCommandLine(line); word != want[0] || arg != want[1] {
			t.Errorf("parseCommandLine(%q) = %q, %q; want %q, %q", line, word, arg, want[0], want[1])
		}
	}
}

func TestCommandNamesShortFormsAndPrefixes(t *testing.T) {
	for word, want := range map[string]string{
		// names
		"find": "find", "add": "add", "skip": "skip", "delete": "delete", "download": "download",
		"auto": "auto", "dj": "dj", "sort": "sort", "bpm": "bpm", "radio": "radio",
		"clear": "clear", "help": "help", "quit": "quit",
		// short forms
		"f": "find", "s": "skip", "x": "delete", "d": "download", "a": "auto", "b": "sort",
		"e": "bpm", "r": "radio", "X": "clear", "h": "help", "q": "quit",
		// unique prefixes
		"fin": "find", "ad": "add", "sk": "skip", "de": "delete", "do": "download", "down": "download",
		"au": "auto", "so": "sort", "bp": "bpm", "ra": "radio", "cl": "clear", "he": "help", "qu": "quit",
	} {
		c, candidates := findCommand(word)
		if c == nil || c.name != want {
			t.Errorf("findCommand(%q) = %v %v, want %q", word, c, candidates, want)
		}
	}
	for _, word := range []string{"", "frobnicate", "S", "Skip", "z"} { // names and short forms are case sensitive
		if c, _ := findCommand(word); c != nil {
			t.Errorf("findCommand(%q) = %q, want no command", word, c.name)
		}
	}
}

func TestAnAmbiguousPrefixSaysWhatItCouldBe(t *testing.T) {
	// a short form wins over the prefixes it is also the start of
	withShort := []command{{name: "delete"}, {name: "download", short: []string{"d"}}, {name: "dj"}}
	if c, _ := resolveCommand(withShort, "d"); c == nil || c.name != "download" {
		t.Errorf("d with a short form: got %v, want download", c)
	}
	// without one the word is ambiguous: no command, and the names it could be
	two := []command{{name: "delete"}, {name: "download"}}
	c, candidates := resolveCommand(two, "d")
	if c != nil || strings.Join(candidates, ",") != "delete,download" {
		t.Errorf("ambiguous prefix: command %v, candidates %v, want none and delete,download", c, candidates)
	}
	if c, candidates := resolveCommand(two, "x"); c != nil || len(candidates) != 0 {
		t.Errorf("unknown word: %v %v, want nothing", c, candidates)
	}
}

// ---- the command line ----

func TestColonOpensTheCommandLineFromEveryStateAndEscGoesBack(t *testing.T) {
	states := map[string]func(t *testing.T) Model{
		"normal":  func(t *testing.T) Model { return newTestModel() },
		"results": func(t *testing.T) Model { return withResults(t, 3) },
		"queue": func(t *testing.T) Model {
			m, _ := send(queueModel(t, 4), "tab")
			return m
		},
		"dj": func(t *testing.T) Model { return djModel(t, 4, nil) },
	}
	for name, build := range states {
		m := build(t)
		before := m.mode
		m, _ = send(m, ":")
		if m.mode != modeCommand {
			t.Errorf("%s: mode = %v after :, want the command line", name, m.mode)
			continue
		}
		if got := statusText(m); !strings.HasPrefix(got, ":") {
			t.Errorf("%s: the bottom line is %q, want the command line", name, got)
		}
		m, _ = send(m, "esc")
		if m.mode != before {
			t.Errorf("%s: mode = %v after Esc, want %v again", name, m.mode, before)
		}
		m, _ = send(m, ":", "backspace") // Backspace on an empty line cancels too
		if m.mode != before {
			t.Errorf("%s: mode = %v after Backspace on an empty line, want %v", name, m.mode, before)
		}
		m, _ = send(m, ":", "enter") // so does an empty line
		if m.mode != before || m.status != "" {
			t.Errorf("%s: after an empty command mode = %v status %q, want %v and no status", name, m.mode, m.status, before)
		}
	}
}

func TestColonIsTextWhereTextIsTyped(t *testing.T) {
	m, _ := send(newTestModel(), "/", "a", ":", "b")
	if m.mode != modeSearch || m.input.Value() != "a:b" {
		t.Errorf("search bar: mode %v value %q, want a colon typed into the bar", m.mode, m.input.Value())
	}

	q := queueModel(t, 3)
	q, _ = typeCommand(q, "clear") // the clear-the-queue question
	q, _ = send(q, ":")
	if q.mode != modeConfirm {
		t.Errorf("yes/no popup: mode = %v after :, want it still asking", q.mode)
	}

	h, _ := typeCommand(newTestModel(), "help")
	h, _ = send(h, ":")
	if h.mode != modeHelp {
		t.Errorf("help popup: mode = %v after :, want it still open", h.mode)
	}

	d := withResults(t, 2)
	d.deps.Download = func(ctx context.Context, id, path string) error { return nil }
	d, _ = typeCommand(d, "download")
	d, _ = send(d, ":")
	if d.mode != modeDownload || !strings.HasSuffix(d.dlInput.Value(), ":") {
		t.Errorf("download popup: mode %v path %q, want a colon typed into the path", d.mode, d.dlInput.Value())
	}
}

// line is what the command line shows, without the cursor.
func line(m Model) string { return strings.TrimSpace(statusText(m)) }

func TestTypingInTheCommandLineAndHistory(t *testing.T) {
	m := newTestModel()
	m, _ = send(m, ":", "s", "k", "x", "backspace")
	if got := line(m); got != ":sk" {
		t.Errorf("bottom line = %q, want :sk", got)
	}
	m, _ = send(m, "esc")

	m, _ = typeCommand(m, "skip") // run, and remembered
	m, _ = typeCommand(m, "s")
	m, _ = send(m, ":")
	if got := line(m); got != ":" {
		t.Fatalf("a fresh line shows %q, want it empty", got)
	}
	m, _ = send(m, "k") // the letter k is text here, not "up"
	if got := line(m); got != ":k" {
		t.Errorf("after typing k the line = %q, want :k", got)
	}
	m, _ = send(m, "backspace", "up")
	if got := line(m); got != ":s" {
		t.Errorf("after Up the line = %q, want the last command, s", got)
	}
	m, _ = send(m, "up")
	if got := line(m); got != ":skip" {
		t.Errorf("after Up twice the line = %q, want skip", got)
	}
	m, _ = send(m, "up")
	if got := line(m); got != ":skip" {
		t.Errorf("Up past the oldest changed the line to %q", got)
	}
	m, _ = send(m, "down")
	if got := line(m); got != ":s" {
		t.Errorf("after Down the line = %q, want s again", got)
	}
	m, _ = send(m, "down")
	if got := line(m); got != ":" {
		t.Errorf("after Down past the newest the line = %q, want what was typed before (nothing)", got)
	}
}

func TestTheSameCommandTwiceIsRememberedOnce(t *testing.T) {
	m := newTestModel()
	m, _ = typeCommand(m, "skip")
	m, _ = typeCommand(m, "skip")
	if len(m.cmdHistory) != 1 {
		t.Errorf("history = %q, want skip once", m.cmdHistory)
	}
}

func TestTabCompletesACommandName(t *testing.T) {
	for typed, want := range map[string]string{
		"do":     ":download",
		"fi":     ":find",
		"de":     ":delete",
		"r":      ":radio",
		"zzz":    ":zzz", // nothing to complete to
		"find d": ":find d",
	} {
		m, _ := send(newTestModel(), ":")
		for _, r := range typed {
			m, _ = send(m, string(r))
		}
		m, _ = send(m, "tab")
		if got := line(m); got != want {
			t.Errorf(":%s and Tab: the line = %q, want %q", typed, got, want)
		}
		if m.mode != modeCommand {
			t.Errorf(":%s and Tab left the command line: mode %v", typed, m.mode)
		}
	}
	// where the names agree as far as a common start, Tab goes that far
	if got := completeCommand("d"); got != "d" {
		t.Errorf("completeCommand(d) = %q, d is the start of delete, download and dj: nothing common beyond it", got)
	}
	if got := completeCommand("c"); got != "c" {
		t.Errorf("completeCommand(c) = %q: clear and comments share nothing beyond c", got)
	}
	for in, want := range map[string]string{"cl": "clear ", "co": "comments ", "ra": "radio "} {
		if got := completeCommand(in); got != want {
			t.Errorf("completeCommand(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	m := queueModel(t, 3)
	m, _ = send(m, "tab")
	m, _ = typeCommand(m, "frobnicate now")
	if m.mode != modeQueue {
		t.Errorf("mode = %v, want the queue focus back", m.mode)
	}
	if !m.statusIsErr || !strings.Contains(m.status, "Unknown command: frobnicate") {
		t.Errorf("status = %q (error %v), want Unknown command: frobnicate", m.status, m.statusIsErr)
	}
}

// ---- the commands ----

func TestSkipAndQuitCommands(t *testing.T) {
	for _, word := range []string{"skip", "s", "sk"} {
		m, cmd := typeCommand(queueModel(t, 2), word)
		m = run(m, cmd)
		if got := actionsOf(m).calls; len(got) != 1 || got[0] != "skip" {
			t.Errorf(":%s: calls = %q, want one skip", word, got)
		}
	}
	for _, word := range []string{"quit", "q", "qu"} {
		if _, cmd := typeCommand(newTestModel(), word); !isQuit(cmd) {
			t.Errorf(":%s did not quit", word)
		}
	}
}

func TestFindCommandSearches(t *testing.T) {
	for _, word := range []string{"find daft punk", "f daft punk", "fin   daft punk"} {
		m, cmd := typeCommand(newTestModel(), word)
		if m.query != "daft punk" || cmd == nil || m.status != "Searching…" {
			t.Errorf(":%s: query %q status %q cmd %v, want a search for daft punk", word, m.query, m.status, cmd != nil)
		}
		if m.input.Value() != "daft punk" {
			t.Errorf(":%s: the search bar holds %q, want the query so / shows it", word, m.input.Value())
		}
	}
	for _, word := range []string{"find", "f"} {
		if m, _ := typeCommand(newTestModel(), word); m.mode != modeSearch {
			t.Errorf(":%s without a name: mode %v, want the search bar", word, m.mode)
		}
	}
}

func TestAddCommandQueuesTheHighlightedResult(t *testing.T) {
	for _, word := range []string{"add", "ad"} {
		m := withResults(t, 3)
		m, _ = send(m, "j") // highlight the second result
		m, cmd := typeCommand(m, word)
		m = run(m, cmd)
		if got := lastCall(m); got != "queue https://www.youtube.com/watch?v=b" {
			t.Errorf(":%s: last call %q, want the highlighted result queued", word, got)
		}
	}

	m, cmd := typeCommand(newTestModel(), "add") // no results at all
	m = run(m, cmd)
	if len(actionsOf(m).calls) != 0 || !m.statusIsErr {
		t.Errorf("without results: calls %q status %q (error %v), want an error and no call", actionsOf(m).calls, m.status, m.statusIsErr)
	}

	// the results keep their highlight while the queue has the focus
	q := queueModel(t, 3)
	q.results, q.selected = results(2), 1
	q, _ = send(q, "tab")
	q, cmd = typeCommand(q, "add")
	q = run(q, cmd)
	if got := lastCall(q); got != "queue https://www.youtube.com/watch?v=b" {
		t.Errorf("with the queue focused: last call %q, want the highlighted result queued", got)
	}
}

func TestDeleteCommandRemovesTheHighlightedQueueEntry(t *testing.T) {
	for _, word := range []string{"delete", "x", "de"} {
		m := queueModel(t, 4)
		m, _ = send(m, "tab", "j") // e2 is highlighted
		m, cmd := typeCommand(m, word)
		m = run(m, cmd)
		if got := lastCall(m); got != "remove 2" {
			t.Errorf(":%s: last call %q, want entry 2 removed", word, got)
		}
	}
	for name, m := range map[string]Model{"normal": queueModel(t, 3), "results": withResults(t, 2)} {
		m, cmd := typeCommand(m, "delete")
		m = run(m, cmd)
		if len(actionsOf(m).calls) != 0 || !strings.Contains(m.status, "Tab") {
			t.Errorf("%s: calls %q status %q, want nothing removed and a hint about Tab", name, actionsOf(m).calls, m.status)
		}
	}
}

func downloadModel(m Model) Model {
	m.deps.Download = func(ctx context.Context, id, path string) error { return nil }
	return m
}

func TestDownloadCommandSavesTheHighlightedSong(t *testing.T) {
	for _, word := range []string{"download", "d", "do"} {
		m := downloadModel(withResults(t, 3))
		m, _ = send(m, "j")
		m, _ = typeCommand(m, word)
		if m.mode != modeDownload || m.dlTrack.ID != "b" {
			t.Errorf(":%s from the results: mode %v track %q, want the download popup for b", word, m.mode, m.dlTrack.ID)
		}
	}
	q := downloadModel(queueModel(t, 4))
	q, _ = send(q, "tab", "j") // e2
	q, _ = typeCommand(q, "d")
	if q.mode != modeDownload || q.dlTrack.ID != "e2" {
		t.Errorf(":d from the queue: mode %v track %q, want the download popup for e2", q.mode, q.dlTrack.ID)
	}
	n := downloadModel(queueModel(t, 2)) // nothing highlighted: the playing song
	n, _ = typeCommand(n, "d")
	if n.mode != modeDownload || n.dlTrack.ID != "e0" {
		t.Errorf(":d with nothing highlighted: mode %v track %q, want the playing song e0", n.mode, n.dlTrack.ID)
	}
	e := downloadModel(newTestModel())
	e, _ = typeCommand(e, "d")
	if e.mode == modeDownload || !strings.Contains(e.status, "Nothing is playing") {
		t.Errorf(":d with nothing playing: mode %v status %q", e.mode, e.status)
	}
	// esc goes back to where the popup came from
	m := downloadModel(withResults(t, 2))
	m, _ = typeCommand(m, "d")
	m, _ = send(m, "esc")
	if m.mode != modeResults {
		t.Errorf("after Esc the mode is %v, want the results", m.mode)
	}
}

func TestAutoCommandWorksFromAnywhere(t *testing.T) {
	m := queueModel(t, 3)
	rec := &autoRecorder{}
	m.deps.SetAutoDJ = rec.set
	for _, word := range []string{"auto", "a"} {
		m, cmd := typeCommand(m, word)
		m = run(m, cmd)
		if on, called := rec.last(); !called || !on || !m.autoDJ {
			t.Errorf(":%s from the normal state: on %v called %v model %v, want autodj on", word, on, called, m.autoDJ)
		}
		if !m.djActive {
			t.Errorf(":%s did not switch on the DJ analyses (djActive false)", word)
		}
		if m.mode == modeDJ {
			t.Errorf(":%s opened the popup, it only switches autodj", word)
		}
		m, cmd = typeCommand(m, word)
		m = run(m, cmd)
		if on, _ := rec.last(); on || m.autoDJ {
			t.Errorf(":%s again: on %v model %v, want autodj off", word, on, m.autoDJ)
		}
	}
	// and inside the popup
	p, rec2 := autoModel(t, 3)
	p, cmd := typeCommand(p, "a")
	p = run(p, cmd)
	if on, called := rec2.last(); !called || !on || p.mode != modeDJ {
		t.Errorf(":a in the popup: on %v called %v mode %v", on, called, p.mode)
	}
}

func TestDJCommandOpensAndClosesThePopup(t *testing.T) {
	m, _ := typeCommand(queueModel(t, 3), "dj")
	if m.mode != modeDJ {
		t.Fatalf("mode = %v after :dj", m.mode)
	}
	m, _ = typeCommand(m, "dj")
	if m.mode != modeNormal {
		t.Errorf("mode = %v after :dj in the popup, want it closed", m.mode)
	}
}

func TestSortAndBPMCommandsWorkInTheDJPopup(t *testing.T) {
	m := djModel(t, 4, map[string]float64{"e1": 140, "e2": 100, "e3": 120})
	for _, word := range []string{"sort", "b"} {
		m, cmd := typeCommand(m, word)
		m = run(m, cmd)
		if got := reorderCall(m); got != "e2,e3,e1" && got != "e1,e3,e2" {
			t.Errorf(":%s: reorder call %q, want the queue ordered by BPM", word, got)
		}
	}

	m = djModel(t, 4, nil) // the cursor is on e1
	for _, word := range []string{"bpm 128", "e 128", "bp 128"} {
		m, _ = typeCommand(m, "bpm") // start from the detected value
		m, _ = typeCommand(m, word)
		if got := m.bpmSet["e1"]; got != 128 {
			t.Errorf(":%s: the BPM of e1 = %v, want 128 set by hand", word, got)
		}
	}
	m, _ = typeCommand(m, "bpm")
	if _, ok := m.bpmSet["e1"]; ok {
		t.Errorf(":bpm alone should go back to the detected tempo")
	}
	m, _ = typeCommand(m, "bpm abc")
	if !m.statusIsErr || !strings.Contains(m.status, "not a number") {
		t.Errorf(":bpm abc: status %q (error %v)", m.status, m.statusIsErr)
	}
	m, _ = typeCommand(m, "bpm 500")
	if !m.statusIsErr || !strings.Contains(m.status, "between 30 and 300") {
		t.Errorf(":bpm 500: status %q (error %v)", m.status, m.statusIsErr)
	}
	if _, ok := m.bpmSet["e1"]; ok || m.mode != modeDJ {
		t.Errorf("a bad BPM must change nothing and keep the popup open: set %v mode %v", m.bpmSet, m.mode)
	}
	d := djModel(t, 4, nil)
	d, _ = send(d, "j") // e2
	d, _ = typeCommand(d, "bpm 90")
	if d.bpmSet["e2"] != 90 {
		t.Errorf(":bpm 90 on the highlighted row: %v, want e2 set", d.bpmSet)
	}
}

func TestSortAndBPMCommandsNeedThePopup(t *testing.T) {
	for _, word := range []string{"sort", "b", "bpm 120", "e 120"} {
		m, cmd := typeCommand(queueModel(t, 4), word)
		m = run(m, cmd)
		if len(actionsOf(m).calls) != 0 || len(m.bpmSet) != 0 || !m.statusIsErr || !strings.Contains(m.status, ":dj") {
			t.Errorf(":%s outside the popup: calls %q set %v status %q, want a hint to open it with :dj", word, actionsOf(m).calls, m.bpmSet, m.status)
		}
	}
}

func TestClearHelpAndRadioCommands(t *testing.T) {
	for _, word := range []string{"clear", "X", "cl"} {
		m, _ := typeCommand(queueModel(t, 3), word)
		if m.mode != modeConfirm {
			t.Errorf(":%s: mode = %v, want the question", word, m.mode)
		}
	}
	if m, _ := typeCommand(newTestModel(), "clear"); m.mode == modeConfirm || !strings.Contains(m.status, "empty") {
		t.Errorf(":clear with nothing queued: mode %v status %q", m.mode, m.status)
	}
	for _, word := range []string{"help", "h", "he"} {
		m, _ := typeCommand(queueModel(t, 3), word)
		if m.mode != modeHelp {
			t.Errorf(":%s: mode = %v, want the help popup", word, m.mode)
		}
		m, _ = send(m, "esc")
		if m.mode != modeNormal {
			t.Errorf("Esc closed the help into mode %v", m.mode)
		}
	}
	m := queueModel(t, 3)
	called := false
	m.deps.Similar = func(ctx context.Context, seeds []string, exclude map[string]bool, n int) ([]yt.Result, error) {
		called = true
		return nil, nil
	}
	m, cmd := typeCommand(m, "radio")
	run(m, cmd)
	if !called {
		t.Errorf(":radio did not look for similar songs")
	}
}

func TestOnlyThePopupsCommandsRunWhileItIsOpen(t *testing.T) {
	for _, word := range []string{"find x", "skip", "add", "download", "delete", "radio", "clear"} {
		m := djModel(t, 4, nil)
		m, cmd := typeCommand(m, word)
		m = run(m, cmd)
		if m.mode != modeDJ || !m.statusIsErr || !strings.Contains(m.status, "Close the popup first") {
			t.Errorf(":%s in the popup: mode %v status %q, want the popup still open and a hint", word, m.mode, m.status)
		}
		if len(actionsOf(m).calls) != 0 {
			t.Errorf(":%s in the popup made calls %q", word, actionsOf(m).calls)
		}
	}
	for _, word := range []string{"help", "quit"} {
		m := djModel(t, 4, nil)
		m, cmd := typeCommand(m, word)
		if word == "quit" && !isQuit(cmd) {
			t.Errorf(":quit in the popup did not quit")
		}
		if word == "help" && m.mode != modeHelp {
			t.Errorf(":help in the popup: mode %v", m.mode)
		}
	}
}

// ---- the old letters ----

// deadLetters are the keys that used to do something and are commands now.
var deadLetters = []string{"s", "d", "q", "r", "h", "D", "X", "Q", "b", "e", "a", "y", "n", "p", "v", "m", "l"}

func TestOldLettersDoNothingOutsideTheCommandLine(t *testing.T) {
	states := map[string]func(t *testing.T) Model{
		"normal":  func(t *testing.T) Model { return downloadModel(queueModel(t, 4)) },
		"results": func(t *testing.T) Model { return downloadModel(withResults(t, 3)) },
		"queue": func(t *testing.T) Model {
			m, _ := send(downloadModel(queueModel(t, 4)), "tab")
			return m
		},
		"dj": func(t *testing.T) Model { return djModel(t, 4, nil) },
	}
	for name, build := range states {
		for _, letter := range deadLetters {
			m := build(t)
			m.deps.Similar = func(ctx context.Context, seeds []string, exclude map[string]bool, n int) ([]yt.Result, error) {
				t.Errorf("%s: %q looked for similar songs", name, letter)
				return nil, nil
			}
			rec := &autoRecorder{}
			m.deps.SetAutoDJ = rec.set
			calls, ids := len(actionsOf(m).calls), queueIDs(m)
			next, cmd := send(m, letter)
			for _, msg := range collect(cmd) { // the commands it started run, and none may be a quit
				if _, quit := msg.(tea.QuitMsg); quit {
					t.Errorf("%s: %q quit the program", name, letter)
				}
			}
			switch {
			case next.mode != m.mode:
				t.Errorf("%s: %q changed the mode from %v to %v", name, letter, m.mode, next.mode)
			case len(actionsOf(next).calls) != calls:
				t.Errorf("%s: %q made player calls %q", name, letter, actionsOf(next).calls[calls:])
			case queueIDs(next) != ids:
				t.Errorf("%s: %q changed the queue from %s to %s", name, letter, ids, queueIDs(next))
			case next.status != m.status:
				t.Errorf("%s: %q changed the status to %q", name, letter, next.status)
			case next.autoDJ != m.autoDJ:
				t.Errorf("%s: %q switched autodj", name, letter)
			case len(next.bpmSet) != len(m.bpmSet):
				t.Errorf("%s: %q set a BPM", name, letter)
			}
			if _, called := rec.last(); called {
				t.Errorf("%s: %q told the player about autodj", name, letter)
			}
		}
	}
}

func TestOnlyEscAndEnterCloseTheHelp(t *testing.T) {
	for _, letter := range []string{"h", "q", "Q"} {
		m, _ := typeCommand(newTestModel(), "help")
		m, cmd := send(m, letter)
		if m.mode != modeHelp || isQuit(cmd) {
			t.Errorf("%q in the help: mode %v quit %v, want the help still open", letter, m.mode, isQuit(cmd))
		}
	}
	for _, closer := range []string{"esc", "enter"} {
		m, _ := typeCommand(newTestModel(), "help")
		if m, _ = send(m, closer); m.mode != modeNormal {
			t.Errorf("%s in the help: mode %v, want it closed", closer, m.mode)
		}
	}
}

func TestTheKeysThatRemainStillWork(t *testing.T) {
	m := withResults(t, 3)
	if next, _ := send(m, "j"); next.selected != 1 {
		t.Errorf("j: selected %d, want 1", next.selected)
	}
	if next, cmd := send(m, "enter"); lastCallAfter(run(next, cmd)) != "playnow https://www.youtube.com/watch?v=a" {
		t.Errorf("Enter did not play the highlighted result")
	}
	if next, _ := send(m, "/"); next.mode != modeSearch {
		t.Errorf("/ : mode %v, want the search bar", next.mode)
	}
	q := queueModel(t, 4)
	if next, _ := send(q, "tab"); next.mode != modeQueue {
		t.Errorf("Tab: mode %v, want the queue", next.mode)
	}
	if next, cmd := send(q, " "); lastCallAfter(run(next, cmd)) != "pause" {
		t.Errorf("Space did not pause")
	}
	if next, cmd := send(q, "5"); lastCallAfter(run(next, cmd)) != "seek 50" {
		t.Errorf("5 did not seek to 50")
	}
	if next, cmd := send(q, "="); lastCallAfter(run(next, cmd)) != "seek 100" {
		t.Errorf("= did not seek to the end")
	}
	qq, _ := send(q, "tab")
	if next, cmd := send(qq, "x"); lastCallAfter(run(next, cmd)) != "remove 1" {
		t.Errorf("x did not remove the highlighted queue entry")
	}
	if _, cmd := send(q, "ctrl+c"); !isQuit(cmd) {
		t.Errorf("Ctrl+C did not quit")
	}
}

func lastCallAfter(m Model) string { return lastCall(m) }

// ---- the help ----

func helpText_(m Model) string { return ansi.Strip(strings.Join(m.helpBox(), "\n")) }

func TestHelpListsEveryCommandAndEveryKey(t *testing.T) {
	m := queueModel(t, 3)
	m.width, m.height = 100, 80 // room for all of it
	m, _ = typeCommand(m, "help")
	out := helpText_(m)
	for _, c := range commands {
		if !strings.Contains(out, c.usage) {
			t.Errorf("the help does not list %s:\n%s", c.usage, out)
		}
	}
	for _, k := range []string{"/", "j / k", "Enter", "Tab", "x", "Space", "0-9", "Ctrl+C", "Esc"} {
		if !strings.Contains(out, k) {
			t.Errorf("the help does not list the key %q:\n%s", k, out)
		}
	}
	for _, old := range []string{"q  add", "s  skip", "D  DJ", "X  clear"} {
		if strings.Contains(out, old) {
			t.Errorf("the help still lists the old key %q", old)
		}
	}
}

func TestHelpScrollsWhenTheTerminalIsSmall(t *testing.T) {
	m := queueModel(t, 3)
	m.width, m.height = 90, 22
	m, _ = typeCommand(m, "help")
	last := commands[len(commands)-1].usage
	if got := helpText_(m); strings.Contains(got, last) {
		t.Fatalf("a 22 row terminal should not fit all of the help, but %s is there already", last)
	}
	if box := m.helpBox(); len(box) > m.height-2 {
		t.Errorf("the help popup is %d rows on a %d row terminal", len(box), m.height)
	}
	for i := 0; i < 60; i++ {
		m, _ = send(m, "j")
	}
	if got := helpText_(m); !strings.Contains(got, last) {
		t.Errorf("after scrolling down the help should reach %s:\n%s", last, got)
	}
	if m.mode != modeHelp {
		t.Errorf("j closed the help: mode %v", m.mode)
	}
	// pressing j past the end must not store the overshoot: one k moves the view at once
	m, _ = send(m, "k")
	if got := helpText_(m); strings.Contains(got, last) {
		t.Errorf("one k after reaching the end should scroll up and hide %s again:\n%s", last, got)
	}
	for i := 0; i < 80; i++ {
		m, _ = send(m, "k")
	}
	if got := helpText_(m); strings.Contains(got, last) || !strings.Contains(got, "ytplay") {
		t.Errorf("scrolling back up should show the top again:\n%s", got)
	}
	if box := m.helpBox(); len(box) > m.height-2 {
		t.Errorf("after scrolling the popup is %d rows on a %d row terminal", len(box), m.height)
	}
}

func screen(m Model) string { return ansi.Strip(m.View()) }

// The DJ popup is where :sort and :bpm are used, so it must not vanish while
// the command line is open or after a command has run.
func TestTheDJPopupStaysOnScreenWhileACommandIsTyped(t *testing.T) {
	m := djModel(t, 4, map[string]float64{"e1": 120, "e2": 90})
	const popup, row = "┌ DJ", "Title-e2"
	if s := screen(m); !strings.Contains(s, popup) || !strings.Contains(s, row) {
		t.Fatalf("setup: the popup is not on screen:\n%s", s)
	}
	m, _ = send(m, ":")
	if s := screen(m); !strings.Contains(s, popup) || !strings.Contains(s, row) {
		t.Errorf("the popup vanished when the command line opened:\n%s", s)
	}
	for _, k := range []string{"s", "o", "r", "t"} {
		m, _ = send(m, k)
		if s := screen(m); !strings.Contains(s, popup) {
			t.Errorf("the popup vanished while typing (after %q):\n%s", k, s)
		}
	}
	if s := screen(m); !strings.Contains(s, ":sort") {
		t.Errorf("the command line is not shown under the popup:\n%s", s)
	}
	m, cmd := send(m, "enter")
	m = run(m, cmd)
	if s := screen(m); !strings.Contains(s, popup) || m.mode != modeDJ {
		t.Errorf("the popup is gone after :sort (mode %v):\n%s", m.mode, s)
	}
	// a second command straight after, and one that is cancelled
	m, _ = typeCommand(m, "bpm 100")
	if s := screen(m); !strings.Contains(s, popup) || m.mode != modeDJ {
		t.Errorf("the popup is gone after :bpm (mode %v):\n%s", m.mode, s)
	}
	m, _ = send(m, ":", "b", "esc")
	if s := screen(m); !strings.Contains(s, popup) || m.mode != modeDJ {
		t.Errorf("the popup is gone after cancelling a command (mode %v):\n%s", m.mode, s)
	}
}

// Popups that only ask or explain are not shown under the command line (it
// cannot be opened from them), and without a popup none is drawn.
func TestNoPopupIsDrawnUnderTheCommandLineWithoutOne(t *testing.T) {
	m, _ := send(queueModel(t, 3), ":")
	for _, title := range []string{"┌ DJ", "┌ ytplay help", "┌ Clear"} {
		if s := screen(m); strings.Contains(s, title) {
			t.Errorf("%q is drawn although no popup is open:\n%s", title, s)
		}
	}
}
