package player

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PatrickSteil/ytplay/internal/mpv"
)

// fakeDeck is a Deck that records every command and lets a test inject
// events. loadfile is answered with ascending playlist_entry_ids, like mpv.
type fakeDeck struct {
	mu       sync.Mutex
	calls    []string
	props    map[string]float64
	nextID   int64
	failLoad map[string]bool // urls whose loadfile mpv rejects
	failCmd  map[string]bool // commands (first word) that fail
	noIDs    bool            // answer loadfile without an entry id, like an older mpv
	events   chan mpv.Event
	done     chan struct{}
	closed   bool
}

func newFakeDeck() *fakeDeck {
	return &fakeDeck{
		props: map[string]float64{}, failLoad: map[string]bool{}, failCmd: map[string]bool{},
		events: make(chan mpv.Event, 64), done: make(chan struct{}),
	}
}

func (d *fakeDeck) Command(ctx context.Context, args ...any) (json.RawMessage, error) {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = fmt.Sprint(a)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, strings.Join(parts, " "))
	if d.closed {
		return nil, errors.New("mpv exited")
	}
	if d.failCmd[parts[0]] {
		return nil, errors.New("mpv: command failed")
	}
	if parts[0] == "loadfile" {
		if d.failLoad[parts[1]] {
			return nil, errors.New("mpv: loading failed")
		}
		if d.noIDs {
			return json.RawMessage(`{}`), nil
		}
		d.nextID++
		return json.RawMessage(fmt.Sprintf(`{"playlist_entry_id":%d}`, d.nextID)), nil
	}
	return nil, nil
}

func (d *fakeDeck) GetFloat(ctx context.Context, prop string) (float64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	v, ok := d.props[prop]
	if !ok {
		return 0, errors.New("mpv: property unavailable")
	}
	return v, nil
}

func (d *fakeDeck) Events() <-chan mpv.Event { return d.events }
func (d *fakeDeck) Done() <-chan struct{}    { return d.done }
func (d *fakeDeck) StderrTail() string       { return "fake stderr" }
func (d *fakeDeck) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.closed {
		d.closed = true
		close(d.done)
	}
	return nil
}

// take returns the commands recorded so far and forgets them.
func (d *fakeDeck) take() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	c := d.calls
	d.calls = nil
	return c
}

// exit simulates the mpv process dying.
func (d *fakeDeck) exit() { _ = d.Close() }

func (d *fakeDeck) endFile(reason string, id int64) {
	d.events <- mpv.Event{Name: "end-file", Reason: reason, EntryID: id}
}

// newTestPlayer returns a Player on one fake deck.
func newTestPlayer(t *testing.T) (*Player, *fakeDeck) {
	t.Helper()
	a := newFakeDeck()
	p, err := New(context.Background(), func(ctx context.Context, name string) (Deck, error) {
		if name != "a" {
			return nil, errors.New("no deck " + name)
		}
		return a, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p, a
}

// describe renders an event compactly: "playlist=[a*,b]" (* marks the current
// entry), "playlist-pos=1", "playlist-pos=null", "idle-active=false",
// "pause=true", "end-file:error".
func describe(ev mpv.Event) string {
	switch ev.Name {
	case "end-file":
		return "end-file:" + ev.Reason
	case "property-change":
		if ev.Property == "playlist" {
			var pl []struct {
				Filename string `json:"filename"`
				Current  bool   `json:"current"`
			}
			_ = json.Unmarshal(ev.Data, &pl)
			parts := make([]string, len(pl))
			for i, e := range pl {
				parts[i] = e.Filename
				if e.Current {
					parts[i] += "*"
				}
			}
			return "playlist=[" + strings.Join(parts, ",") + "]"
		}
		return ev.Property + "=" + string(ev.Data)
	}
	return ev.Name
}

// state is the three events every change of the queue emits.
func state(list string, pos int) []string {
	p, idle := "null", "true"
	if pos >= 0 {
		p, idle = fmt.Sprint(pos), "false"
	}
	return []string{"playlist=[" + list + "]", "playlist-pos=" + p, "idle-active=" + idle}
}

func cat(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// drain returns the events emitted so far (synchronous actions emit before
// they return).
func drain(p *Player) []string {
	var out []string
	for {
		select {
		case ev := <-p.Events():
			out = append(out, describe(ev))
		default:
			return out
		}
	}
}

// waitEvents waits for n events caused by something asynchronous.
func waitEvents(t *testing.T, p *Player, n int) []string {
	t.Helper()
	var out []string
	timeout := time.After(2 * time.Second)
	for len(out) < n {
		select {
		case ev := <-p.Events():
			out = append(out, describe(ev))
		case <-timeout:
			t.Fatalf("got %d of %d events: %q", len(out), n, out)
		}
	}
	return out
}

func quiet(t *testing.T, p *Player) {
	t.Helper()
	time.Sleep(80 * time.Millisecond)
	if got := drain(p); len(got) != 0 {
		t.Errorf("unexpected events: %q", got)
	}
}

func eq(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got  %q\n want %q", what, got, want)
	}
}

var bg = context.Background()

func TestQueueWhenIdleStartsPlaying(t *testing.T) {
	p, a := newTestPlayer(t)
	if err := p.Queue(bg, "a"); err != nil {
		t.Fatal(err)
	}
	eq(t, "commands", a.take(), []string{"loadfile a replace"})
	eq(t, "events", drain(p), state("a*", 0))
}

func TestPlaylistEventHasTheShapeMpvSends(t *testing.T) {
	p, _ := newTestPlayer(t)
	_ = p.QueueMany(bg, []string{"a", "b"})
	ev := <-p.Events()
	if ev.Name != "property-change" || ev.Property != "playlist" {
		t.Fatalf("first event = %+v", ev)
	}
	var pl []struct {
		Filename string `json:"filename"`
		Current  bool   `json:"current"`
		Playing  bool   `json:"playing"`
	}
	if err := json.Unmarshal(ev.Data, &pl); err != nil || len(pl) != 2 {
		t.Fatalf("data %s: %v", ev.Data, err)
	}
	if pl[0].Filename != "a" || !pl[0].Current || !pl[0].Playing || pl[1].Current || pl[1].Playing {
		t.Errorf("entries = %+v", pl)
	}
}

func TestQueueWhilePlayingOnlyAppends(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.Queue(bg, "a")
	a.take()
	drain(p)
	if err := p.Queue(bg, "b"); err != nil {
		t.Fatal(err)
	}
	eq(t, "commands", a.take(), nil)
	eq(t, "events", drain(p), state("a*,b", 0))
}

func TestQueueManyStartsOnlyTheFirstWhenIdle(t *testing.T) {
	p, a := newTestPlayer(t)
	if err := p.QueueMany(bg, []string{"a", "b", "c"}); err != nil {
		t.Fatal(err)
	}
	eq(t, "commands", a.take(), []string{"loadfile a replace"})
	eq(t, "events", drain(p), state("a*,b,c", 0))
}

func TestQueueManyAppendsWhenPlaying(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.Queue(bg, "a")
	a.take()
	drain(p)
	_ = p.QueueMany(bg, []string{"b", "c"})
	eq(t, "commands", a.take(), nil)
	eq(t, "events", drain(p), state("a*,b,c", 0))
}

func TestPlayNowWhenIdleBehavesLikeQueue(t *testing.T) {
	p, a := newTestPlayer(t)
	if err := p.PlayNow(bg, "a"); err != nil {
		t.Fatal(err)
	}
	eq(t, "commands", a.take(), []string{"loadfile a replace"})
	eq(t, "events", drain(p), state("a*", 0))
}

func TestPlayNowInsertsAfterCurrentAndPlaysIt(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.QueueMany(bg, []string{"a", "b", "c"})
	_ = p.Skip(bg) // b is playing
	a.take()
	drain(p)
	if err := p.PlayNow(bg, "n"); err != nil {
		t.Fatal(err)
	}
	eq(t, "commands", a.take(), []string{"loadfile n replace"})
	eq(t, "events", drain(p), state("a,b,n*,c", 2))
}

func TestSkipPlaysTheNextAndGoesIdleOnTheLast(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.QueueMany(bg, []string{"a", "b"})
	a.take()
	drain(p)
	if err := p.Skip(bg); err != nil {
		t.Fatal(err)
	}
	eq(t, "commands", a.take(), []string{"loadfile b replace"})
	eq(t, "events", drain(p), state("a,b*", 1))

	if err := p.Skip(bg); err != nil {
		t.Fatal(err)
	}
	eq(t, "commands at the end", a.take(), []string{"stop"})
	eq(t, "events at the end", drain(p), state("a,b", -1))
}

func TestSkipWhenIdleDoesNothing(t *testing.T) {
	p, a := newTestPlayer(t)
	if err := p.Skip(bg); err != nil {
		t.Fatal(err)
	}
	eq(t, "commands", a.take(), nil)
	quiet(t, p)
}

func TestEofAdvancesToTheNextEntry(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.QueueMany(bg, []string{"a", "b"})
	a.take()
	drain(p)
	a.endFile("eof", 1)
	eq(t, "events", waitEvents(t, p, 3), state("a,b*", 1))
	eq(t, "commands", a.take(), []string{"loadfile b replace"})

	a.endFile("eof", 2)
	eq(t, "events at the end", waitEvents(t, p, 3), state("a,b", -1))
}

func TestStaleEndFileIsIgnored(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.QueueMany(bg, []string{"a", "b", "c"})
	_ = p.Skip(bg) // b (entry id 2) is playing; a's id was 1
	a.take()
	drain(p)

	a.endFile("eof", 1)  // the end of a, arriving after the skip
	a.endFile("stop", 2) // mpv reporting our own loadfile/stop
	a.endFile("quit", 2)
	quiet(t, p)
	eq(t, "commands", a.take(), nil)

	a.endFile("eof", 2) // the real end of b
	eq(t, "events", waitEvents(t, p, 3), state("a,b,c*", 2))
}

func TestLoadErrorReportsThenAdvances(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.QueueMany(bg, []string{"a", "b"})
	a.take()
	drain(p)
	a.endFile("error", 1)
	// The error comes first, while the UI still points at the song that failed.
	eq(t, "events", waitEvents(t, p, 4), cat([]string{"end-file:error"}, state("a,b*", 1)))
	eq(t, "commands", a.take(), []string{"loadfile b replace"})
}

func TestEveryEntryFailingEndsIdle(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.QueueMany(bg, []string{"a", "b"})
	drain(p)
	a.endFile("error", 1)
	waitEvents(t, p, 4)
	a.endFile("error", 2)
	eq(t, "events", waitEvents(t, p, 4), cat([]string{"end-file:error"}, state("a,b", -1)))
	quiet(t, p)
}

func TestLoadCommandFailureSkipsTheEntry(t *testing.T) {
	p, a := newTestPlayer(t)
	a.failLoad["bad"] = true
	if err := p.QueueMany(bg, []string{"bad", "good"}); err != nil {
		t.Fatal(err)
	}
	eq(t, "commands", a.take(), []string{"loadfile bad replace", "loadfile good replace"})
	eq(t, "events", drain(p), cat(state("bad*,good", 0), []string{"end-file:error"}, state("bad,good*", 1)))
}

func TestLoadCommandFailureOnTheLastEntryEndsIdle(t *testing.T) {
	p, a := newTestPlayer(t)
	a.failLoad["bad"] = true
	_ = p.Queue(bg, "bad")
	eq(t, "events", drain(p), cat(state("bad*", 0), []string{"end-file:error"}, state("bad", -1)))
}

func TestQueueAfterTheQueueFinishedPlaysTheNewSong(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.Queue(bg, "a")
	a.endFile("eof", 1)
	waitEvents(t, p, 6) // the start and the end
	a.take()
	if err := p.Queue(bg, "b"); err != nil {
		t.Fatal(err)
	}
	eq(t, "commands", a.take(), []string{"loadfile b replace"})
	eq(t, "events", drain(p), state("a,b*", 1))
}

func TestRemoveUpcoming(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.QueueMany(bg, []string{"a", "b", "c"})
	a.take()
	drain(p)
	if err := p.Remove(bg, 2); err != nil {
		t.Fatal(err)
	}
	eq(t, "commands", a.take(), nil)
	eq(t, "events", drain(p), state("a*,b", 0))
}

func TestRemoveCurrentPlaysTheFollowingEntry(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.QueueMany(bg, []string{"a", "b", "c"})
	a.take()
	drain(p)
	if err := p.Remove(bg, 0); err != nil {
		t.Fatal(err)
	}
	eq(t, "commands", a.take(), []string{"loadfile b replace"})
	eq(t, "events", drain(p), state("b*,c", 0))
}

func TestRemoveTheOnlyCurrentGoesIdle(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.Queue(bg, "a")
	a.take()
	drain(p)
	if err := p.Remove(bg, 0); err != nil {
		t.Fatal(err)
	}
	eq(t, "commands", a.take(), []string{"stop"})
	eq(t, "events", drain(p), state("", -1))
}

func TestRemoveOutOfRangeFailsQuietly(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.Queue(bg, "a")
	a.take()
	drain(p)
	if err := p.Remove(bg, 5); err == nil {
		t.Error("expected an error")
	}
	eq(t, "commands", a.take(), nil)
	quiet(t, p)
}

func TestMoveUpAndMoveDown(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.QueueMany(bg, []string{"a", "b", "c"})
	a.take()
	drain(p)
	if err := p.MoveDown(bg, 1); err != nil {
		t.Fatal(err)
	}
	eq(t, "after MoveDown", drain(p), state("a*,c,b", 0))
	if err := p.MoveUp(bg, 2); err != nil {
		t.Fatal(err)
	}
	eq(t, "after MoveUp", drain(p), state("a*,b,c", 0))
	if err := p.MoveUp(bg, 0); err == nil {
		t.Error("moving the first entry up must fail")
	}
	if err := p.MoveDown(bg, 2); err == nil {
		t.Error("moving the last entry down must fail")
	}
	eq(t, "commands", a.take(), nil)
	quiet(t, p)
}

func TestClearQueueKeepsTheCurrentSong(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.QueueMany(bg, []string{"a", "b", "c"})
	_ = p.Skip(bg) // b is playing
	a.take()
	drain(p)
	if err := p.ClearQueue(bg); err != nil {
		t.Fatal(err)
	}
	eq(t, "commands", a.take(), nil)
	eq(t, "events", drain(p), state("b*", 0))
}

func TestStopKeepsTheQueueAndGoesIdle(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.QueueMany(bg, []string{"a", "b"})
	a.take()
	drain(p)
	if err := p.Stop(bg); err != nil {
		t.Fatal(err)
	}
	eq(t, "commands", a.take(), []string{"stop"})
	eq(t, "events", drain(p), state("a,b", -1))
	// A late end-file of the song that was stopped changes nothing.
	a.endFile("eof", 1)
	quiet(t, p)
}

func TestSeekPercent(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.Queue(bg, "a")
	a.take()
	if err := p.SeekPercent(bg, 50); err != nil {
		t.Fatal(err)
	}
	eq(t, "commands", a.take(), []string{"seek 50 absolute-percent"})
}

func TestPauseControls(t *testing.T) {
	p, a := newTestPlayer(t)
	if err := p.TogglePause(bg); err != nil {
		t.Fatal(err)
	}
	eq(t, "toggle when idle", a.take(), nil)

	_ = p.Queue(bg, "a")
	a.take()
	if err := p.TogglePause(bg); err != nil {
		t.Fatal(err)
	}
	if err := p.SetPause(bg, true); err != nil {
		t.Fatal(err)
	}
	eq(t, "commands", a.take(), []string{"cycle pause", "set_property pause true"})
}

func TestPauseEventIsForwarded(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.Queue(bg, "a")
	drain(p)
	a.events <- mpv.Event{Name: "property-change", Property: "pause", Data: json.RawMessage("true")}
	eq(t, "events", waitEvents(t, p, 1), []string{"pause=true"})
}

func TestPositionReadsTheActiveDeck(t *testing.T) {
	p, a := newTestPlayer(t)
	if _, _, err := p.Position(bg); err == nil {
		t.Error("an idle player has no position")
	}
	_ = p.Queue(bg, "a")
	a.props["time-pos"], a.props["duration"] = 12.5, 200
	elapsed, total, err := p.Position(bg)
	if err != nil || elapsed != 12.5 || total != 200 {
		t.Errorf("Position = %v, %v, %v", elapsed, total, err)
	}
}

func TestDoneClosesWhenTheDeckExits(t *testing.T) {
	p, a := newTestPlayer(t)
	select {
	case <-p.Done():
		t.Fatal("Done closed too early")
	default:
	}
	a.exit()
	select {
	case <-p.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done did not close after the deck exited")
	}
	if got := p.StderrTail(); got != "fake stderr" {
		t.Errorf("StderrTail = %q", got)
	}
}

func TestCloseClosesTheDecks(t *testing.T) {
	p, a := newTestPlayer(t)
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.done:
	default:
		t.Error("deck a was not closed")
	}
	if err := p.Close(); err != nil { // idempotent
		t.Error(err)
	}
}

func TestOlderMpvWithoutEntryIDsStillAdvances(t *testing.T) {
	p, a := newTestPlayer(t)
	a.noIDs = true
	_ = p.QueueMany(bg, []string{"a", "b"})
	drain(p)
	a.endFile("eof", 0)
	eq(t, "events", waitEvents(t, p, 3), state("a,b*", 1))
}

func TestReorderUpcomingAppliesTheOrder(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.QueueMany(bg, []string{"a", "b", "c", "d"})
	a.take()
	drain(p)
	if err := p.ReorderUpcoming(bg, []string{"d", "b", "c"}); err != nil {
		t.Fatal(err)
	}
	eq(t, "commands", a.take(), nil)
	eq(t, "events", drain(p), state("a*,d,b,c", 0))
}

func TestReorderUpcomingRejectsADifferentSet(t *testing.T) {
	p, a := newTestPlayer(t)
	_ = p.QueueMany(bg, []string{"a", "b", "c"})
	a.take()
	drain(p)
	if err := p.ReorderUpcoming(bg, []string{"c", "x"}); err == nil {
		t.Error("expected an error")
	}
	quiet(t, p)
}
