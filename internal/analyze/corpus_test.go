package analyze

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type corpusEntry struct {
	id    string
	bpm   float64
	title string
}

func readCorpus(t *testing.T) []corpusEntry {
	t.Helper()
	f, err := os.Open("testdata/corpus.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []corpusEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) < 3 {
			t.Fatalf("bad corpus line %q", line)
		}
		bpm, err := strconv.ParseFloat(cols[1], 64)
		if err != nil {
			t.Fatalf("bad BPM in %q: %v", line, err)
		}
		out = append(out, corpusEntry{cols[0], bpm, cols[2]})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func loadPCM(path string) ([]float32, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pcm := make([]float32, len(raw)/4)
	for i := range pcm {
		pcm[i] = math.Float32frombits(binary.NativeEndian.Uint32(raw[i*4:]))
	}
	return pcm, nil
}

func savePCM(path string, pcm []float32) error {
	raw := make([]byte, 4*len(pcm))
	for i, v := range pcm {
		binary.NativeEndian.PutUint32(raw[i*4:], math.Float32bits(v))
	}
	return os.WriteFile(path, raw, 0o644)
}

// fetchMissing downloads (in parallel) the songs that are not cached yet.
func fetchMissing(t *testing.T, dir string, entries []corpusEntry) {
	mpv, err := exec.LookPath("mpv")
	if err != nil {
		t.Skip("mpv not found")
	}
	ytdlp, err := exec.LookPath("yt-dlp")
	if err != nil {
		t.Skip("yt-dlp not found")
	}
	var wg sync.WaitGroup
	for _, e := range entries {
		path := filepath.Join(dir, e.id+".f32")
		if _, err := os.Stat(path); err == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			pcm, err := Fetch(ctx, mpv, ytdlp, e.id, 0)
			if err != nil {
				t.Errorf("fetch %s (%s): %v", e.id, e.title, err)
				return
			}
			if err := savePCM(path, pcm); err != nil {
				t.Errorf("cache %s: %v", e.id, err)
			}
		}()
	}
	wg.Wait()
}

// within reports whether est is within pct percent of want.
func within(est, want, pct float64) bool {
	return est > 0 && math.Abs(est-want) <= pct/100*want
}

// octaveOK is the usual "tempo accuracy 2" test: right up to a factor of 2 or 3.
func octaveOK(est, want, pct float64) bool {
	for _, f := range [...]float64{1, 2, 0.5, 3, 1.0 / 3} {
		if within(est, want*f, pct) {
			return true
		}
	}
	return false
}

// TestCorpus scores the tempo detector on real songs. It needs the network
// and mpv/yt-dlp, so it only runs when YTPLAY_CORPUS names a cache directory.
//
// A song may be off by a factor of 2 or 3 (the usual ambiguity, e.g. a ballad
// counted in eighths), but at most one of the songs may be, and everything
// else must be within 4%. Audio that is not a song must report no tempo.
func TestCorpus(t *testing.T) {
	dir := os.Getenv("YTPLAY_CORPUS")
	if dir == "" {
		t.Skip("set YTPLAY_CORPUS=<cache dir> to run against real songs")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	entries := readCorpus(t)
	fetchMissing(t, dir, entries)

	fmt.Printf("\n%-56s %6s | %7s %6s | %-10s %5s\n", "song", "truth", "BPM", "beat", "key", "score")
	songs, exact, octave := 0, 0, 0
	for _, e := range entries {
		full, err := loadPCM(filepath.Join(dir, e.id+".f32"))
		if err != nil {
			t.Errorf("load %s: %v", e.id, err)
			continue
		}
		res, err := Analyze(full)
		if err != nil {
			t.Errorf("%s: %v", e.title, err)
			continue
		}
		verdict := ""
		switch {
		case e.bpm == 0 && res.BPM != 0:
			verdict = "WRONG: should have no tempo"
			t.Errorf("%s: reported %.1f BPM, want none", e.title, res.BPM)
		case e.bpm == 0:
			verdict = "ok (none)"
		case within(res.BPM, e.bpm, 4):
			verdict = "ok"
			songs, exact = songs+1, exact+1
		case octaveOK(res.BPM, e.bpm, 4):
			verdict = fmt.Sprintf("x%.2f", res.BPM/e.bpm)
			songs, octave = songs+1, octave+1
		default:
			verdict = "WRONG"
			songs++
			t.Errorf("%s: %.1f BPM, want %.1f", e.title, res.BPM, e.bpm)
		}
		fmt.Printf("%-56s %6.1f | %7.2f %6.3f | %-10s %5.2f  %s\n",
			fmt.Sprintf("%s (%.0fs)", e.title, float64(len(full))/SampleRate), e.bpm, res.BPM, res.TempoScore, res.Key, res.KeyScore, verdict)
	}
	fmt.Printf("\nsongs: %d within 4%%, %d off by an octave or a third, of %d\n", exact, octave, songs)
	if octave > 1 {
		t.Errorf("%d songs are off by a factor of 2 or 3, at most 1 is accepted", octave)
	}
}
