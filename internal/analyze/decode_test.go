package analyze

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeMpv writes a shell script that logs its arguments (one invocation per
// line) and then runs body, which can use $file (the --ao-pcm-file target)
// and $start.
func fakeMpv(t *testing.T, body string) (path, logFile string) {
	t.Helper()
	dir := t.TempDir()
	logFile = filepath.Join(dir, "log")
	script := `#!/bin/sh
echo "$*" >> ` + logFile + `
for a in "$@"; do
  case "$a" in
    --ao-pcm-file=*) file="${a#--ao-pcm-file=}" ;;
    --start=*) start="${a#--start=}" ;;
  esac
done
` + body
	path = filepath.Join(dir, "mpv")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, logFile
}

func readLog(t *testing.T, logFile string) []string {
	t.Helper()
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestDecodeReadsFloatSamplesAndPassesTheRightFlags(t *testing.T) {
	// Three little-endian float32 samples of 1.0 (bytes 00 00 80 3f).
	mpv, logFile := fakeMpv(t, `printf '\000\000\200\077\000\000\200\077\000\000\200\077' > "$file"`)
	pcm, err := decode(context.Background(), mpv, "/usr/bin/yt-dlp", "https://www.youtube.com/watch?v=abc", 40, 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) != 3 || pcm[0] != 1 || pcm[2] != 1 {
		t.Errorf("pcm = %v", pcm)
	}
	args := readLog(t, logFile)[0]
	for _, want := range []string{
		"--no-config", "--no-video", "--ao=pcm", "--ao-pcm-file=/dev/stdout", "--ao-pcm-waveheader=no", "--audio-channels=mono",
		"--audio-samplerate=22050", "--audio-format=float", "--start=40", "--length=60",
		"--script-opts=ytdl_hook-ytdl_path=/usr/bin/yt-dlp", "--ytdl-format=bestaudio/best",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %s in %q", want, args)
		}
	}
	if !strings.HasSuffix(args, "https://www.youtube.com/watch?v=abc") {
		t.Errorf("url must be the last argument: %q", args)
	}
}

func TestDecodeFailsWhenMpvFails(t *testing.T) {
	mpv, _ := fakeMpv(t, `echo "boom" >&2; exit 2`)
	if _, err := decode(context.Background(), mpv, "yt-dlp", "u", 0, 60); err == nil {
		t.Error("expected an error")
	}
	mpv, _ = fakeMpv(t, `exit 0`) // success but no output file
	if _, err := decode(context.Background(), mpv, "yt-dlp", "u", 0, 60); err == nil {
		t.Error("expected an error when mpv produced no audio")
	}
}

func TestDecodeStopsWhenContextIsCancelled(t *testing.T) {
	mpv, _ := fakeMpv(t, `exec sleep 5`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := decode(ctx, mpv, "yt-dlp", "u", 0, 60); err == nil {
		t.Error("expected an error")
	}
}

func TestFetchTakesTheWholeSong(t *testing.T) {
	for _, tt := range []struct {
		duration float64
		start    string
	}{
		{0, "--start=0 "},     // unknown length
		{240, "--start=0 "},   // a normal song: from the very beginning
		{600, "--start=0 "},   // exactly the longest stretch
		{2000, "--start=700"}, // a long mix: the middle stretch
	} {
		mpv, logFile := fakeMpv(t, `head -c 1058400 /dev/zero > "$file"`)
		if _, err := Fetch(context.Background(), mpv, "yt-dlp", "abc", tt.duration); err != nil {
			t.Fatal(err)
		}
		args := readLog(t, logFile)[0] + " "
		if !strings.Contains(args, strings.TrimSpace(tt.start)+" ") || !strings.Contains(args, "--length=600 ") {
			t.Errorf("duration %v: invocation = %q, want %s and --length=600", tt.duration, args, tt.start)
		}
	}
}

func TestFetchRetriesFromTheBeginningWhenTheMiddleIsMissing(t *testing.T) {
	// The long mix is shorter than announced: the middle yields almost
	// nothing, the beginning yields 12 s of (silent) audio.
	mpv, logFile := fakeMpv(t, `
if [ "$start" = "0" ]; then head -c 1058400 /dev/zero > "$file"; else head -c 400 /dev/zero > "$file"; fi`)
	pcm, err := Fetch(context.Background(), mpv, "yt-dlp", "abc", 2000)
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) != 264600 {
		t.Errorf("got %d samples", len(pcm))
	}
	log := readLog(t, logFile)
	if len(log) != 2 || !strings.Contains(log[0], "--start=700") || !strings.Contains(log[1], "--start=0") {
		t.Errorf("invocations = %q", log)
	}
}

func TestWindowStart(t *testing.T) {
	tests := []struct {
		duration float64
		want     int
	}{
		{0, 0},      // unknown: from the beginning
		{240, 0},    // a normal song
		{600, 0},    // exactly the longest stretch
		{601, 0},    // 0.5 s off the middle rounds down
		{1200, 300}, // a long track: the middle ten minutes
		{4000, 1700},
	}
	for _, tt := range tests {
		if got := windowStart(tt.duration); got != tt.want {
			t.Errorf("windowStart(%v) = %d, want %d", tt.duration, got, tt.want)
		}
	}
}

func withFetchBudget(t *testing.T, d time.Duration) {
	t.Helper()
	old := fetchBudget
	fetchBudget = d
	t.Cleanup(func() { fetchBudget = old })
}

func TestDecodeKeepsTheAudioThatArrivedWithinTheBudget(t *testing.T) {
	// A slow stream: 12 s of audio arrive, then nothing for a long time.
	withFetchBudget(t, 300*time.Millisecond)
	mpv, _ := fakeMpv(t, `head -c 1058400 /dev/zero > "$file"; exec sleep 5`)
	start := time.Now()
	pcm, err := decode(context.Background(), mpv, "yt-dlp", "u", 0, 600)
	if err != nil {
		t.Fatalf("the audio that arrived should be used, got %v", err)
	}
	if len(pcm) != 264600 {
		t.Errorf("got %d samples", len(pcm))
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("took %v, the budget was not enforced", took)
	}
}

func TestDecodeFailsWhenTooLittleArrivedWithinTheBudget(t *testing.T) {
	withFetchBudget(t, 300*time.Millisecond)
	mpv, _ := fakeMpv(t, `head -c 400 /dev/zero > "$file"; exec sleep 5`)
	if _, err := decode(context.Background(), mpv, "yt-dlp", "u", 0, 600); err == nil {
		t.Error("a second of audio is not enough to analyse")
	}
}
