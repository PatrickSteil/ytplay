package player

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PatrickSteil/ytplay/internal/cast"
	"github.com/PatrickSteil/ytplay/internal/mpv"
)

// fakeRemote is a cast connection that records what it is told.
type fakeRemote struct {
	mu       sync.Mutex
	calls    []string
	events   chan cast.Event
	pos, dur float64
	state    string
	loadErr  error
	loaded   chan string // every url loaded, in order
}

func newFakeRemote() *fakeRemote {
	return &fakeRemote{events: make(chan cast.Event, 16), state: "IDLE", loaded: make(chan string, 16)}
}

func (r *fakeRemote) rec(s string) error {
	r.mu.Lock()
	r.calls = append(r.calls, s)
	r.mu.Unlock()
	return nil
}
func (r *fakeRemote) Launch(ctx context.Context) error { return r.rec("launch") }
func (r *fakeRemote) Load(ctx context.Context, url, ct, title, artist string) error {
	r.rec("load " + url + " " + ct)
	if r.loadErr != nil {
		return r.loadErr
	}
	r.mu.Lock()
	r.state, r.pos, r.dur = "PLAYING", 0, 200
	r.mu.Unlock()
	r.loaded <- url
	return nil
}
func (r *fakeRemote) Pause(ctx context.Context) error { return r.rec("pause") }
func (r *fakeRemote) Play(ctx context.Context) error  { return r.rec("play") }
func (r *fakeRemote) Seek(ctx context.Context, s float64) error {
	return r.rec(fmt.Sprintf("seek %v", s))
}
func (r *fakeRemote) SetVolume(ctx context.Context, l float64) error {
	return r.rec(fmt.Sprintf("volume %v", l))
}
func (r *fakeRemote) Stop(ctx context.Context) error { return r.rec("stop") }
func (r *fakeRemote) Position() (float64, float64, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pos, r.dur, r.state
}
func (r *fakeRemote) Events() <-chan cast.Event { return r.events }
func (r *fakeRemote) Close()                    { r.rec("close"); close(r.events) }
func (r *fakeRemote) took() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.calls
	r.calls = nil
	return c
}

func resolveTo(m map[string]string) Resolve {
	return func(ctx context.Context, url string) (string, string, error) {
		if s, ok := m[url]; ok {
			return s, "audio/mp4", nil
		}
		return "", "", errors.New("cannot resolve " + url)
	}
}

func newCastDeck(r *fakeRemote, resolve Resolve) *CastDeck {
	return NewCastDeck("Wohnzimmer", r, resolve)
}

func loadfile(t *testing.T, d Deck, url string) int64 {
	t.Helper()
	raw, err := d.Command(bg, "loadfile", url, "replace")
	if err != nil {
		t.Fatal(err)
	}
	var reply struct {
		ID int64 `json:"playlist_entry_id"`
	}
	if json.Unmarshal(raw, &reply) != nil || reply.ID == 0 {
		t.Fatalf("reply %s", raw)
	}
	return reply.ID
}

func waitLoaded(t *testing.T, r *fakeRemote) string {
	t.Helper()
	select {
	case u := <-r.loaded:
		return u
	case <-time.After(3 * time.Second):
		t.Fatal("nothing was loaded")
	}
	return ""
}

func event(t *testing.T, d Deck) mpv.Event {
	t.Helper()
	select {
	case ev := <-d.Events():
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("no event")
	}
	return mpv.Event{}
}

func TestLoadfileResolvesAndLoadsWithoutBlocking(t *testing.T) {
	r := newFakeRemote()
	release := make(chan struct{})
	d := newCastDeck(r, func(ctx context.Context, url string) (string, string, error) {
		<-release
		return "http://stream/" + url, "audio/mp4", nil
	})
	defer d.Close()
	id := loadfile(t, d, "song") // returns while the address is still being worked out
	if id == 0 || len(r.took()) != 0 {
		t.Fatal("loadfile waited for the resolver")
	}
	close(release)
	if u := waitLoaded(t, r); u != "http://stream/song" {
		t.Errorf("loaded %q", u)
	}
	if calls := r.took(); len(calls) != 2 || calls[0] != "launch" || !strings.HasPrefix(calls[1], "load http://stream/song audio/mp4") {
		t.Errorf("calls %v, want launch then load", calls)
	}
}

func TestTheMediaPlayerIsLaunchedOncePerSession(t *testing.T) {
	r := newFakeRemote()
	d := newCastDeck(r, resolveTo(map[string]string{"a": "http://s/a", "b": "http://s/b"}))
	defer d.Close()
	loadfile(t, d, "a")
	waitLoaded(t, r)
	loadfile(t, d, "b")
	waitLoaded(t, r)
	n := 0
	for _, c := range r.took() {
		if c == "launch" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d launches", n)
	}
	_, _ = d.Command(bg, "stop")
	loadfile(t, d, "a")
	waitLoaded(t, r)
	if calls := r.took(); calls[0] != "stop" || calls[1] != "launch" {
		t.Errorf("after stop: %v, want stop, then launch again", calls)
	}
}

func TestAResolveFailureIsAnErrorEndOfThatEntry(t *testing.T) {
	r := newFakeRemote()
	d := newCastDeck(r, resolveTo(nil))
	defer d.Close()
	id := loadfile(t, d, "nowhere")
	ev := event(t, d)
	if ev.Name != "end-file" || ev.Reason != "error" || ev.EntryID != id {
		t.Errorf("event %+v, want an error end of entry %d", ev, id)
	}
}

func TestALoadTheSpeakerRefusesIsAnErrorEndToo(t *testing.T) {
	r := newFakeRemote()
	r.loadErr = errors.New("refused")
	d := newCastDeck(r, resolveTo(map[string]string{"a": "http://s/a"}))
	defer d.Close()
	id := loadfile(t, d, "a")
	if ev := event(t, d); ev.Reason != "error" || ev.EntryID != id {
		t.Errorf("event %+v", ev)
	}
}

func TestANewerLoadReplacesOneStillBeingResolved(t *testing.T) {
	r := newFakeRemote()
	slow := make(chan struct{})
	d := newCastDeck(r, func(ctx context.Context, url string) (string, string, error) {
		if url == "slow" {
			select {
			case <-slow:
			case <-ctx.Done():
				return "", "", ctx.Err()
			}
		}
		return "http://s/" + url, "audio/mp4", nil
	})
	defer d.Close()
	loadfile(t, d, "slow")
	loadfile(t, d, "fast")
	if u := waitLoaded(t, r); u != "http://s/fast" {
		t.Fatalf("loaded %q", u)
	}
	close(slow)
	time.Sleep(50 * time.Millisecond)
	select {
	case u := <-r.loaded:
		t.Errorf("the replaced song was loaded too: %q", u)
	default:
	}
}

func TestCommandsBecomeCastCommands(t *testing.T) {
	r := newFakeRemote()
	d := newCastDeck(r, resolveTo(map[string]string{"a": "http://s/a"}))
	defer d.Close()
	loadfile(t, d, "a")
	waitLoaded(t, r)
	r.took()
	r.mu.Lock()
	r.pos = 50
	r.mu.Unlock()
	for _, c := range [][]any{
		{"set_property", "pause", true}, {"set_property", "pause", false}, {"cycle", "pause"},
		{"set_property", "volume", 40.0}, {"seek", 30.0, "absolute"}, {"seek", 10.0, "relative"}, {"seek", -20.0, "relative"},
		{"seek", 25, "absolute-percent"},
		{"set_property", "speed", 1.03}, {"observe_property", 1, "x"},
	} {
		if _, err := d.Command(bg, c...); err != nil {
			t.Fatalf("%v: %v", c, err)
		}
	}
	want := []string{"pause", "play", "pause", "volume 0.4", "seek 30", "seek 60", "seek 30", "seek 50"}
	// "cycle pause" after a play is a pause: the deck remembers what it last told the speaker
	if got := r.took(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("calls %v, want %v", got, want)
	}
}

func TestASeekBeforeTheSongIsOnTheSpeakerWaitsForIt(t *testing.T) {
	r := newFakeRemote()
	release := make(chan struct{})
	d := newCastDeck(r, func(ctx context.Context, url string) (string, string, error) {
		<-release
		return "http://s/a", "audio/mp4", nil
	})
	defer d.Close()
	loadfile(t, d, "a")
	_, _ = d.Command(bg, "seek", 42.0, "absolute")
	close(release)
	waitLoaded(t, r)
	waitCall(t, r, "seek 42")
}

func waitCall(t *testing.T, r *fakeRemote, want string) {
	t.Helper()
	for i := 0; i < 300; i++ {
		r.mu.Lock()
		for _, c := range r.calls {
			if c == want {
				r.mu.Unlock()
				return
			}
		}
		r.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%q was never sent; calls %v", want, r.calls)
}

func TestTheSpeakersPositionIsTheDecksTimePos(t *testing.T) {
	r := newFakeRemote()
	d := newCastDeck(r, resolveTo(map[string]string{"a": "http://s/a"}))
	defer d.Close()
	if _, err := d.GetFloat(bg, "time-pos"); err == nil {
		t.Error("a position before anything is loaded")
	}
	loadfile(t, d, "a")
	waitLoaded(t, r)
	r.mu.Lock()
	r.pos = 12.5
	r.mu.Unlock()
	if v, err := d.GetFloat(bg, "time-pos"); err != nil || v != 12.5 {
		t.Errorf("time-pos %v %v", v, err)
	}
	if v, err := d.GetFloat(bg, "duration"); err != nil || v != 200 {
		t.Errorf("duration %v %v", v, err)
	}
	if _, err := d.GetFloat(bg, "nonsense"); err == nil {
		t.Error("an unknown property")
	}
}

func TestTheEndOfASongAndPauseAreMpvEvents(t *testing.T) {
	r := newFakeRemote()
	d := newCastDeck(r, resolveTo(map[string]string{"a": "http://s/a"}))
	defer d.Close()
	id := loadfile(t, d, "a")
	waitLoaded(t, r)
	r.events <- cast.Event{Kind: cast.MediaEvent, State: "PAUSED"}
	if ev := event(t, d); ev.Name != "property-change" || ev.Property != "pause" || string(ev.Data) != "true" {
		t.Errorf("pause event %+v %s", ev, ev.Data)
	}
	r.events <- cast.Event{Kind: cast.MediaEvent, State: "PAUSED"} // no change, no event
	r.events <- cast.Event{Kind: cast.MediaEvent, State: "IDLE", IdleReason: "INTERRUPTED"}
	r.events <- cast.Event{Kind: cast.MediaEvent, State: "PLAYING"}
	if ev := event(t, d); string(ev.Data) != "false" {
		t.Errorf("resume event %+v %s", ev, ev.Data)
	}
	r.events <- cast.Event{Kind: cast.MediaEvent, State: "IDLE", IdleReason: "FINISHED"}
	if ev := event(t, d); ev.Name != "end-file" || ev.Reason != "eof" || ev.EntryID != id {
		t.Errorf("end event %+v", ev)
	}
	id2 := loadfile(t, d, "a")
	waitLoaded(t, r)
	r.events <- cast.Event{Kind: cast.MediaEvent, State: "IDLE", IdleReason: "ERROR"}
	if ev := event(t, d); ev.Reason != "error" || ev.EntryID != id2 {
		t.Errorf("error event %+v, want an error end of entry %d", ev, id2)
	}
}

func TestASongEndsOnlyOnce(t *testing.T) {
	r := newFakeRemote()
	d := newCastDeck(r, resolveTo(map[string]string{"a": "http://s/a"}))
	defer d.Close()
	loadfile(t, d, "a")
	waitLoaded(t, r)
	r.events <- cast.Event{Kind: cast.MediaEvent, State: "IDLE", IdleReason: "FINISHED"}
	r.events <- cast.Event{Kind: cast.MediaEvent, State: "IDLE", IdleReason: "FINISHED"} // reported twice
	r.events <- cast.Event{Kind: cast.MediaEvent, State: "PAUSED"}                       // what to wait for
	var got []string
	for len(got) < 2 {
		ev := event(t, d)
		got = append(got, ev.Name)
	}
	if got[0] != "end-file" || got[1] != "property-change" {
		t.Errorf("events %v, want the end once and then the pause", got)
	}
}

func TestTheConnectionClosingEndsTheDeck(t *testing.T) {
	r := newFakeRemote()
	d := newCastDeck(r, resolveTo(nil))
	r.events <- cast.Event{Kind: cast.ClosedEvent}
	select {
	case <-d.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("not done")
	}
	if err := d.Close(); err != nil {
		t.Error(err)
	}
}

func TestAnEndReportWhileASongIsStillLoadingIsNotItsEnd(t *testing.T) {
	r := newFakeRemote()
	release := make(chan struct{})
	d := newCastDeck(r, func(ctx context.Context, url string) (string, string, error) {
		<-release
		return "http://s/a", "audio/mp4", nil
	})
	defer d.Close()
	loadfile(t, d, "a")
	r.events <- cast.Event{Kind: cast.MediaEvent, State: "IDLE", IdleReason: "FINISHED"} // the song before
	select {
	case ev := <-d.Events():
		t.Errorf("event %+v for a song that is not on the speaker yet", ev)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	waitLoaded(t, r)
}

func TestStopForgetsASongThatWasStillLoading(t *testing.T) {
	r := newFakeRemote()
	release := make(chan struct{})
	d := newCastDeck(r, func(ctx context.Context, url string) (string, string, error) {
		<-release // ignores being cancelled, like a slow tool
		return "http://s/a", "audio/mp4", nil
	})
	defer d.Close()
	loadfile(t, d, "a")
	_, _ = d.Command(bg, "stop")
	close(release)
	select {
	case u := <-r.loaded:
		t.Errorf("%q was loaded after stop", u)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestASongThatIsReplacedWhileLoadingIsCancelled(t *testing.T) {
	r := newFakeRemote()
	cancelled := make(chan struct{})
	d := newCastDeck(r, func(ctx context.Context, url string) (string, string, error) {
		if url == "slow" {
			<-ctx.Done()
			close(cancelled)
			return "", "", ctx.Err()
		}
		return "http://s/" + url, "audio/mp4", nil
	})
	defer d.Close()
	loadfile(t, d, "slow")
	loadfile(t, d, "fast")
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Error("the replaced song's work was not cancelled")
	}
}

func TestClosingTheDeckStopsThePlaybackOnTheSpeaker(t *testing.T) {
	r := newFakeRemote()
	d := newCastDeck(r, resolveTo(map[string]string{"a": "http://s/a"}))
	loadfile(t, d, "a")
	waitLoaded(t, r)
	r.took()
	_ = d.Close()
	got := r.took()
	if len(got) != 2 || got[0] != "stop" || got[1] != "close" {
		t.Errorf("calls %v, want stop, then close", got)
	}
	// a deck that never played has nothing to stop
	r2 := newFakeRemote()
	d2 := newCastDeck(r2, resolveTo(nil))
	_ = d2.Close()
	if got := r2.took(); len(got) != 1 || got[0] != "close" {
		t.Errorf("calls %v", got)
	}
}
