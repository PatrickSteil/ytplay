package player

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestFarSeekPreloadRealYouTube measures what a beat-matched transition into a
// song whose beat starts late asks of a deck: the next song is loaded paused and
// then seeked to where its beat starts. Seeking 60 to 140 s into a YouTube stream
// is not free. A measurement, not a check; it needs the network:
//
//	YTPLAY_NETWORK=1 go test ./internal/player -run FarSeekPreload -v
func TestFarSeekPreloadRealYouTube(t *testing.T) {
	if os.Getenv("YTPLAY_NETWORK") != "1" {
		t.Skip("set YTPLAY_NETWORK=1 to time seeking into a real YouTube stream")
	}
	needTools(t)
	const url = "https://www.youtube.com/watch?v=wDuoOapZ9Z0" // Avicii - Levels, 339 s
	for _, at := range []float64{0, 60, 100, 140} {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		d, err := realDeck(ctx, "farseek")
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if _, err := d.Command(ctx, "set_property", "pause", true); err != nil {
			t.Fatal(err)
		}
		t0 := time.Now()
		if _, err := load(ctx, d, url); err != nil {
			t.Fatal(err)
		}
		var loaded, landed time.Duration
		for ctx.Err() == nil {
			if dur, err := d.GetFloat(ctx, "duration"); err == nil && dur > 0 {
				loaded = time.Since(t0)
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		if at > 0 {
			if _, err := d.Command(ctx, "seek", at, "absolute", "exact"); err != nil {
				t.Fatal(err)
			}
		}
		// A paused deck reports the seek target as its position at once, before any
		// audio is buffered there; the deck is ready when it has stopped seeking.
		for ctx.Err() == nil {
			if raw, err := d.Command(ctx, "get_property", "seeking"); err == nil && string(raw) == "false" {
				landed = time.Since(t0)
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		if ctx.Err() != nil {
			t.Errorf("seek to %3.0f s: not there after %v", at, 90*time.Second)
		} else {
			ahead, _ := d.GetFloat(ctx, "demuxer-cache-time")
			t.Logf("seek to %3.0f s: loaded (duration known) after %5.2f s, stopped seeking after %5.2f s, cache reaches %.0f s", at, loaded.Seconds(), landed.Seconds(), ahead)
		}
		_ = d.Close()
		cancel()
	}
}
