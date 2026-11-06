// Package mpv starts one long-running mpv process and controls it over
// mpv's JSON IPC socket.
package mpv

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Event is an asynchronous message from mpv.
type Event struct {
	Name     string          // event name, e.g. "property-change", "end-file"
	Property string          // property name for property-change
	Data     json.RawMessage // "data" field (property-change)
	Reason   string          // "reason" of end-file; "error" means the file failed to load
}

// CommandError is mpv's rejection of a command (e.g. "property unavailable"),
// as opposed to a transport failure.
type CommandError struct{ Msg string }

func (e *CommandError) Error() string { return "mpv: " + e.Msg }

type reply struct {
	data json.RawMessage
	err  error
}

// Client talks to one mpv process.
type Client struct {
	conn net.Conn

	wmu    sync.Mutex // serialises writes and guards nextID
	nextID int64

	pmu     sync.Mutex // guards pending
	pending map[int64]chan reply

	events chan Event

	done     chan struct{} // closed when mpv exits or the connection is lost
	doneOnce sync.Once

	// Set only for clients created by Start.
	cmd        *exec.Cmd
	exited     chan struct{} // closed after cmd.Wait returns
	sockPath   string
	stderr     *tailBuffer
	closeOnce  sync.Once
	closeError error
}

// wireMsg covers both replies and events.
type wireMsg struct {
	RequestID *int64          `json:"request_id"`
	Error     string          `json:"error"`
	Data      json.RawMessage `json:"data"`
	Event     string          `json:"event"`
	Name      string          `json:"name"`
	Reason    string          `json:"reason"`
}

func newClient(conn net.Conn) *Client {
	c := &Client{
		conn:    conn,
		pending: make(map[int64]chan reply),
		events:  make(chan Event, 256),
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c
}

func (c *Client) markDone() { c.doneOnce.Do(func() { close(c.done) }) }

func (c *Client) readLoop() {
	defer func() {
		c.markDone()
		c.pmu.Lock()
		for id, ch := range c.pending {
			ch <- reply{err: errors.New("mpv connection closed")}
			delete(c.pending, id)
		}
		c.pmu.Unlock()
	}()
	r := bufio.NewReaderSize(c.conn, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			c.handleLine(line)
		}
		if err != nil {
			return
		}
	}
}

func (c *Client) handleLine(line []byte) {
	var m wireMsg
	if err := json.Unmarshal(line, &m); err != nil {
		return // ignore garbage
	}
	if m.RequestID != nil {
		c.pmu.Lock()
		ch, ok := c.pending[*m.RequestID]
		delete(c.pending, *m.RequestID)
		c.pmu.Unlock()
		if !ok {
			return
		}
		if m.Error != "" && m.Error != "success" {
			ch <- reply{err: &CommandError{Msg: m.Error}}
		} else {
			ch <- reply{data: m.Data}
		}
		return
	}
	if m.Event == "" {
		return
	}
	ev := Event{Name: m.Event, Data: m.Data, Reason: m.Reason}
	if m.Event == "property-change" {
		ev.Property = m.Name
	}
	select {
	case c.events <- ev:
	case <-c.done:
	}
}

// Command sends one command and waits for its reply.
func (c *Client) Command(ctx context.Context, args ...any) (json.RawMessage, error) {
	ch := make(chan reply, 1)

	c.wmu.Lock()
	c.nextID++
	id := c.nextID
	c.pmu.Lock()
	c.pending[id] = ch
	c.pmu.Unlock()
	payload, err := json.Marshal(map[string]any{"command": args, "request_id": id})
	if err == nil {
		_, err = c.conn.Write(append(payload, '\n'))
	}
	c.wmu.Unlock()
	if err != nil {
		c.forget(id)
		return nil, fmt.Errorf("send mpv command: %w", err)
	}

	select {
	case r := <-ch:
		return r.data, r.err
	case <-ctx.Done():
		c.forget(id)
		return nil, fmt.Errorf("mpv command: %w", ctx.Err())
	case <-c.done:
		// The reader may have delivered a reply just before exiting.
		select {
		case r := <-ch:
			return r.data, r.err
		default:
		}
		c.forget(id)
		return nil, errors.New("mpv exited")
	}
}

func (c *Client) forget(id int64) {
	c.pmu.Lock()
	delete(c.pending, id)
	c.pmu.Unlock()
}

// GetFloat reads a numeric property.
func (c *Client) GetFloat(ctx context.Context, prop string) (float64, error) {
	raw, err := c.Command(ctx, "get_property", prop)
	if err != nil {
		return 0, err
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, fmt.Errorf("property %s: %w", prop, err)
	}
	return f, nil
}

// GetInt reads an integer property.
func (c *Client) GetInt(ctx context.Context, prop string) (int, error) {
	f, err := c.GetFloat(ctx, prop)
	return int(f), err
}

// Observe registers a property observer; changes arrive on Events().
func (c *Client) Observe(id int, prop string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := c.Command(ctx, "observe_property", id, prop)
	return err
}

// Events returns the channel of asynchronous mpv events.
func (c *Client) Events() <-chan Event { return c.events }

// Done is closed when mpv exits or the connection is lost.
func (c *Client) Done() <-chan struct{} { return c.done }

// StderrTail returns the last part of mpv's stderr (empty for clients not
// created by Start).
func (c *Client) StderrTail() string {
	if c.stderr == nil {
		return ""
	}
	return c.stderr.String()
}

// Close asks mpv to quit, kills it after 2 s, and removes the socket.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		if c.cmd != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			_, _ = c.Command(ctx, "quit") // mpv may close before replying
			cancel()
			select {
			case <-c.exited:
			case <-time.After(2 * time.Second):
				_ = c.cmd.Process.Kill()
				<-c.exited
			}
		}
		c.closeError = c.conn.Close()
		c.markDone()
		if c.sockPath != "" {
			_ = os.Remove(c.sockPath)
		}
	})
	return c.closeError
}

// SocketPath returns the IPC socket path ("" for clients not made by Start).
func SocketPath() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, fmt.Sprintf("ytplay-%d.sock", os.Getpid()))
}

// Start launches mpv in idle mode and connects to its IPC socket.
func Start(ctx context.Context, ytdlpPath string) (*Client, error) {
	mpvPath, err := exec.LookPath("mpv")
	if err != nil {
		return nil, fmt.Errorf("find mpv: %w", err)
	}
	sock := SocketPath()
	_ = os.Remove(sock) // stale file from a previous run with the same pid

	cmd := exec.Command(mpvPath,
		"--idle=yes", "--no-video", "--no-terminal", "--force-window=no",
		"--ytdl-format=bestaudio/best",
		"--script-opts=ytdl_hook-ytdl_path="+ytdlpPath,
		"--input-ipc-server="+sock,
	)
	stderr := &tailBuffer{max: 4096}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start mpv: %w", err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()

	fail := func(cause error) (*Client, error) {
		_ = cmd.Process.Kill()
		<-exited
		_ = os.Remove(sock)
		return nil, fmt.Errorf("%w (mpv stderr: %s)", cause, strings.TrimSpace(stderr.String()))
	}

	deadline := time.Now().Add(5 * time.Second)
	var conn net.Conn
	for {
		conn, err = net.Dial("unix", sock)
		if err == nil {
			break
		}
		select {
		case <-exited:
			return fail(errors.New("mpv exited before its socket appeared"))
		case <-ctx.Done():
			return fail(fmt.Errorf("connect to mpv: %w", ctx.Err()))
		case <-time.After(50 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return fail(fmt.Errorf("connect to mpv socket: %w", err))
		}
	}

	c := newClient(conn)
	c.cmd = cmd
	c.exited = exited
	c.sockPath = sock
	c.stderr = stderr
	go func() {
		<-exited
		c.markDone()
	}()
	return c, nil
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// ObservedProperties are the properties the app observes. time-pos and
// percent-pos are deliberately absent: they fire constantly, so the UI polls.
var ObservedProperties = []string{"pause", "media-title", "playlist", "playlist-pos", "idle-active"}

// ObserveAll registers observers for ObservedProperties (ids 1..n).
func (c *Client) ObserveAll() error {
	for i, p := range ObservedProperties {
		if err := c.Observe(i+1, p); err != nil {
			return fmt.Errorf("observe %s: %w", p, err)
		}
	}
	return nil
}
