package analyze

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Measurements, not checks: they only run when asked for, and print how long
// analysing a queue of real songs takes.
//
//	YTPLAY_CORPUS=<cache dir> [YTPLAY_SONGS_AT_ONCE=n] [YTPLAY_POOL=workers] [YTPLAY_LEVEL=light] go test ./internal/analyze -run QueueTiming -v
//	YTPLAY_CORPUS=<cache dir> YTPLAY_FETCH_TIMING=1 go test ./internal/analyze -run FetchTiming -v

// songsAtOnce is how many songs are analysed at the same time (the app does two
// today).
func songsAtOnce() int {
	if n, err := strconv.Atoi(os.Getenv("YTPLAY_SONGS_AT_ONCE")); err == nil && n > 0 {
		return n
	}
	return 2
}

// TestQueueTiming analyses every cached song of the corpus, songsAtOnce at a
// time, and says how long the whole set took. YTPLAY_POOL=n shares a pool of n
// workers between the songs (what the app does in DJ mode), YTPLAY_LEVEL=light
// asks for tempo and key only.
func TestQueueTiming(t *testing.T) {
	dir := os.Getenv("YTPLAY_CORPUS")
	if dir == "" {
		t.Skip("set YTPLAY_CORPUS=<cache dir> to time the analysis of real songs")
	}
	var ids []string
	for _, e := range readCorpus(t) {
		if _, err := os.Stat(filepath.Join(dir, e.id+".f32")); err == nil {
			ids = append(ids, e.id)
		}
	}
	if len(ids) == 0 {
		t.Skip("no cached songs in " + dir)
	}
	n := songsAtOnce()
	var opts Options
	if w, err := strconv.Atoi(os.Getenv("YTPLAY_POOL")); err == nil && w > 0 {
		opts.Pool = NewPool(w)
	}
	if os.Getenv("YTPLAY_LEVEL") == "light" {
		opts.Level = Light
	}
	jobs := make(chan string)
	var mu sync.Mutex
	var audio, compute time.Duration // seconds of music, and time spent analysing it (summed over songs)
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < n; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				pcm, err := loadPCM(filepath.Join(dir, id+".f32"))
				if err != nil {
					t.Errorf("load %s: %v", id, err)
					continue
				}
				t0 := time.Now()
				if _, err := AnalyzeWith(pcm, opts); err != nil {
					t.Errorf("%s: %v", id, err)
				}
				took := time.Since(t0)
				mu.Lock()
				audio += time.Duration(len(pcm)) * time.Second / SampleRate
				compute += took
				mu.Unlock()
			}
		}()
	}
	for _, id := range ids {
		jobs <- id
	}
	close(jobs)
	wg.Wait()
	wall := time.Since(start)
	t.Logf("%d songs, %d at a time, pool %v, level %v: %.1f s wall; %.0f s of music analysed in %.1f s of compute (%.1f s of music per second of compute, %.2f s compute per song)",
		len(ids), n, os.Getenv("YTPLAY_POOL"), opts.Level, wall.Seconds(), audio.Seconds(), compute.Seconds(), audio.Seconds()/compute.Seconds(), compute.Seconds()/float64(len(ids)))
}

// TestFetchTiming fetches the first five songs of the corpus one after the
// other, the way the app does for each song, and says how long each took.
func TestFetchTiming(t *testing.T) {
	if os.Getenv("YTPLAY_FETCH_TIMING") != "1" {
		t.Skip("set YTPLAY_FETCH_TIMING=1 to time fetching real songs (needs the network)")
	}
	mpv, err := exec.LookPath("mpv")
	if err != nil {
		t.Skip("mpv not found")
	}
	ytdlp, err := exec.LookPath("yt-dlp")
	if err != nil {
		t.Skip("yt-dlp not found")
	}
	entries := readCorpus(t)
	if len(entries) > 5 {
		entries = entries[:5]
	}
	var total time.Duration
	for _, e := range entries {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		t0 := time.Now()
		pcm, err := Fetch(ctx, mpv, ytdlp, e.id, 0)
		cancel()
		took := time.Since(t0)
		if err != nil {
			t.Errorf("fetch %s (%s): %v", e.id, e.title, err)
			continue
		}
		total += took
		t.Logf("%-50s fetched %3.0f s of music in %4.1f s", e.title, float64(len(pcm))/SampleRate, took.Seconds())
	}
	t.Logf("mean %.1f s per song", total.Seconds()/float64(len(entries)))
}
