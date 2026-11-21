package ui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/PatrickSteil/ytplay/internal/mpv"
)

type castFakes struct {
	names   []string
	listErr error
	castErr error
	cast    []string
	off     int
	offErr  error
}

func (f *castFakes) deps(m Model) Model {
	m.deps.Devices = func(ctx context.Context) ([]string, error) { return f.names, f.listErr }
	m.deps.Cast = func(ctx context.Context, name string) (string, error) {
		f.cast = append(f.cast, name)
		if f.castErr != nil {
			return "", f.castErr
		}
		return "Wohnzimmer", nil
	}
	m.deps.CastOff = func(ctx context.Context) error { f.off++; return f.offErr }
	return m
}

func castEvent(name string) MpvEventMsg {
	return MpvEventMsg{mpv.Event{Name: "cast", Reason: name, Data: json.RawMessage(`null`)}}
}

func TestCastPickerSelectsASpeakerAndReturnsToTheQueue(t *testing.T) {
	f := &castFakes{names: []string{"Kitchen speaker", "Wohnzimmer"}}
	m, _ := send(f.deps(at(queueModel(t, 3), 140, 30)), "tab")
	m, discover := send(m, "C")
	if m.mode != modeCast || !strings.Contains(m.View(), "Looking for speakers") {
		t.Fatalf("C did not open a speaker picker in discovery mode")
	}
	m = run(m, discover)
	for _, want := range f.names {
		if !strings.Contains(m.View(), want) {
			t.Errorf("picker lacks %q", want)
		}
	}
	if len(f.cast) != 0 {
		t.Fatal("casting started before selection")
	}
	m, connect := send(m, "down", "enter")
	m = run(m, connect)
	if m.mode != modeQueue || len(f.cast) != 1 || f.cast[0] != "Wohnzimmer" {
		t.Errorf("mode %v, cast requests %v, want queue mode and Wohnzimmer", m.mode, f.cast)
	}
}

func TestCastPickerWithoutSpeakers(t *testing.T) {
	m := doCmd((&castFakes{}).deps(at(queueModel(t, 3), 140, 30)), "cast")
	if m.mode != modeCast || !strings.Contains(m.View(), "No speakers found") {
		t.Fatal("empty discovery did not show the empty picker")
	}
}

func TestCastPickerKeepsPlaybackOnTheCurrentSpeaker(t *testing.T) {
	f := &castFakes{names: []string{"Wohnzimmer"}}
	m := f.deps(at(queueModel(t, 3), 140, 30))
	m.castName = "Wohnzimmer"
	m = doCmd(m, "cast")
	m, cmd := send(m, "enter")
	if m.mode != modeNormal || cmd != nil {
		t.Error("selecting the current speaker should close the picker without reconnecting")
	}
}

func TestCastPickerCanReturnToLocalPlayback(t *testing.T) {
	f := &castFakes{listErr: errors.New("no multicast")}
	m := f.deps(at(queueModel(t, 3), 140, 30))
	m.castName = "Wohnzimmer"
	m = doCmd(m, "cast")
	m, off := send(m, "enter")
	m = run(m, off)
	if m.mode != modeNormal || f.off != 1 || len(f.cast) != 0 {
		t.Errorf("mode %v, local playback calls %d, cast requests %v", m.mode, f.off, f.cast)
	}
}

func TestCastWithANameStartsCasting(t *testing.T) {
	f := &castFakes{}
	m := doCmd(f.deps(at(queueModel(t, 3), 140, 30)), "cast wohn")
	if len(f.cast) != 1 || f.cast[0] != "wohn" {
		t.Fatalf("asked to cast to %v", f.cast)
	}
	if !strings.Contains(m.status, "Wohnzimmer") || m.statusIsErr {
		t.Errorf("status %q (error %v)", m.status, m.statusIsErr)
	}
}

func TestCastOffGoesBackToPlayingHere(t *testing.T) {
	f := &castFakes{}
	m := doCmd(f.deps(at(queueModel(t, 3), 140, 30)), "cast off")
	if f.off != 1 || len(f.cast) != 0 {
		t.Errorf("off %d cast %v", f.off, f.cast)
	}
	if !strings.Contains(strings.ToLower(m.status), "here") {
		t.Errorf("status %q", m.status)
	}
}

func TestCastFailuresAreSaid(t *testing.T) {
	m := doCmd((&castFakes{castErr: errors.New("no speaker called \"x\"")}).deps(at(queueModel(t, 3), 140, 30)), "cast x")
	if !m.statusIsErr || !strings.Contains(m.status, "no speaker called") {
		t.Errorf("status %q (error %v)", m.status, m.statusIsErr)
	}
	m = doCmd((&castFakes{listErr: errors.New("no multicast")}).deps(at(queueModel(t, 3), 140, 30)), "cast")
	if m.mode != modeCast || !strings.Contains(m.View(), "no multicast") {
		t.Error("discovery failure did not appear in the picker")
	}
	m = doCmd((&castFakes{offErr: errors.New("not casting")}).deps(at(queueModel(t, 3), 140, 30)), "cast off")
	if !m.statusIsErr || !strings.Contains(m.status, "not casting") {
		t.Errorf("status %q", m.status)
	}
	bare := doCmd(at(queueModel(t, 3), 140, 30), "cast")
	if !bare.statusIsErr || !strings.Contains(bare.status, "not available") {
		t.Errorf("without casting support: %q", bare.status)
	}
}

func TestOnlyOneCastLookupAtATime(t *testing.T) {
	f := &castFakes{names: []string{"A"}}
	m := f.deps(at(queueModel(t, 3), 140, 30))
	m, cmd := typeCommand(m, "cast")
	if !m.castBusy {
		t.Fatal("not busy while looking")
	}
	m, _ = send(m, "esc")
	again, cmd2 := send(m, "C")
	if cmd2 != nil && len(collect(cmd2)) != 0 || !strings.Contains(again.status, "Still") {
		t.Errorf("a second :cast while looking: %q", again.status)
	}
	m = run(again, cmd)
	if m.castBusy || m.mode != modeNormal {
		t.Error("late discovery changed the mode or left casting busy")
	}
}

func TestTheNowPlayingPanelNamesTheSpeakerWhileCasting(t *testing.T) {
	m := at(queueModel(t, 3), 140, 30)
	m, _ = update(m, positionMsg{elapsed: 10, total: 200})
	if strings.Contains(m.View(), "Wohnzimmer") {
		t.Fatal("named before casting")
	}
	m, _ = update(m, castEvent("Wohnzimmer"))
	if !strings.Contains(m.View(), "Wohnzimmer") {
		t.Errorf("the speaker is not named:\n%s", m.View())
	}
	narrow := at(m, 90, 30)
	if !strings.Contains(narrow.View(), "Wohnzimmer") {
		t.Error("the speaker is not named in the stacked layout")
	}
	m, _ = update(m, castEvent(""))
	if strings.Contains(m.View(), "Wohnzimmer") {
		t.Error("still named after casting stopped")
	}
}

func TestLosingTheSpeakerIsSaid(t *testing.T) {
	m := at(queueModel(t, 3), 140, 30)
	m, _ = update(m, castEvent("Wohnzimmer"))
	m, _ = update(m, castEvent("lost"))
	if m.castName != "" || !m.statusIsErr || !strings.Contains(m.status, "speaker") {
		t.Errorf("cast %q status %q (error %v)", m.castName, m.status, m.statusIsErr)
	}
}
