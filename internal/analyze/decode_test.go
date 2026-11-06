package analyze

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestFetchRetriesFromTheBeginningWhenTheClipIsTooShort(t *testing.T) {
	// Starting at 40 s yields almost nothing (song shorter than expected);
	// starting at 0 yields 12 s of (silent) audio.
	mpv, logFile := fakeMpv(t, `
if [ "$start" = "0" ]; then head -c 1058400 /dev/zero > "$file"; else head -c 400 /dev/zero > "$file"; fi`)
	pcm, err := Fetch(context.Background(), mpv, "yt-dlp", "abc", 0) // unknown duration -> start at 40
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) != 264600 {
		t.Errorf("got %d samples", len(pcm))
	}
	log := readLog(t, logFile)
	if len(log) != 2 || !strings.Contains(log[0], "--start=40") || !strings.Contains(log[1], "--start=0") {
		t.Errorf("invocations = %q", log)
	}
}

func TestClipStart(t *testing.T) {
	tests := []struct {
		duration float64
		want     int
	}{
		{0, 40},    // unknown: assume a normal song
		{240, 40},  // normal song: skip the intro
		{110, 40},  // exactly long enough
		{90, 15},   // shorter: take the middle minute
		{60, 0},    // exactly the clip length
		{45, 0},    // shorter than the clip
		{4000, 40}, // long mix: still near the start
	}
	for _, tt := range tests {
		if got := clipStart(tt.duration); got != tt.want {
			t.Errorf("clipStart(%v) = %d, want %d", tt.duration, got, tt.want)
		}
	}
}
