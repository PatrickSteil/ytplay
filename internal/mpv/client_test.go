package mpv

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// fakeServer listens on a unix socket and hands the accepted connection to fn.
func fakeServer(t *testing.T, fn func(conn net.Conn)) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mpv.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		fn(conn)
	}()
	return path
}

func dialFake(t *testing.T, path string) *Client {
	t.Helper()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	c := newClient(conn)
	t.Cleanup(func() { conn.Close() })
	return c
}

type req struct {
	Command   []any `json:"command"`
	RequestID int64 `json:"request_id"`
}

func TestRepliesRoutedByRequestIDAndEventsDelivered(t *testing.T) {
	path := fakeServer(t, func(conn net.Conn) {
		sc := bufio.NewScanner(conn)
		var reqs []req
		for len(reqs) < 2 && sc.Scan() {
			var r req
			if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
				return
			}
			reqs = append(reqs, r)
		}
		// Interleave an event, then answer in reverse order.
		conn.Write([]byte(`{"event":"property-change","id":3,"name":"pause","data":true}` + "\n"))
		conn.Write([]byte(`{"request_id":` + itoa(reqs[1].RequestID) + `,"error":"success","data":"second"}` + "\n"))
		conn.Write([]byte(`{"event":"end-file","reason":"error"}` + "\n"))
		conn.Write([]byte(`{"request_id":` + itoa(reqs[0].RequestID) + `,"error":"success","data":"first"}` + "\n"))
	})
	c := dialFake(t, path)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	type pair struct{ arg, data string }
	pairs := make(chan pair, 2)
	call := func(arg string) {
		raw, err := c.Command(ctx, "get_property", arg)
		if err != nil {
			t.Errorf("Command(%s): %v", arg, err)
			pairs <- pair{arg, ""}
			return
		}
		var s string
		json.Unmarshal(raw, &s)
		pairs <- pair{arg, s}
	}
	go call("a")
	// Make sure "a" is written before "b" so the server sees them in order.
	time.Sleep(50 * time.Millisecond)
	go call("b")

	for i := 0; i < 2; i++ {
		p := <-pairs
		want := map[string]string{"a": "first", "b": "second"}[p.arg]
		if p.data != want {
			t.Errorf("reply for %q = %q, want %q", p.arg, p.data, want)
		}
	}

	var names []string
	for i := 0; i < 2; i++ {
		select {
		case ev := <-c.Events():
			names = append(names, ev.Name)
			if ev.Name == "property-change" {
				if ev.Property != "pause" || string(ev.Data) != "true" {
					t.Errorf("bad property-change event: %+v", ev)
				}
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for events")
		}
	}
	if len(names) != 2 || names[0] != "property-change" || names[1] != "end-file" {
		t.Errorf("events = %v", names)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestErrorReplyBecomesGoError(t *testing.T) {
	path := fakeServer(t, func(conn net.Conn) {
		sc := bufio.NewScanner(conn)
		for sc.Scan() {
			var r req
			json.Unmarshal(sc.Bytes(), &r)
			conn.Write([]byte(`{"request_id":` + itoa(r.RequestID) + `,"error":"property unavailable"}` + "\n"))
		}
	})
	c := dialFake(t, path)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := c.Command(ctx, "get_property", "time-pos")
	if err == nil || err.Error() == "" {
		t.Fatalf("expected error, got %v", err)
	}
	if _, err := c.GetFloat(ctx, "time-pos"); err == nil {
		t.Fatal("GetFloat should fail on error reply")
	}
}

func TestGetFloatAndGetInt(t *testing.T) {
	path := fakeServer(t, func(conn net.Conn) {
		sc := bufio.NewScanner(conn)
		for sc.Scan() {
			var r req
			json.Unmarshal(sc.Bytes(), &r)
			conn.Write([]byte(`{"request_id":` + itoa(r.RequestID) + `,"error":"success","data":7}` + "\n"))
		}
	})
	c := dialFake(t, path)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	f, err := c.GetFloat(ctx, "duration")
	if err != nil || f != 7 {
		t.Fatalf("GetFloat = %v, %v", f, err)
	}
	i, err := c.GetInt(ctx, "playlist-count")
	if err != nil || i != 7 {
		t.Fatalf("GetInt = %v, %v", i, err)
	}
}

func TestCommandFailsWhenConnectionCloses(t *testing.T) {
	path := fakeServer(t, func(conn net.Conn) {
		bufio.NewReader(conn).ReadBytes('\n')
		// close without replying
	})
	c := dialFake(t, path)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := c.Command(ctx, "get_property", "x"); err == nil {
		t.Fatal("expected error after connection closed")
	}
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("Done not closed after connection loss")
	}
}

func TestCommandHonoursContext(t *testing.T) {
	path := fakeServer(t, func(conn net.Conn) {
		bufio.NewReader(conn).ReadBytes('\n')
		time.Sleep(time.Second) // never reply
	})
	c := dialFake(t, path)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Command(ctx, "get_property", "x"); err == nil {
		t.Fatal("expected context error")
	}
}

func TestObserveSendsObserveProperty(t *testing.T) {
	seen := make(chan req, 1)
	path := fakeServer(t, func(conn net.Conn) {
		sc := bufio.NewScanner(conn)
		for sc.Scan() {
			var r req
			json.Unmarshal(sc.Bytes(), &r)
			seen <- r
			conn.Write([]byte(`{"request_id":` + itoa(r.RequestID) + `,"error":"success"}` + "\n"))
		}
	})
	c := dialFake(t, path)
	if err := c.Observe(3, "pause"); err != nil {
		t.Fatal(err)
	}
	r := <-seen
	if len(r.Command) != 3 || r.Command[0] != "observe_property" || r.Command[1] != float64(3) || r.Command[2] != "pause" {
		t.Errorf("command = %v", r.Command)
	}
}
