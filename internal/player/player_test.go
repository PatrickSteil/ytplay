package player

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// recorder implements Commander, recording commands and answering
// get_property from a map.
type recorder struct {
	calls []string
	props map[string]any
	err   error // returned for every non-get command when set
}

func (r *recorder) Command(ctx context.Context, args ...any) (json.RawMessage, error) {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = fmt.Sprint(a)
	}
	r.calls = append(r.calls, strings.Join(parts, " "))
	if args[0] == "get_property" {
		v, ok := r.props[args[1].(string)]
		if !ok {
			return nil, errors.New("mpv: property unavailable")
		}
		return json.Marshal(v)
	}
	return nil, r.err
}

const u = "https://www.youtube.com/watch?v=abc"

func check(t *testing.T, r *recorder, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(r.calls, want) {
		t.Errorf("commands:\n got  %q\n want %q", r.calls, want)
	}
}

func TestQueueWhenIdleStartsPlayingTheNewEntry(t *testing.T) {
	r := &recorder{props: map[string]any{"idle-active": true, "playlist-count": 4}}
	if err := New(r).Queue(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	check(t, r,
		"loadfile "+u+" append",
		"get_property idle-active",
		"get_property playlist-count",
		"playlist-play-index 3",
	)
}

func TestQueueWhenPlayingOnlyAppends(t *testing.T) {
	r := &recorder{props: map[string]any{"idle-active": false}}
	if err := New(r).Queue(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	check(t, r, "loadfile "+u+" append", "get_property idle-active")
}

func TestPlayNowWhenIdleBehavesLikeQueue(t *testing.T) {
	r := &recorder{props: map[string]any{"idle-active": true, "playlist-count": 1}}
	if err := New(r).PlayNow(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	check(t, r,
		"get_property idle-active",
		"loadfile "+u+" append",
		"get_property idle-active",
		"get_property playlist-count",
		"playlist-play-index 0",
	)
}

func TestPlayNowInMiddleOfQueueInsertsAfterCurrent(t *testing.T) {
	// 4 entries, currently playing index 1; new entry must land at index 2.
	r := &recorder{props: map[string]any{"idle-active": false, "playlist-count": 4, "playlist-pos": 1}}
	if err := New(r).PlayNow(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	check(t, r,
		"get_property idle-active",
		"get_property playlist-count",
		"get_property playlist-pos",
		"loadfile "+u+" append",
		"playlist-move 4 2",
		"playlist-play-index 2",
	)
}

func TestPlayNowOnLastItemNeedsNoMove(t *testing.T) {
	// The appended entry already sits right after the current (last) one.
	r := &recorder{props: map[string]any{"idle-active": false, "playlist-count": 3, "playlist-pos": 2}}
	if err := New(r).PlayNow(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	check(t, r,
		"get_property idle-active",
		"get_property playlist-count",
		"get_property playlist-pos",
		"loadfile "+u+" append",
		"playlist-play-index 3",
	)
}

func TestPlayNowWithEmptyPlaylistBehavesLikeQueue(t *testing.T) {
	r := &recorder{props: map[string]any{"idle-active": false, "playlist-count": 0}}
	if err := New(r).PlayNow(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	check(t, r,
		"get_property idle-active",
		"get_property playlist-count",
		"loadfile "+u+" append",
		"get_property idle-active",
	)
}

func TestSkip(t *testing.T) {
	r := &recorder{}
	if err := New(r).Skip(context.Background()); err != nil {
		t.Fatal(err)
	}
	check(t, r, "playlist-next force")
}

func TestSeekPercent(t *testing.T) {
	r := &recorder{}
	p := New(r)
	for _, n := range []int{0, 50, 100} {
		if err := p.SeekPercent(context.Background(), n); err != nil {
			t.Fatal(err)
		}
	}
	check(t, r,
		"seek 0 absolute-percent",
		"seek 50 absolute-percent",
		"seek 100 absolute-percent",
	)
}

func TestTogglePause(t *testing.T) {
	r := &recorder{}
	if err := New(r).TogglePause(context.Background()); err != nil {
		t.Fatal(err)
	}
	check(t, r, "cycle pause")
}

func TestErrorsAreWrapped(t *testing.T) {
	boom := errors.New("boom")
	r := &recorder{err: boom}
	err := New(r).Skip(context.Background())
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want wrapping boom", err)
	}
}

func TestRemoveEntry(t *testing.T) {
	r := &recorder{}
	if err := New(r).Remove(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
	check(t, r, "playlist-remove 5")
}

func TestMoveUpPutsEntryBeforeItsPredecessor(t *testing.T) {
	r := &recorder{}
	if err := New(r).MoveUp(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
	check(t, r, "playlist-move 5 4")
}

func TestMoveDownPutsEntryAfterItsSuccessor(t *testing.T) {
	// playlist-move places the entry *before* the target index, so moving
	// entry 5 behind entry 6 means target index 7.
	r := &recorder{}
	if err := New(r).MoveDown(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
	check(t, r, "playlist-move 5 7")
}

func TestSetPause(t *testing.T) {
	r := &recorder{}
	p := New(r)
	if err := p.SetPause(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if err := p.SetPause(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	check(t, r, "set_property pause true", "set_property pause false")
}

func TestStop(t *testing.T) {
	r := &recorder{}
	if err := New(r).Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	check(t, r, "stop")
}

func TestQueueManyAppendsAllWhenPlaying(t *testing.T) {
	r := &recorder{props: map[string]any{"idle-active": false}}
	if err := New(r).QueueMany(context.Background(), []string{"u1", "u2", "u3"}); err != nil {
		t.Fatal(err)
	}
	check(t, r,
		"loadfile u1 append",
		"get_property idle-active",
		"loadfile u2 append",
		"loadfile u3 append",
	)
}

func TestQueueManyStartsTheFirstNewEntryWhenIdle(t *testing.T) {
	// mpv went idle (last track just ended): the first new entry must start,
	// and the others follow it.
	r := &recorder{props: map[string]any{"idle-active": true, "playlist-count": 5}}
	if err := New(r).QueueMany(context.Background(), []string{"u1", "u2"}); err != nil {
		t.Fatal(err)
	}
	check(t, r,
		"loadfile u1 append",
		"get_property idle-active",
		"get_property playlist-count",
		"playlist-play-index 4",
		"loadfile u2 append",
	)
}

func TestQueueManyWithNothingDoesNothing(t *testing.T) {
	r := &recorder{}
	if err := New(r).QueueMany(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	check(t, r)
}

func TestClearQueue(t *testing.T) {
	r := &recorder{}
	if err := New(r).ClearQueue(context.Background()); err != nil {
		t.Fatal(err)
	}
	check(t, r, "playlist-clear")
}
