package cast

import (
	"context"
	"slices"
	"testing"
	"time"
)

var bg = context.Background()

func dial(t *testing.T) (*Client, *fakeDevice) {
	t.Helper()
	d := newFakeDevice(t)
	c, err := Dial(bg, d.addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c, d
}

func launched(t *testing.T) (*Client, *fakeDevice) {
	t.Helper()
	c, d := dial(t)
	if err := c.Launch(bg); err != nil {
		t.Fatal(err)
	}
	return c, d
}

func next(t *testing.T, c *Client, kind EventKind) Event {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-c.Events():
			if ev.Kind == kind {
				return ev
			}
		case <-deadline:
			t.Fatalf("no %v event", kind)
		}
	}
}

func TestDialConnectsToTheReceiver(t *testing.T) {
	_, d := dial(t)
	waitFor(t, func() bool { return slices.Contains(d.sentKinds(), "tp.connection:CONNECT") })
	if g, _ := d.last("CONNECT"); g.Dest != "receiver-0" {
		t.Errorf("connected to %q", g.Dest)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestLaunchStartsTheDefaultMediaReceiverAndConnectsToIt(t *testing.T) {
	_, d := launched(t)
	if g, ok := d.last("LAUNCH"); !ok || g.Payload["appId"] != defaultReceiver {
		t.Errorf("launch: %+v", g)
	}
	waitFor(t, func() bool { g, _ := d.last("CONNECT"); return g.Dest == "tr-1" }) // the app's transport

}

func TestLaunchConnectsToTheMediaPlayerNotToAnotherApp(t *testing.T) {
	d := newFakeDevice(t)
	d.otherApp = true
	c, err := Dial(bg, d.addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Launch(bg); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { g, _ := d.last("CONNECT"); return g.Dest == "tr-1" })
}

func TestLaunchGivesUpWhenTheAppNeverComesUp(t *testing.T) {
	old := launchTimeout
	launchTimeout = 300 * time.Millisecond
	defer func() { launchTimeout = old }()
	d := newFakeDevice(t)
	d.noTransport = true
	c, err := Dial(bg, d.addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Launch(bg); err == nil {
		t.Error("no error")
	}
}

func TestLoadPlaysAURL(t *testing.T) {
	c, d := launched(t)
	if err := c.Load(bg, "http://example.com/a.m4a", "audio/mp4", "Title", "Artist"); err != nil {
		t.Fatal(err)
	}
	g, _ := d.last("LOAD")
	media, _ := g.Payload["media"].(map[string]any)
	if media["contentId"] != "http://example.com/a.m4a" || media["contentType"] != "audio/mp4" || g.Payload["autoplay"] != true || g.Dest != "tr-1" {
		t.Errorf("load: %+v", g)
	}
	if media["streamType"] != "BUFFERED" {
		t.Errorf("stream type %v", media["streamType"])
	}
	if md, _ := media["metadata"].(map[string]any); md["title"] != "Title" || md["artist"] != "Artist" {
		t.Errorf("metadata %+v", media["metadata"])
	}
	ev := next(t, c, MediaEvent)
	if ev.State != "PLAYING" || ev.Duration != 200 {
		t.Errorf("event %+v", ev)
	}
}

func TestALoadTheDeviceRefusesIsAnError(t *testing.T) {
	c, d := launched(t)
	d.loadFails = true
	if err := c.Load(bg, "http://example.com/a.m4a", "audio/mp4", "", ""); err == nil {
		t.Error("no error")
	}
}

func TestLoadBeforeLaunchIsAnError(t *testing.T) {
	c, _ := dial(t)
	if err := c.Load(bg, "http://example.com/a", "audio/mp4", "", ""); err == nil {
		t.Error("no error")
	}
}

func TestPauseResumeAndSeek(t *testing.T) {
	c, d := launched(t)
	_ = c.Load(bg, "http://example.com/a", "audio/mp4", "", "")
	if err := c.Pause(bg); err != nil {
		t.Fatal(err)
	}
	if _, _, state := c.Position(); state != "PAUSED" {
		t.Errorf("state %q after pause", state)
	}
	if err := c.Play(bg); err != nil {
		t.Fatal(err)
	}
	if err := c.Seek(bg, 42.5); err != nil {
		t.Fatal(err)
	}
	if g, _ := d.last("SEEK"); g.Payload["currentTime"] != 42.5 || g.Payload["mediaSessionId"] != float64(1) {
		t.Errorf("seek %+v", g.Payload)
	}
	if pos, _, state := c.Position(); state != "PLAYING" || pos < 42.5 || pos > 44 {
		t.Errorf("position %v state %q", pos, state)
	}
}

func TestPositionRunsOnWhileItPlays(t *testing.T) {
	c, _ := launched(t)
	_ = c.Load(bg, "http://example.com/a", "audio/mp4", "", "")
	t0 := time.Now()
	c.now = func() time.Time { return t0.Add(10 * time.Second) }
	c.mu.Lock()
	c.at = t0
	c.mu.Unlock()
	if pos, _, _ := c.Position(); pos < 9.9 || pos > 10.1 {
		t.Errorf("position %v, want about 10", pos)
	}
}

func TestVolumeAndMute(t *testing.T) {
	c, d := launched(t)
	if err := c.SetVolume(bg, 0.25); err != nil {
		t.Fatal(err)
	}
	if err := c.SetMuted(bg, true); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	vol, muted := d.volume, d.muted
	d.mu.Unlock()
	if vol != 0.25 || !muted {
		t.Errorf("volume %v muted %v", vol, muted)
	}
	_ = c.SetVolume(bg, 7)
	d.mu.Lock()
	vol = d.volume
	d.mu.Unlock()
	if vol != 1 {
		t.Errorf("a volume of 7 reached the device as %v, want it held at 1", vol)
	}
	for ev := next(t, c, VolumeEvent); ev.Volume != 0.25 || !ev.Muted; ev = next(t, c, VolumeEvent) {
		if ev.Kind != VolumeEvent {
			t.Fatalf("event %+v", ev)
		}
	}
}

func TestASongEndingByItselfIsAnEvent(t *testing.T) {
	c, d := launched(t)
	_ = c.Load(bg, "http://example.com/a", "audio/mp4", "", "")
	next(t, c, MediaEvent)
	d.finish("FINISHED")
	ev := next(t, c, MediaEvent)
	if ev.State != "IDLE" || ev.IdleReason != "FINISHED" {
		t.Errorf("event %+v", ev)
	}
}

func TestTheConnectionDroppingIsAnEvent(t *testing.T) {
	c, d := launched(t)
	d.mu.Lock()
	d.conn.Close()
	d.mu.Unlock()
	next(t, c, ClosedEvent)
	if err := c.Pause(bg); err == nil {
		t.Error("a command on a dead connection succeeded")
	}
}

func TestHeartbeats(t *testing.T) {
	old := heartbeatEvery
	heartbeatEvery = 20 * time.Millisecond
	defer func() { heartbeatEvery = old }()
	_, d := dial(t)
	waitFor(t, func() bool { return slices.Contains(d.sentKinds(), "tp.heartbeat:PING") })
	// and a ping from the device is answered
	d.mu.Lock()
	d.reply(d.conn, "receiver-0", nsHeartbeat, map[string]any{"type": "PING"})
	d.mu.Unlock()
	waitFor(t, func() bool { return slices.Contains(d.sentKinds(), "tp.heartbeat:PONG") })
	if g, _ := d.last("PONG"); g.Dest != "receiver-0" {
		t.Errorf("the pong went to %q", g.Dest)
	}
}

func TestStopEndsTheApp(t *testing.T) {
	c, d := launched(t)
	if err := c.Stop(bg); err != nil {
		t.Fatal(err)
	}
	if g, ok := d.last("STOP"); !ok || g.Payload["sessionId"] != "s-1" || g.Dest != "receiver-0" {
		t.Errorf("stop %+v", g)
	}
}
