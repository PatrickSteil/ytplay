package player

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/PatrickSteil/ytplay/internal/mpv"
)

// These tests drive the Player on real mpv processes playing generated tones
// (silently, --ao=null). They skip when mpv or ffmpeg is missing.

func needTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"mpv", "ffmpeg"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
}

// tone writes a sine tone of the given length and returns its path.
func tone(t *testing.T, dir, name string, seconds float64) string {
	t.Helper()
	path := filepath.Join(dir, name)
	out, err := exec.Command("ffmpeg", "-loglevel", "error", "-y", "-f", "lavfi",
		"-i", "sine=frequency=440:duration="+strconv.FormatFloat(seconds, 'f', 2, 64), path).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	return path
}

// realDeck starts an mpv that plays silently.
func realDeck(ctx context.Context, name string) (Deck, error) {
	c, err := mpv.Start(ctx, "yt-dlp", name, "--ao=null")
	if err != nil {
		return nil, err
	}
	if err := c.ObserveAll(); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func TestPlayerPlaysTheQueueRealMpv(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	first, second := tone(t, dir, "a.wav", 1.2), tone(t, dir, "b.wav", 1.2)
	p, err := New(context.Background(), realDeck)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	if err := p.QueueMany(bg, []string{first, second}); err != nil {
		t.Fatal(err)
	}

	var positions []string
	timeout := time.After(10 * time.Second)
	for len(positions) < 3 {
		select {
		case ev := <-p.Events():
			if ev.Property == "playlist-pos" {
				positions = append(positions, string(ev.Data))
			}
		case <-timeout:
			t.Fatalf("playlist-pos events so far: %v, want 0, 1, null", positions)
		}
	}
	eq(t, "playlist-pos", positions, []string{"0", "1", "null"})
}

// TestGaplessHandoffRealMpv plays two 3 s tones. The second must be loaded on
// the second deck before the first ends, and must be audible within 300 ms of
// the player announcing it. (mpv's own time-pos stands still for a few hundred
// milliseconds while a song's last audio drains, so "the longest time without
// progress" would measure mpv, not the handoff.)
func TestGaplessHandoffRealMpv(t *testing.T) {
	needTools(t)
	oldLead, oldPoll := preloadLead, pollEvery
	preloadLead, pollEvery = 20*time.Second, 100*time.Millisecond // preload as soon as the first song plays
	t.Cleanup(func() { preloadLead, pollEvery = oldLead, oldPoll })

	dir := t.TempDir()
	first, second := tone(t, dir, "a.wav", 3), tone(t, dir, "b.wav", 3)
	p, err := New(context.Background(), realDeck)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	if err := p.QueueMany(bg, []string{first, second}); err != nil {
		t.Fatal(err)
	}

	var handedOff, audible time.Time
	sawPreload := false
	deadline := time.After(20 * time.Second)
	for idle := false; !idle; {
		select {
		case <-deadline:
			t.Fatal("the queue did not finish in 20 s")
		case ev := <-p.Events():
			if ev.Property == "playlist-pos" && string(ev.Data) == "1" {
				handedOff = time.Now()
			}
			idle = ev.Property == "idle-active" && string(ev.Data) == "true"
		case <-time.After(10 * time.Millisecond):
		}
		p.mu.Lock()
		sawPreload = sawPreload || p.pre != nil
		p.mu.Unlock()
		if !handedOff.IsZero() && audible.IsZero() {
			if e, _, err := p.Position(bg); err == nil && e > 0.02 {
				audible = time.Now()
			}
		}
	}
	if !sawPreload {
		t.Error("the second song was not preloaded before the first ended")
	}
	p.mu.Lock()
	onSecondDeck := p.active == 1
	p.mu.Unlock()
	if !onSecondDeck {
		t.Error("the second song did not play on the second deck")
	}
	if handedOff.IsZero() || audible.IsZero() {
		t.Fatal("never saw the second song start")
	}
	if gap := audible.Sub(handedOff); gap > 300*time.Millisecond {
		t.Errorf("the second song became audible %v after the handoff, want under 300 ms", gap)
	}
}
