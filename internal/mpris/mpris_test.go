package mpris

import (
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

var playing = State{Active: true, ID: "abc_-123", Title: "Song", Channel: "Chan", Duration: 212.5, HasNext: true}

func meta(t *testing.T, st State) map[string]dbus.Variant {
	t.Helper()
	m, ok := playerProps(st)["Metadata"].Value().(map[string]dbus.Variant)
	if !ok {
		t.Fatalf("Metadata has wrong type: %T", playerProps(st)["Metadata"].Value())
	}
	return m
}

func TestTrackPathIsValidAndUnique(t *testing.T) {
	ids := []string{"abc_-123", "abc-_123", "abc__123", "dQw4w9WgXcQ", "a"}
	seen := map[dbus.ObjectPath]string{}
	for _, id := range ids {
		p := trackPath(id)
		if !p.IsValid() {
			t.Errorf("trackPath(%q) = %q is not a valid object path", id, p)
		}
		if prev, dup := seen[p]; dup {
			t.Errorf("trackPath collision: %q and %q -> %q", prev, id, p)
		}
		seen[p] = id
	}
}

func TestMetadataForPlayingTrack(t *testing.T) {
	m := meta(t, playing)
	want := map[string]any{
		"mpris:trackid": trackPath("abc_-123"),
		"mpris:length":  int64(212500000), // microseconds
		"mpris:artUrl":  "https://i.ytimg.com/vi/abc_-123/mqdefault.jpg",
		"xesam:title":   "Song",
		"xesam:artist":  []string{"Chan"},
		"xesam:url":     "https://www.youtube.com/watch?v=abc_-123",
	}
	for k, v := range want {
		got, ok := m[k]
		if !ok {
			t.Errorf("metadata missing %s", k)
			continue
		}
		if !reflect.DeepEqual(got.Value(), v) {
			t.Errorf("%s = %#v, want %#v", k, got.Value(), v)
		}
	}
}

func TestMetadataWithoutDurationOrChannelOmitsThem(t *testing.T) {
	m := meta(t, State{Active: true, ID: "x", Title: "T"})
	if _, ok := m["mpris:length"]; ok {
		t.Error("unknown duration must not be reported")
	}
	if _, ok := m["xesam:artist"]; ok {
		t.Error("empty channel must not be reported")
	}
}

func TestStoppedWhenNothingPlays(t *testing.T) {
	p := playerProps(State{})
	if p["PlaybackStatus"].Value() != "Stopped" {
		t.Errorf("status = %v", p["PlaybackStatus"].Value())
	}
	for _, k := range []string{"CanGoNext", "CanPlay", "CanPause"} {
		if p[k].Value() != false {
			t.Errorf("%s = %v, want false", k, p[k].Value())
		}
	}
	if got := meta(t, State{})["mpris:trackid"].Value(); got != dbus.ObjectPath(noTrack) {
		t.Errorf("trackid = %v", got)
	}
}

func TestPlaybackStatusAndCapabilities(t *testing.T) {
	p := playerProps(playing)
	if p["PlaybackStatus"].Value() != "Playing" || p["CanPlay"].Value() != true ||
		p["CanPause"].Value() != true || p["CanGoNext"].Value() != true || p["CanControl"].Value() != true {
		t.Errorf("playing props: %v", p)
	}
	paused := playing
	paused.Paused = true
	if playerProps(paused)["PlaybackStatus"].Value() != "Paused" {
		t.Error("expected Paused")
	}
	last := playing
	last.HasNext = false
	if playerProps(last)["CanGoNext"].Value() != false {
		t.Error("CanGoNext must be false on the last track")
	}
	if p["CanGoPrevious"].Value() != false || p["CanSeek"].Value() != false {
		t.Error("previous/seek are not supported")
	}
}

type emission struct {
	iface   string
	changed []string
}

func newTestServer(ctl Controller) (*Server, *[]emission) {
	var got []emission
	s := newServer(ctl, func() time.Duration { return 42 * time.Second }, func(iface string, ch map[string]dbus.Variant) {
		var names []string
		for k := range ch {
			names = append(names, k)
		}
		sort.Strings(names)
		got = append(got, emission{iface, names})
	})
	return s, &got
}

func TestUpdateEmitsOnlyWhatChanged(t *testing.T) {
	s, got := newTestServer(nil)
	s.Update(playing)
	if len(*got) != 1 || (*got)[0].iface != ifacePlayer {
		t.Fatalf("emissions = %v", *got)
	}
	want := []string{"CanGoNext", "CanPause", "CanPlay", "Metadata", "PlaybackStatus"}
	if !reflect.DeepEqual((*got)[0].changed, want) {
		t.Errorf("changed = %v, want %v", (*got)[0].changed, want)
	}

	*got = nil
	s.Update(playing) // identical
	if len(*got) != 0 {
		t.Errorf("identical update emitted %v", *got)
	}

	paused := playing
	paused.Paused = true
	s.Update(paused)
	if len(*got) != 1 || !reflect.DeepEqual((*got)[0].changed, []string{"PlaybackStatus"}) {
		t.Errorf("pause emitted %v", *got)
	}
}

func TestPropertiesGet(t *testing.T) {
	s, _ := newTestServer(nil)
	s.Update(playing)
	po := &propsObj{s}

	v, err := po.Get(ifacePlayer, "PlaybackStatus")
	if err != nil || v.Value() != "Playing" {
		t.Errorf("PlaybackStatus = %v, %v", v, err)
	}
	v, err = po.Get(ifacePlayer, "Position")
	if err != nil || v.Value() != int64(42_000_000) {
		t.Errorf("Position = %v, %v (want 42s in µs)", v, err)
	}
	v, err = po.Get(ifaceRoot, "Identity")
	if err != nil || v.Value() != "ytplay" {
		t.Errorf("Identity = %v, %v", v, err)
	}
	if _, err := po.Get(ifacePlayer, "Nope"); err == nil {
		t.Error("unknown property should fail")
	}
	if _, err := po.Get("org.example.Other", "X"); err == nil {
		t.Error("unknown interface should fail")
	}
}

func TestPropertiesGetAllAndSet(t *testing.T) {
	s, _ := newTestServer(nil)
	po := &propsObj{s}
	all, err := po.GetAll(ifacePlayer)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"PlaybackStatus", "Metadata", "Position", "CanControl", "Volume", "Rate"} {
		if _, ok := all[k]; !ok {
			t.Errorf("GetAll missing %s", k)
		}
	}
	root, err := po.GetAll(ifaceRoot)
	if err != nil || root["CanQuit"].Value() != false || root["HasTrackList"].Value() != false {
		t.Errorf("root props = %v, %v", root, err)
	}
	if err := po.Set(ifacePlayer, "Volume", dbus.MakeVariant(0.5)); err == nil {
		t.Error("Set should be rejected")
	}
}

type fakeCtl struct {
	calls []string
	err   error
}

func (f *fakeCtl) rec(s string) error { f.calls = append(f.calls, s); return f.err }
func (f *fakeCtl) Play() error        { return f.rec("play") }
func (f *fakeCtl) Pause() error       { return f.rec("pause") }
func (f *fakeCtl) PlayPause() error   { return f.rec("playpause") }
func (f *fakeCtl) Next() error        { return f.rec("next") }
func (f *fakeCtl) Stop() error        { return f.rec("stop") }

func TestPlayerMethodsCallTheController(t *testing.T) {
	ctl := &fakeCtl{}
	s, _ := newTestServer(ctl)
	po := &playerObj{s}
	for _, f := range []func() *dbus.Error{po.Play, po.Pause, po.PlayPause, po.Next, po.Stop} {
		if err := f(); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"play", "pause", "playpause", "next", "stop"}
	if !reflect.DeepEqual(ctl.calls, want) {
		t.Errorf("calls = %v, want %v", ctl.calls, want)
	}
	// Unsupported controls are accepted and ignored, not errors.
	if po.Previous() != nil || po.SeekBy(1) != nil || po.SetPosition("/x", 1) != nil || po.OpenUri("x") != nil {
		t.Error("unsupported methods should return nil")
	}
	if len(ctl.calls) != len(want) {
		t.Errorf("unsupported methods reached the controller: %v", ctl.calls)
	}
}

func TestControllerErrorBecomesDBusError(t *testing.T) {
	s, _ := newTestServer(&fakeCtl{err: errors.New("boom")})
	if err := (&playerObj{s}).Next(); err == nil {
		t.Error("expected a D-Bus error")
	}
}
