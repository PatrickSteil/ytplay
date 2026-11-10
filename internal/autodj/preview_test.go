package autodj

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/PatrickSteil/ytplay/internal/analyze"
)

// ffmpegStretch changes the speed of x without changing its pitch, with ffmpeg's
// atempo filter.
func ffmpegStretch(x []float32, speed float64) []float32 {
	in := make([]byte, 4*len(x))
	for i, v := range x {
		binary.LittleEndian.PutUint32(in[4*i:], math.Float32bits(v))
	}
	cmd := exec.Command("ffmpeg", "-loglevel", "error", "-f", "f32le", "-ar", strconv.Itoa(analyze.SampleRate), "-ac", "1", "-i", "-",
		"-filter:a", "atempo="+strconv.FormatFloat(speed, 'f', 6, 64), "-f", "f32le", "-ar", strconv.Itoa(analyze.SampleRate), "-ac", "1", "-")
	cmd.Stdin = bytes.NewReader(in)
	out, err := cmd.Output()
	if err != nil {
		panic(fmt.Sprintf("ffmpeg: %v", err))
	}
	y := make([]float32, len(out)/4)
	for i := range y {
		y[i] = math.Float32frombits(binary.LittleEndian.Uint32(out[4*i:]))
	}
	return y
}

type previewSong struct {
	title string
	pcm   []float32
	song  Song
}

// TestPreviewTransitions writes WAVs of transitions between the test songs so
// they can be listened to. It needs the downloaded test songs, ffmpeg and
// somewhere to write:
//
//	YTPLAY_CORPUS=<cache> YTPLAY_PREVIEW_OUT=<dir> go test ./internal/autodj -run TestPreviewTransitions -v
func TestPreviewTransitions(t *testing.T) {
	dir, out := os.Getenv("YTPLAY_CORPUS"), os.Getenv("YTPLAY_PREVIEW_OUT")
	if dir == "" || out == "" {
		t.Skip("set YTPLAY_CORPUS and YTPLAY_PREVIEW_OUT")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open("../analyze/testdata/corpus.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var songs []previewSong
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		cols := strings.Split(sc.Text(), "\t")
		if len(cols) < 3 || strings.HasPrefix(cols[0], "#") || cols[1] == "0" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, cols[0]+".f32"))
		if err != nil {
			continue
		}
		pcm := make([]float32, len(raw)/4)
		for i := range pcm {
			pcm[i] = math.Float32frombits(binary.NativeEndian.Uint32(raw[4*i:]))
		}
		res, err := analyze.Analyze(pcm)
		if err != nil {
			t.Fatal(err)
		}
		songs = append(songs, previewSong{cols[2], pcm, Song{Length: float64(len(pcm)) / analyze.SampleRate, Edges: res.Edges}})
	}

	type pair struct {
		a, b int
		plan Plan
		gap  float64
	}
	var matched, plain []pair
	for i := range songs {
		for j := range songs {
			if i == j {
				continue
			}
			p := For(songs[i].song, songs[j].song)
			if p.Kind == BeatMatch {
				matched = append(matched, pair{i, j, p, math.Abs(p.Match() - 1)})
			} else {
				plain = append(plain, pair{i, j, p, 0})
			}
		}
	}
	sort.Slice(matched, func(x, y int) bool { return matched[x].gap < matched[y].gap })
	chosen := append(matched[:min(len(matched), 12)], plain[:min(len(plain), 2)]...)

	slug := func(s string) string {
		return strings.Map(func(c rune) rune {
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
				return c
			}
			return '_'
		}, s[:min(len(s), 22)])
	}
	fmt.Printf("\n%d beat-matched pairs among %d songs (listing %d, plus 2 crossfades)\n", len(matched), len(songs), len(chosen)-2)
	for n, c := range chosen {
		a, b := songs[c.a], songs[c.b]
		mix := Render(a.pcm, b.pcm, c.plan, 12, 12, ffmpegStretch)
		name := fmt.Sprintf("%02d_%s__to__%s__%s.wav", n+1, slug(a.title), slug(b.title), c.plan.Kind)
		if err := WriteWAV(filepath.Join(out, name), mix); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("%s\n    %s; the next song starts at %.1f s of the playing one, from %.1f s of its own, for %.1f s\n",
			name, c.plan.Why, c.plan.AStart, c.plan.BFrom, c.plan.Overlap)
	}
}
