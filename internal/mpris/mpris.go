// Package mpris exposes the player on the D-Bus session bus using the MPRIS
// specification, so desktop shells (GNOME's notification area, KDE, media
// keys, playerctl) show the current track and offer play / pause / next.
//
// Only the parts of MPRIS that make sense for ytplay are implemented: no
// track list, no seeking, no previous track.
package mpris

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"

	"github.com/PatrickSteil/ytplay/internal/yt"
)

const (
	busName     = "org.mpris.MediaPlayer2.ytplay"
	objPath     = dbus.ObjectPath("/org/mpris/MediaPlayer2")
	ifaceRoot   = "org.mpris.MediaPlayer2"
	ifacePlayer = "org.mpris.MediaPlayer2.Player"
	ifaceProps  = "org.freedesktop.DBus.Properties"
	noTrack     = "/org/mpris/MediaPlayer2/TrackList/NoTrack"
)

// State is what the shell should show.
type State struct {
	Active   bool // a track is current (playing or paused)
	ID       string
	Title    string
	Channel  string
	Duration float64 // seconds, 0 when unknown
	Paused   bool
	HasNext  bool
}

// Controller performs the actions the shell's buttons request.
type Controller interface {
	Play() error
	Pause() error
	PlayPause() error
	Next() error
	Stop() error
}

// trackPath turns a video ID into a valid D-Bus object path. Object path
// elements allow only [A-Za-z0-9_], but IDs contain '-' and '_', so every
// other byte is hex-escaped ("_" itself too) which keeps the mapping unique.
func trackPath(id string) dbus.ObjectPath {
	var sb strings.Builder
	sb.WriteString("/org/ytplay/track/")
	for i := 0; i < len(id); i++ {
		c := id[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			sb.WriteByte(c)
		} else {
			fmt.Fprintf(&sb, "_%02x", c)
		}
	}
	return dbus.ObjectPath(sb.String())
}

func metadata(st State) map[string]dbus.Variant {
	if !st.Active {
		return map[string]dbus.Variant{"mpris:trackid": dbus.MakeVariant(dbus.ObjectPath(noTrack))}
	}
	r := yt.Result{ID: st.ID}
	m := map[string]dbus.Variant{
		"mpris:trackid": dbus.MakeVariant(trackPath(st.ID)),
		"mpris:artUrl":  dbus.MakeVariant(r.ThumbURL()),
		"xesam:title":   dbus.MakeVariant(st.Title),
		"xesam:url":     dbus.MakeVariant(r.URL()),
	}
	if st.Duration > 0 {
		m["mpris:length"] = dbus.MakeVariant(int64(st.Duration * 1e6)) // µs
	}
	if st.Channel != "" {
		m["xesam:artist"] = dbus.MakeVariant([]string{st.Channel})
	}
	return m
}

// playerProps are the state-dependent properties of the Player interface.
// (Position is read on demand instead.)
func playerProps(st State) map[string]dbus.Variant {
	status := "Stopped"
	switch {
	case st.Active && st.Paused:
		status = "Paused"
	case st.Active:
		status = "Playing"
	}
	v := dbus.MakeVariant
	return map[string]dbus.Variant{
		"PlaybackStatus": v(status),
		"Metadata":       v(metadata(st)),
		"CanGoNext":      v(st.Active && st.HasNext),
		"CanGoPrevious":  v(false),
		"CanPlay":        v(st.Active),
		"CanPause":       v(st.Active),
		"CanSeek":        v(false),
		"CanControl":     v(true),
		"LoopStatus":     v("None"),
		"Rate":           v(1.0),
		"Shuffle":        v(false),
		"Volume":         v(1.0),
		"MinimumRate":    v(1.0),
		"MaximumRate":    v(1.0),
	}
}

func rootProps() map[string]dbus.Variant {
	v := dbus.MakeVariant
	return map[string]dbus.Variant{
		"CanQuit":             v(false),
		"CanRaise":            v(false),
		"CanSetFullscreen":    v(false),
		"Fullscreen":          v(false),
		"HasTrackList":        v(false),
		"Identity":            v("ytplay"),
		"SupportedUriSchemes": v([]string{}),
		"SupportedMimeTypes":  v([]string{}),
	}
}

// Server owns the D-Bus presence.
type Server struct {
	ctl      Controller
	position func() time.Duration
	emit     func(iface string, changed map[string]dbus.Variant)

	mu     sync.Mutex
	player map[string]dbus.Variant

	conn *dbus.Conn
}

func newServer(ctl Controller, position func() time.Duration, emit func(string, map[string]dbus.Variant)) *Server {
	return &Server{ctl: ctl, position: position, emit: emit, player: playerProps(State{})}
}

// Update publishes a new state. Only properties that actually changed are
// signalled (PropertiesChanged), which is what shells listen for.
func (s *Server) Update(st State) {
	next := playerProps(st)
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := map[string]dbus.Variant{}
	for k, v := range next {
		if !reflect.DeepEqual(v.Value(), s.player[k].Value()) {
			changed[k] = v
		}
	}
	s.player = next
	if len(changed) > 0 && s.emit != nil {
		s.emit(ifacePlayer, changed)
	}
}

// Close leaves the bus.
func (s *Server) Close() {
	if s.conn != nil {
		s.conn.Close()
	}
}

// Start connects to the session bus and registers the player. It fails (and
// the caller should simply carry on without MPRIS) when there is no session
// bus, e.g. over SSH or on a bare macOS terminal.
func Start(ctl Controller, position func() time.Duration) (*Server, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("connect to session bus: %w", err)
	}
	s := newServer(ctl, position, func(iface string, changed map[string]dbus.Variant) {
		_ = conn.Emit(objPath, ifaceProps+".PropertiesChanged", iface, changed, []string{})
	})
	s.conn = conn

	exports := []struct {
		obj     any
		iface   string
		mapping map[string]string
	}{
		{&rootObj{s}, ifaceRoot, nil},
		{&playerObj{s}, ifacePlayer, map[string]string{"SeekBy": "Seek"}},
		{&propsObj{s}, ifaceProps, nil},
	}
	for _, e := range exports {
		if err := conn.ExportWithMap(e.obj, e.mapping, objPath, e.iface); err != nil {
			conn.Close()
			return nil, fmt.Errorf("export %s: %w", e.iface, err)
		}
	}
	playerMethods := introspect.Methods(&playerObj{})
	for i := range playerMethods {
		if playerMethods[i].Name == "SeekBy" {
			playerMethods[i].Name = "Seek"
		}
	}
	node := &introspect.Node{Name: string(objPath), Interfaces: []introspect.Interface{
		introspect.IntrospectData,
		prop.IntrospectData,
		{Name: ifaceRoot, Methods: introspect.Methods(&rootObj{}), Properties: propSpecs(rootProps(), nil)},
		{Name: ifacePlayer, Methods: playerMethods,
			Properties: propSpecs(playerProps(State{}), map[string]string{"Position": "x"})},
	}}
	if err := conn.Export(introspect.NewIntrospectable(node), objPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("export introspection: %w", err)
	}

	name := busName
	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err == nil && reply != dbus.RequestNameReplyPrimaryOwner {
		// Another ytplay already owns the name: use an instance-specific one.
		name = fmt.Sprintf("%s.instance%d", busName, os.Getpid())
		reply, err = conn.RequestName(name, dbus.NameFlagDoNotQueue)
	}
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		conn.Close()
		return nil, fmt.Errorf("request bus name %s: %v (reply %d)", name, err, reply)
	}
	return s, nil
}

// propSpecs derives introspection data (all read-only) from property values.
func propSpecs(props map[string]dbus.Variant, extra map[string]string) []introspect.Property {
	var out []introspect.Property
	for name, v := range props {
		out = append(out, introspect.Property{Name: name, Type: v.Signature().String(), Access: "read"})
	}
	for name, sig := range extra {
		out = append(out, introspect.Property{Name: name, Type: sig, Access: "read"})
	}
	return out
}

// rootObj implements org.mpris.MediaPlayer2.
type rootObj struct{ s *Server }

func (rootObj) Raise() *dbus.Error { return nil } // CanRaise is false
func (rootObj) Quit() *dbus.Error  { return nil } // CanQuit is false

// playerObj implements org.mpris.MediaPlayer2.Player.
type playerObj struct{ s *Server }

func (p *playerObj) call(f func() error) *dbus.Error {
	if p.s == nil || p.s.ctl == nil {
		return nil
	}
	if err := f(); err != nil {
		return dbus.MakeFailedError(err)
	}
	return nil
}

func (p *playerObj) Play() *dbus.Error  { return p.call(func() error { return p.s.ctl.Play() }) }
func (p *playerObj) Pause() *dbus.Error { return p.call(func() error { return p.s.ctl.Pause() }) }
func (p *playerObj) PlayPause() *dbus.Error {
	return p.call(func() error { return p.s.ctl.PlayPause() })
}
func (p *playerObj) Next() *dbus.Error { return p.call(func() error { return p.s.ctl.Next() }) }
func (p *playerObj) Stop() *dbus.Error { return p.call(func() error { return p.s.ctl.Stop() }) }

// The rest is advertised as unsupported (CanGoPrevious / CanSeek are false).
func (p *playerObj) Previous() *dbus.Error { return nil }

// SeekBy is exported on the bus as "Seek"; go vet reserves the Go name Seek
// for io.Seeker-shaped methods.
func (p *playerObj) SeekBy(offset int64) *dbus.Error                      { return nil }
func (p *playerObj) SetPosition(t dbus.ObjectPath, pos int64) *dbus.Error { return nil }
func (p *playerObj) OpenUri(uri string) *dbus.Error                       { return nil }

// propsObj implements org.freedesktop.DBus.Properties by hand so that
// Position can be read live from mpv.
type propsObj struct{ s *Server }

func (p *propsObj) all(iface string) (map[string]dbus.Variant, bool) {
	switch iface {
	case ifaceRoot:
		return rootProps(), true
	case ifacePlayer:
		p.s.mu.Lock()
		out := make(map[string]dbus.Variant, len(p.s.player)+1)
		for k, v := range p.s.player {
			out[k] = v
		}
		p.s.mu.Unlock()
		var pos time.Duration
		if p.s.position != nil {
			pos = p.s.position()
		}
		out["Position"] = dbus.MakeVariant(pos.Microseconds())
		return out, true
	}
	return nil, false
}

func (p *propsObj) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	props, ok := p.all(iface)
	if !ok {
		return dbus.Variant{}, prop.ErrIfaceNotFound
	}
	v, ok := props[name]
	if !ok {
		return dbus.Variant{}, prop.ErrPropNotFound
	}
	return v, nil
}

func (p *propsObj) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	props, ok := p.all(iface)
	if !ok {
		return nil, prop.ErrIfaceNotFound
	}
	return props, nil
}

func (p *propsObj) Set(iface, name string, value dbus.Variant) *dbus.Error {
	return prop.ErrReadOnly
}
