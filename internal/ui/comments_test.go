package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/PatrickSteil/ytplay/internal/yt"
)

func commentInfo(n int) yt.Info {
	var cs []yt.Comment
	for i := 1; i <= n; i++ {
		cs = append(cs, yt.Comment{Author: "@u", Text: fmt.Sprintf("Comment number %02d is here", i), Likes: i})
	}
	return yt.Info{Description: "A description", Comments: cs}
}

type infoCall struct {
	id       string
	comments int
}

// commentsFake is yt.FetchInfo for a video that has `total` comments.
type commentsFake struct {
	mu    sync.Mutex
	total int
	err   error
	calls []infoCall
}

func (f *commentsFake) fetch(ctx context.Context, id string, comments int) (yt.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, infoCall{id, comments})
	if f.err != nil {
		return yt.Info{}, f.err
	}
	return commentInfo(min(f.total, comments)), nil
}

func (f *commentsFake) asked() []infoCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]infoCall(nil), f.calls...)
}

// commentsModel plays song a with the first fetch of its comments done: the
// video has `total` comments and the first fetch got up to ten.
func commentsModel(t *testing.T, total, w, h int) (Model, *commentsFake) {
	t.Helper()
	f := &commentsFake{total: total}
	m := statsModel(&statsFakes{})
	m.deps.Info = f.fetch
	m.width, m.height = w, h
	m, _ = startStats(t, m, "0", "*a", "b")
	m, _ = update(m, infoMsg{id: "a", info: commentInfo(min(total, firstComments))})
	return m, f
}

func commentsOnScreen(m Model) string {
	var found []string
	for _, l := range strings.Split(ansi.Strip(m.View()), "\n") {
		if i := strings.Index(l, "Comment number "); i >= 0 {
			found = append(found, strings.TrimSpace(l[i+len("Comment number "):i+len("Comment number ")+2]))
		}
	}
	return strings.Join(found, ",")
}

func commentsHeader(m Model) string {
	for _, l := range strings.Split(ansi.Strip(m.View()), "\n") {
		if i := strings.Index(l, "Top comments"); i >= 0 {
			return strings.TrimSpace(strings.TrimRight(l[i:], "│ "))
		}
	}
	return ""
}

// shownCount is how many comments the screen shows.
func shownCount(m Model) int {
	got := commentsOnScreen(m)
	if got == "" {
		return 0
	}
	return len(strings.Split(got, ","))
}

func TestCommentsFillTheSpaceBelowEachOther(t *testing.T) {
	m, _ := commentsModel(t, 40, 140, 50)
	if got := commentsOnScreen(m); got != "01,02,03,04,05,06,07,08,09,10" {
		t.Errorf("comments on screen = %q, want all ten that were fetched", got)
	}
	h := commentsHeader(m)
	for _, want := range []string{"1–10 of 10+", ":comments"} {
		if !strings.Contains(h, want) {
			t.Errorf("header = %q, want it to contain %q (there may be more on YouTube)", h, want)
		}
	}
	few, _ := commentsModel(t, 2, 140, 50) // all there is
	if h := commentsHeader(few); !strings.Contains(h, "1–2 of 2") || strings.Contains(h, "+") || strings.Contains(h, ":comments") {
		t.Errorf("a video with two comments: header %q, want 1–2 of 2 and no hint to load more", h)
	}
}

func TestCommentsCommandPagesWhenTheyDoNotAllFit(t *testing.T) {
	m, f := commentsModel(t, 100, 140, 30)
	k := shownCount(m)
	if k < 2 || k >= firstComments {
		t.Fatalf("%d comments fit at this height: the test needs a short panel", k)
	}
	m = doCmd(m, "comments")
	if got, want := commentsOnScreen(m), fmt.Sprintf("%02d", k+1); !strings.HasPrefix(got, want) {
		t.Errorf("after :comments the screen shows %q, want it to go on with %s", got, want)
	}
	if len(f.asked()) != 0 {
		t.Errorf("paging through what is there must not fetch: %v", f.asked())
	}
}

// doCmd types a command and feeds back every message the commands it started
// produce (such as a fetch of more comments).
func doCmd(m Model, text string) Model {
	next, cmd := typeCommand(m, text)
	m = next
	for _, msg := range collect(cmd) {
		m, _ = update(m, msg)
	}
	return m
}

func TestCommentsCommandAddsMoreBelowTheOnesThatAreThere(t *testing.T) {
	m, f := commentsModel(t, 100, 140, 50)
	next, cmd := typeCommand(m, "comments") // all ten are in view: load more
	if !strings.Contains(next.status, "Loading more comments") {
		t.Errorf("status = %q, want it to say it is loading", next.status)
	}
	if again, c2 := typeCommand(next, "comments"); c2 != nil && len(collect(c2)) != 0 {
		t.Errorf("a second :comments while loading must not start another load (status %q)", again.status)
	}
	for _, msg := range collect(cmd) {
		next, _ = update(next, msg)
	}
	if got := f.asked(); len(got) != 1 || got[0] != (infoCall{"a", 30}) {
		t.Errorf("fetches = %v, want one for 30 comments of a", got)
	}
	if got := commentsOnScreen(next); !strings.HasPrefix(got, "01,02,03,04,05,06,07,08,09,10,11,12") {
		t.Errorf("after loading the screen shows %q, want the old ones with the new ones below", got)
	}
	if h := commentsHeader(next); !strings.Contains(h, "of 30+") {
		t.Errorf("header = %q", h)
	}
}

func TestLoadedCommentsOutOfViewAreScrolledTo(t *testing.T) {
	m, f := commentsModel(t, 100, 140, 30) // room for only a few
	for i := 0; i < 20 && len(f.asked()) == 0; i++ {
		m = doCmd(m, "comments")
	}
	if len(f.asked()) == 0 {
		t.Fatal("never fetched")
	}
	if got := commentsOnScreen(m); !strings.Contains(got, "11") {
		t.Errorf("after loading the screen shows %q, want the first new comment, 11, in view", got)
	}
}

func TestCommentsWrapAroundWhenThereAreNoMore(t *testing.T) {
	m, f := commentsModel(t, 7, 140, 50) // the video only has seven, all in view
	m = doCmd(m, "comments")
	if got := commentsOnScreen(m); got != "01,02,03,04,05,06,07" || !strings.Contains(m.status, "last") {
		t.Errorf("at the end: screen %q status %q, want it back at the first comments", got, m.status)
	}
	if len(f.asked()) != 0 {
		t.Errorf("a video with seven comments must not be asked for more: %v", f.asked())
	}
}

func TestLoadingMoreCommentsStopsAtAHundred(t *testing.T) {
	m, f := commentsModel(t, 1000, 140, 50)
	for i := 0; i < 150; i++ {
		m = doCmd(m, "comments")
		if len(f.asked()) >= 6 {
			break
		}
	}
	var asked []int
	for _, c := range f.asked() {
		asked = append(asked, c.comments)
	}
	if fmt.Sprint(asked) != "[30 50 70 90 100]" {
		t.Errorf("fetches asked for %v comments, want 30, 50, 70, 90 and then 100 at most", asked)
	}
}

func TestAFailedLoadOfMoreCommentsKeepsWhatIsThere(t *testing.T) {
	m, f := commentsModel(t, 100, 140, 50)
	f.err = errors.New("network down")
	m = doCmd(m, "comments")
	if !m.statusIsErr || !strings.Contains(m.status, "network down") {
		t.Errorf("status = %q (error %v), want the failure said", m.status, m.statusIsErr)
	}
	if got := commentsOnScreen(m); got != "01,02,03,04,05,06,07,08,09,10" {
		t.Errorf("after the failure the screen shows %q, want the comments still there", got)
	}
	f.err = nil
	m = doCmd(m, "comments") // it can be tried again
	if got := commentsOnScreen(m); !strings.Contains(got, ",11,") {
		t.Errorf("after trying again the screen shows %q, want the new comments too", got)
	}
}

func TestCommentsCommandNeedsSomethingToShow(t *testing.T) {
	cases := map[string]func() Model{
		"nothing playing": func() Model {
			m, _ := commentsModel(t, 5, 140, 50)
			m, _ = update(m, plEvent(t, "a", "b"))
			m, _ = update(m, propEvent("playlist-pos", "-1"))
			return m
		},
		"not loaded yet": func() Model {
			m := statsModel(&statsFakes{})
			m.deps.Info = (&commentsFake{total: 5}).fetch
			m.width, m.height = 140, 50
			m, _ = startStats(t, m, "0", "*a", "b")
			return m
		},
		"no comments":     func() Model { m, _ := commentsModel(t, 0, 140, 50); return m },
		"a narrow window": func() Model { m, _ := commentsModel(t, 5, 80, 30); return m },
	}
	for name, build := range cases {
		m := build()
		before := m.stats["a"].commentsOff
		m = doCmd(m, "comments")
		if m.status == "" || m.stats["a"].commentsOff != before {
			t.Errorf("%s: status %q offset %d -> %d, want a message and nothing moved", name, m.status, before, m.stats["a"].commentsOff)
		}
	}
}

// The page is what fits: with room for two comments the next page starts at the
// third, so none is skipped.
func TestCommentsPageByWhatIsVisible(t *testing.T) {
	for h := 20; h <= 60; h++ {
		m, _ := commentsModel(t, 40, 140, h)
		if got := commentsOnScreen(m); got != "01,02" {
			continue
		}
		m = doCmd(m, "comments")
		if got := commentsOnScreen(m); got != "03,04" {
			t.Errorf("height %d: after :comments the screen shows %q, want 03,04 (the page was two)", h, got)
		}
		return
	}
	t.Skip("no terminal height shows exactly two comments in this layout")
}

func TestTheInfoFetchAsksForTenComments(t *testing.T) {
	f := &commentsFake{total: 50}
	m := statsModel(&statsFakes{})
	m.deps.Info = f.fetch
	_, cmd := startStats(t, m, "0", "*a", "b")
	for _, msg := range collect(cmd) {
		_ = msg
	}
	if got := f.asked(); len(got) != 1 || got[0] != (infoCall{"a", firstComments}) {
		t.Errorf("fetches = %v, want one for %d comments", got, firstComments)
	}
}
