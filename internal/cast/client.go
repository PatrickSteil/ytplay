package cast

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

var (
	launchTimeout  = 10 * time.Second // how long the media app may take to come up
	requestTimeout = 10 * time.Second // how long a command may wait for its answer
	heartbeatEvery = 5 * time.Second
)

// EventKind says what an Event is about.
type EventKind int

const (
	MediaEvent  EventKind = iota // the player's state changed
	VolumeEvent                  // the device's volume changed
	ClosedEvent                  // the connection is gone
)

// Event is something the device told us.
type Event struct {
	Kind EventKind
	// MediaEvent: State is IDLE, BUFFERING, PLAYING or PAUSED; IdleReason says
	// why it is IDLE (FINISHED, ERROR, CANCELLED, INTERRUPTED).
	State, IdleReason  string
	Position, Duration float64
	// VolumeEvent
	Volume float64
	Muted  bool
}

// Client is a connection to one cast device.
type Client struct {
	conn   net.Conn
	wmu    sync.Mutex
	events chan Event
	closed chan struct{}
	once   sync.Once
	now    func() time.Time

	mu        sync.Mutex
	reqID     int
	pending   map[int]chan map[string]any
	transport string // the media app's transport id, once launched
	session   string // and its session id
	media     int    // the media session id of what is loaded
	state     string
	pos, dur  float64
	at        time.Time // when pos was true
	volume    float64
	muted     bool
	appUp     chan struct{} // told that a status with the app arrived
}

// Dial connects to a device at addr (host:8009) and starts listening to it.
func Dial(ctx context.Context, addr string) (*Client, error) {
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: &tls.Config{InsecureSkipVerify: true}} // devices use their own certificates
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("connect to the speaker: %w", err)
	}
	c := &Client{conn: conn, events: make(chan Event, 64), closed: make(chan struct{}), now: time.Now,
		pending: map[int]chan map[string]any{}, appUp: make(chan struct{}, 1), state: "IDLE"}
	go c.read()
	if err := c.send(nsConnection, "receiver-0", map[string]any{"type": "CONNECT"}); err != nil {
		c.Close()
		return nil, err
	}
	go c.heartbeat()
	return c, nil
}

// Events delivers what the device reports. It is closed after a ClosedEvent.
func (c *Client) Events() <-chan Event { return c.events }

// Close ends the connection.
func (c *Client) Close() {
	c.once.Do(func() {
		close(c.closed)
		c.conn.Close()
	})
}

func (c *Client) send(ns, dest string, payload map[string]any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	select {
	case <-c.closed:
		return errors.New("the connection to the speaker is closed")
	default:
	}
	c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return writeMessage(c.conn, message{Source: "sender-0", Dest: dest, Namespace: ns, Payload: string(b)})
}

func (c *Client) heartbeat() {
	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if c.send(nsHeartbeat, "receiver-0", map[string]any{"type": "PING"}) != nil {
				return
			}
		case <-c.closed:
			return
		}
	}
}

func (c *Client) emit(ev Event) {
	select {
	case c.events <- ev:
	case <-c.closed:
	}
}

func (c *Client) read() {
	defer func() {
		c.Close()
		select { // the buffer has room: this must not be lost to the closed channel
		case c.events <- Event{Kind: ClosedEvent}:
		default:
		}
		close(c.events)
	}()
	for {
		m, err := readMessage(c.conn)
		if err != nil {
			return
		}
		var p map[string]any
		if json.Unmarshal([]byte(m.Payload), &p) != nil {
			continue
		}
		c.handle(m, p)
	}
}

func (c *Client) handle(m message, p map[string]any) {
	typ, _ := p["type"].(string)
	switch typ {
	case "PING":
		_ = c.send(nsHeartbeat, m.Source, map[string]any{"type": "PONG"})
		return
	case "RECEIVER_STATUS":
		c.receiverStatus(p)
	case "MEDIA_STATUS":
		c.mediaStatus(p)
	}
	if id, ok := p["requestId"].(float64); ok && id != 0 {
		c.mu.Lock()
		ch := c.pending[int(id)]
		c.mu.Unlock()
		if ch != nil {
			select {
			case ch <- p:
			default:
			}
		}
	}
}

func (c *Client) receiverStatus(p map[string]any) {
	st, _ := p["status"].(map[string]any)
	c.mu.Lock()
	if vol, ok := st["volume"].(map[string]any); ok {
		if l, ok := vol["level"].(float64); ok {
			c.volume = l
		}
		if mu, ok := vol["muted"].(bool); ok {
			c.muted = mu
		}
	}
	ev := Event{Kind: VolumeEvent, Volume: c.volume, Muted: c.muted}
	apps, _ := st["applications"].([]any)
	for _, a := range apps {
		app, _ := a.(map[string]any)
		if app["appId"] == defaultReceiver {
			c.transport, _ = app["transportId"].(string)
			c.session, _ = app["sessionId"].(string)
			select {
			case c.appUp <- struct{}{}:
			default:
			}
		}
	}
	c.mu.Unlock()
	c.emit(ev)
}

func (c *Client) mediaStatus(p map[string]any) {
	list, _ := p["status"].([]any)
	if len(list) == 0 {
		return
	}
	st, _ := list[0].(map[string]any)
	c.mu.Lock()
	if id, ok := st["mediaSessionId"].(float64); ok {
		c.media = int(id)
	}
	c.state, _ = st["playerState"].(string)
	c.pos, _ = st["currentTime"].(float64)
	if media, ok := st["media"].(map[string]any); ok {
		if d, ok := media["duration"].(float64); ok {
			c.dur = d
		}
	}
	c.at = c.now()
	reason, _ := st["idleReason"].(string)
	ev := Event{Kind: MediaEvent, State: c.state, IdleReason: reason, Position: c.pos, Duration: c.dur}
	c.mu.Unlock()
	c.emit(ev)
}

// request sends a command that has an answer, and waits for it.
func (c *Client) request(ctx context.Context, ns, dest string, payload map[string]any) (map[string]any, error) {
	c.mu.Lock()
	c.reqID++
	id := c.reqID
	ch := make(chan map[string]any, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	payload["requestId"] = id
	if err := c.send(ns, dest, payload); err != nil {
		return nil, err
	}
	timeout := time.NewTimer(requestTimeout)
	defer timeout.Stop()
	select {
	case p := <-ch:
		return p, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timeout.C:
		return nil, errors.New("the speaker did not answer")
	case <-c.closed:
		return nil, errors.New("the connection to the speaker is closed")
	}
}

// Launch starts the speaker's built-in media player and connects to it.
func (c *Client) Launch(ctx context.Context) error {
	if _, err := c.request(ctx, nsReceiver, "receiver-0", map[string]any{"type": "LAUNCH", "appId": defaultReceiver}); err != nil {
		return err
	}
	deadline := time.NewTimer(launchTimeout)
	defer deadline.Stop()
	for {
		c.mu.Lock()
		transport := c.transport
		c.mu.Unlock()
		if transport != "" {
			return c.send(nsConnection, transport, map[string]any{"type": "CONNECT"})
		}
		select {
		case <-c.appUp:
		case <-time.After(500 * time.Millisecond):
			_ = c.send(nsReceiver, "receiver-0", map[string]any{"type": "GET_STATUS", "requestId": 0})
		case <-deadline.C:
			return errors.New("the speaker's media player did not start")
		case <-ctx.Done():
			return ctx.Err()
		case <-c.closed:
			return errors.New("the connection to the speaker is closed")
		}
	}
}

func (c *Client) target() (transport string, media int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.transport == "" {
		return "", 0, errors.New("the speaker's media player is not running")
	}
	return c.transport, c.media, nil
}

// Load plays the audio at url (contentType such as audio/mp4) from the start.
func (c *Client) Load(ctx context.Context, url, contentType, title, artist string) error {
	transport, _, err := c.target()
	if err != nil {
		return err
	}
	p, err := c.request(ctx, nsMedia, transport, map[string]any{
		"type": "LOAD", "autoplay": true, "currentTime": 0,
		"media": map[string]any{
			"contentId": url, "contentType": contentType, "streamType": "BUFFERED",
			"metadata": map[string]any{"metadataType": 3, "title": title, "artist": artist},
		},
	})
	if err != nil {
		return err
	}
	switch p["type"] {
	case "MEDIA_STATUS":
		return nil
	default:
		return fmt.Errorf("the speaker refused to play it (%v)", p["type"])
	}
}

func (c *Client) mediaCommand(ctx context.Context, typ string, extra map[string]any) error {
	transport, media, err := c.target()
	if err != nil {
		return err
	}
	payload := map[string]any{"type": typ, "mediaSessionId": media}
	for k, v := range extra {
		payload[k] = v
	}
	_, err = c.request(ctx, nsMedia, transport, payload)
	return err
}

// Pause, Play and Seek act on what is loaded.
func (c *Client) Pause(ctx context.Context) error { return c.mediaCommand(ctx, "PAUSE", nil) }
func (c *Client) Play(ctx context.Context) error  { return c.mediaCommand(ctx, "PLAY", nil) }
func (c *Client) Seek(ctx context.Context, seconds float64) error {
	return c.mediaCommand(ctx, "SEEK", map[string]any{"currentTime": seconds})
}

// SetVolume sets the device's volume (0 to 1), SetMuted mutes or unmutes it.
func (c *Client) SetVolume(ctx context.Context, level float64) error {
	_, err := c.request(ctx, nsReceiver, "receiver-0", map[string]any{"type": "SET_VOLUME", "volume": map[string]any{"level": min(max(level, 0), 1)}})
	return err
}
func (c *Client) SetMuted(ctx context.Context, muted bool) error {
	_, err := c.request(ctx, nsReceiver, "receiver-0", map[string]any{"type": "SET_VOLUME", "volume": map[string]any{"muted": muted}})
	return err
}

// Stop quits the media player on the device.
func (c *Client) Stop(ctx context.Context) error {
	c.mu.Lock()
	session := c.session
	c.mu.Unlock()
	if session == "" {
		return nil
	}
	_, err := c.request(ctx, nsReceiver, "receiver-0", map[string]any{"type": "STOP", "sessionId": session})
	c.mu.Lock()
	c.transport, c.session, c.state = "", "", "IDLE"
	c.mu.Unlock()
	return err
}

// Position is how far the loaded song is (seconds), how long it is, and the
// player's state; while it plays the position runs on from the last report.
func (c *Client) Position() (pos, dur float64, state string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	pos = c.pos
	if c.state == "PLAYING" {
		pos += c.now().Sub(c.at).Seconds()
	}
	return pos, c.dur, c.state
}
