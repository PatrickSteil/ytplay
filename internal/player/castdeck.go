package player

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/PatrickSteil/ytplay/internal/cast"
	"github.com/PatrickSteil/ytplay/internal/mpv"
)

// Remote is a connection to a cast device. *cast.Client is one.
type Remote interface {
	Launch(ctx context.Context) error
	Load(ctx context.Context, url, contentType, title, artist string) error
	Pause(ctx context.Context) error
	Play(ctx context.Context) error
	Seek(ctx context.Context, seconds float64) error
	SetVolume(ctx context.Context, level float64) error
	Stop(ctx context.Context) error
	Position() (pos, dur float64, state string)
	Events() <-chan cast.Event
	Close()
}

// Resolve turns the address of a song (what mpv would play) into one a cast
// device can fetch by itself: a direct audio stream and its content type.
type Resolve func(ctx context.Context, songURL string) (streamURL, contentType string, err error)

// loadTimeout bounds finding the stream of a song and handing it to the device.
const loadTimeout = 60 * time.Second

// CastDeck is a Deck that plays on a cast device: it takes the mpv commands the
// player sends a deck and turns them into cast commands, and turns what the
// device reports into the events mpv would send. Songs are fetched by the
// device itself, so there is no mixing, only one song at a time.
type CastDeck struct {
	name    string
	r       Remote
	resolve Resolve
	events  chan mpv.Event
	done    chan struct{}
	once    sync.Once

	mu       sync.Mutex
	entry    int64 // the entry id of the song that was last asked for
	cancel   context.CancelFunc
	launched bool
	paused   bool
	loading  bool
	seekTo   float64 // a seek asked for while the song is still being loaded
	seekSet  bool
	ended    int64 // the entry whose end was already reported
}

// NewCastDeck makes a deck of a connected device called name.
func NewCastDeck(name string, r Remote, resolve Resolve) *CastDeck {
	d := &CastDeck{name: name, r: r, resolve: resolve, events: make(chan mpv.Event, 16), done: make(chan struct{})}
	go d.listen()
	return d
}

// Name is the name of the device.
func (d *CastDeck) Name() string { return d.name }

func (d *CastDeck) finish() { d.once.Do(func() { close(d.done) }) }

func (d *CastDeck) emit(ev mpv.Event) {
	select {
	case d.events <- ev:
	case <-d.done:
	}
}

// listen turns the device's reports into mpv events.
func (d *CastDeck) listen() {
	defer d.finish()
	for ev := range d.r.Events() {
		switch ev.Kind {
		case cast.ClosedEvent:
			return
		case cast.MediaEvent:
			d.media(ev)
		}
	}
}

func (d *CastDeck) media(ev cast.Event) {
	d.mu.Lock()
	entry, ended := d.entry, d.ended
	wasPaused := d.paused
	switch ev.State {
	case "PAUSED":
		d.paused = true
	case "PLAYING":
		d.paused = false
	}
	nowPaused := d.paused
	finished := ev.State == "IDLE" && (ev.IdleReason == "FINISHED" || ev.IdleReason == "ERROR") && entry != 0 && entry != ended && !d.loading
	if finished {
		d.ended = entry
	}
	d.mu.Unlock()
	if nowPaused != wasPaused {
		data, _ := json.Marshal(nowPaused)
		d.emit(mpv.Event{Name: "property-change", Property: "pause", Data: data})
	}
	if finished {
		reason := "eof"
		if ev.IdleReason == "ERROR" {
			reason = "error"
		}
		d.emit(mpv.Event{Name: "end-file", Reason: reason, EntryID: entry})
	}
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// Command takes the mpv commands the player uses and does what they mean on the
// device; the rest (speed, observing properties) is quietly accepted.
func (d *CastDeck) Command(ctx context.Context, args ...any) (json.RawMessage, error) {
	if len(args) == 0 {
		return nil, errors.New("no command")
	}
	switch args[0] {
	case "loadfile":
		url, _ := args[1].(string)
		return d.load(url), nil
	case "stop":
		d.mu.Lock()
		if d.cancel != nil {
			d.cancel()
		}
		d.entry++ // what was loading is forgotten
		wasUp := d.launched
		d.launched, d.loading, d.seekSet = false, false, false
		d.mu.Unlock()
		if wasUp {
			return nil, d.r.Stop(ctx)
		}
	case "cycle":
		if len(args) > 1 && args[1] == "pause" {
			d.mu.Lock()
			now := !d.paused
			d.mu.Unlock()
			return nil, d.setPause(ctx, now)
		}
	case "set_property":
		if len(args) < 3 {
			break
		}
		switch args[1] {
		case "pause":
			p, _ := args[2].(bool)
			return nil, d.setPause(ctx, p)
		case "volume":
			if v, ok := number(args[2]); ok {
				return nil, d.r.SetVolume(ctx, v/100)
			}
		}
	case "seek":
		if len(args) < 3 {
			break
		}
		x, ok := number(args[1])
		mode, _ := args[2].(string)
		if !ok {
			break
		}
		pos, dur, _ := d.r.Position()
		switch mode {
		case "relative":
			x += pos
		case "absolute-percent":
			x = dur * x / 100
		}
		return nil, d.seek(ctx, max(x, 0))
	}
	return nil, nil
}

func (d *CastDeck) setPause(ctx context.Context, p bool) error {
	d.mu.Lock()
	d.paused = p
	d.mu.Unlock()
	if p {
		return d.r.Pause(ctx)
	}
	return d.r.Play(ctx)
}

// seek seeks now, or when the song that is still being loaded is there.
func (d *CastDeck) seek(ctx context.Context, to float64) error {
	d.mu.Lock()
	if d.loading {
		d.seekTo, d.seekSet = to, true
		d.mu.Unlock()
		return nil
	}
	d.mu.Unlock()
	return d.r.Seek(ctx, to)
}

// load starts getting url onto the device in the background, so that the player
// (which holds its lock while it sends commands) is not kept waiting while the
// address of the stream is worked out. It returns mpv's reply: an entry id.
func (d *CastDeck) load(songURL string) json.RawMessage {
	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	d.mu.Lock()
	if d.cancel != nil {
		d.cancel() // an earlier song still being loaded is no longer wanted
	}
	d.entry++
	id := d.entry
	d.cancel, d.loading, d.seekSet = cancel, true, false
	d.mu.Unlock()
	go func() {
		defer cancel()
		err := d.fetch(ctx, id, songURL)
		d.mu.Lock()
		current := d.entry == id
		seekTo, seek := d.seekTo, d.seekSet
		if current {
			d.loading, d.seekSet = false, false
		}
		d.mu.Unlock()
		switch {
		case !current:
		case err != nil:
			d.emit(mpv.Event{Name: "end-file", Reason: "error", EntryID: id})
		case seek:
			_ = d.r.Seek(ctx, seekTo)
		}
	}()
	return json.RawMessage(fmt.Sprintf(`{"playlist_entry_id":%d}`, id))
}

func (d *CastDeck) fetch(ctx context.Context, id int64, songURL string) error {
	stream, contentType, err := d.resolve(ctx, songURL)
	if err != nil {
		return err
	}
	d.mu.Lock()
	if d.entry != id {
		d.mu.Unlock()
		return nil
	}
	up := d.launched
	d.mu.Unlock()
	if !up {
		if err := d.r.Launch(ctx); err != nil {
			return err
		}
		d.mu.Lock()
		d.launched = true
		d.mu.Unlock()
	}
	return d.r.Load(ctx, stream, contentType, "", "")
}

// GetFloat answers the properties the player reads: the position and length of
// the song, which exist while it is on the device.
func (d *CastDeck) GetFloat(ctx context.Context, prop string) (float64, error) {
	d.mu.Lock()
	loading := d.loading
	d.mu.Unlock()
	pos, dur, state := d.r.Position()
	if loading || state == "IDLE" {
		return 0, errors.New("property unavailable")
	}
	switch prop {
	case "time-pos":
		return pos, nil
	case "duration":
		if dur > 0 {
			return dur, nil
		}
	}
	return 0, errors.New("property unavailable")
}

func (d *CastDeck) Events() <-chan mpv.Event { return d.events }
func (d *CastDeck) Done() <-chan struct{}    { return d.done }
func (d *CastDeck) StderrTail() string       { return "" }

// Close stops what plays on the device and drops the connection: closing the
// player is the same as stopping the music.
func (d *CastDeck) Close() error {
	d.mu.Lock()
	if d.cancel != nil {
		d.cancel()
	}
	up := d.launched
	d.launched = false
	d.mu.Unlock()
	if up {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = d.r.Stop(ctx)
		cancel()
	}
	d.r.Close()
	d.finish()
	return nil
}
