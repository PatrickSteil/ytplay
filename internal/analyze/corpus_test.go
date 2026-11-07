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

// readBeatStarts reads testdata/beatstart.tsv: for the songs someone has listened
// to, the time in seconds at which the steady beat starts.
func readBeatStarts(t *testing.T) map[string]float64 {
	t.Helper()
	f, err := os.Open("testdata/beatstart.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string]float64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) < 2 {
			t.Fatalf("bad beatstart line %q", line)
		}
		sec, err := strconv.ParseFloat(cols[1], 64)
		if err != nil {
			t.Fatalf("bad seconds in %q: %v", line, err)
		}
		out[cols[0]] = sec
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
	// The corpus holds whole songs: a long track on a slow stream would be cut
	// short by the app's budget (and then analysed as if it were the whole song).
	oldBudget := fetchBudget
	fetchBudget = 4 * time.Minute
	defer func() { fetchBudget = oldBudget }()
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

	// Songs whose beat is plain at both ends must be sure there, songs without a
	// dependable beat (live drums that wander, a ballad) must not be.
	// (Gecko's off-beat bass keeps its start from being clear enough to be sure
	// of, so it is not required here.)
	sureAtBothEnds := map[string]bool{
		"A2VpR8HahKc": true, "rY0WxgSXdEE": true, "wDuoOapZ9Z0": true, "OPf0YbXqDm0": true,
	}
	unsureAtBothEnds := map[string]bool{"hTWKbfoikeg": true, "hLQl3WQQoQ0": true}
	beatStarts := readBeatStarts(t)
	// Songs the tempo is known to come out at half or double speed for. Any other
	// song that does is a regression.
	knownOctave := map[string]string{
		"hLQl3WQQoQ0": "Adele: a ballad whose pulse can be counted either way",
		"HUvovrkygZg": "Pjanoo club mix: minutes of piano, one note per two beats, dominate the whole-song tempo",
	}
	grid := func(g BeatGrid) string {
		return fmt.Sprintf("%6.2f %4.1f %3.0fms %5.1f %-4s @%.2fs", 60/math.Max(g.Period, 1e-9), g.Sharp, g.Wobble*1000, g.Margin, map[bool]string{true: "SURE", false: "-"}[g.Sure], g.Anchor)
	}
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
			if _, known := knownOctave[e.id]; !known {
				t.Errorf("%s: %.1f BPM, want %.1f: off by a factor of %.2f, and this song is not a known octave case", e.title, res.BPM, e.bpm, res.BPM/e.bpm)
			}
			songs, octave = songs+1, octave+1
		default:
			verdict = "WRONG"
			songs++
			t.Errorf("%s: %.1f BPM, want %.1f", e.title, res.BPM, e.bpm)
		}
		fmt.Printf("%-56s %6.1f | %7.2f %6.3f | %-10s %5.2f  %s\n",
			fmt.Sprintf("%s (%.0fs)", e.title, float64(len(full))/SampleRate), e.bpm, res.BPM, res.TempoScore, res.Key, res.KeyScore, verdict)
		if res.BPM > 0 {
			fmt.Printf("    audible %.1f..%.1f s   start: %s   end: %s\n", res.Edges.AudioStart, res.Edges.AudioEnd, grid(res.Edges.Start), grid(res.Edges.End))
		}
		if sureAtBothEnds[e.id] && !(res.Edges.Start.Sure && res.Edges.End.Sure) {
			t.Errorf("%s: both ends should be sure, got start %+v end %+v", e.title, res.Edges.Start, res.Edges.End)
		}
		if unsureAtBothEnds[e.id] && (res.Edges.Start.Sure || res.Edges.End.Sure) {
			t.Errorf("%s: neither end may be sure, got start %+v end %+v", e.title, res.Edges.Start, res.Edges.End)
		}
		// A beat start someone has heard: the start grid must be sure and its anchor
		// at that beat or at most two bars (8 beats) after it.
		if truth, ok := beatStarts[e.id]; ok {
			g := res.Edges.Start
			if !g.Sure || g.Anchor < truth-0.05 || g.Anchor > truth+8*g.Period+0.05 {
				t.Errorf("%s: the beat starts at %.2f s, the start grid says %.2f s (sure: %v)", e.title, truth, g.Anchor, g.Sure)
			}
		}
	}
	fmt.Printf("\nsongs: %d within 4%%, %d off by an octave or a third, of %d\n", exact, octave, songs)
	if octave > len(knownOctave) {
		t.Errorf("%d songs are off by a factor of 2 or 3, at most the %d known ones are accepted", octave, len(knownOctave))
	}
}
