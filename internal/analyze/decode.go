package analyze

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// maxSeconds is the most audio of one song that is analysed. A song fits
// whole; of a longer track (a mix, an album) the middle stretch is used.
const maxSeconds = 600

// fetchBudget is how long fetching the audio may take. A song arrives in a
// few seconds, but some streams are delivered at only about twice real time.
// The audio that has arrived when the time is up is analysed, if there is
// enough of it.
var fetchBudget = 45 * time.Second

// windowStart is where in the track to start reading: at the beginning, unless
// the track is longer than maxSeconds, then so that the middle is read.
func windowStart(duration float64) int {
	if duration <= maxSeconds {
		return 0
	}
	return int((duration - maxSeconds) / 2)
}

// decode lets mpv (already required by ytplay, and already able to play
// YouTube through yt-dlp) write length seconds of the stream, starting at
// start, as raw mono float PCM at SampleRate. This usually runs far faster
// than playback; after fetchBudget whatever has arrived is used.
func decode(ctx context.Context, mpvPath, ytdlpPath, url string, start, length int) ([]float32, error) {
	runCtx, cancel := context.WithTimeout(ctx, fetchBudget)
	defer cancel()
	// The PCM goes to stdout: no temp file to leave behind, and mpv gets
	// SIGPIPE and stops by itself if ytplay is gone.
	cmd := exec.CommandContext(runCtx, mpvPath,
		"--no-config", "--no-video", "--no-terminal", "--really-quiet", "--idle=no",
		"--ao=pcm", "--ao-pcm-file=/dev/stdout", "--ao-pcm-waveheader=no",
		"--audio-channels=mono", "--audio-samplerate="+strconv.Itoa(SampleRate), "--audio-format=float",
		"--start="+strconv.Itoa(start), "--length="+strconv.Itoa(length),
		"--script-opts=ytdl_hook-ytdl_path="+ytdlpPath, "--ytdl-format=bestaudio/best",
		url)
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		switch {
		case ctx.Err() != nil:
			return nil, fmt.Errorf("decode audio: %w", ctx.Err())
		case runCtx.Err() != nil && stdout.Len() >= minSeconds*SampleRate*4:
			// Out of time, but enough audio has arrived to be worth analysing.
		default:
			return nil, fmt.Errorf("decode audio: %w (%s)", err, strings.TrimSpace(stderr.String()))
		}
	}
	raw := stdout.Bytes()
	pcm := make([]float32, len(raw)/4)
	for i := range pcm {
		pcm[i] = math.Float32frombits(binary.NativeEndian.Uint32(raw[i*4:]))
	}
	if len(pcm) == 0 {
		return nil, errors.New("mpv produced no audio")
	}
	return pcm, nil
}

// Fetch returns the audio of a YouTube video as PCM: all of it, or the middle
// maxSeconds of a longer track. duration (seconds, 0 if unknown) decides
// which. If that part does not exist the beginning is used instead.
func Fetch(ctx context.Context, mpvPath, ytdlpPath, id string, duration float64) ([]float32, error) {
	url := "https://www.youtube.com/watch?v=" + id
	start := windowStart(duration)
	pcm, err := decode(ctx, mpvPath, ytdlpPath, url, start, maxSeconds)
	if (err != nil || len(pcm) < minSeconds*SampleRate) && start > 0 && ctx.Err() == nil {
		if retry, rerr := decode(ctx, mpvPath, ytdlpPath, url, 0, maxSeconds); rerr == nil {
			return retry, nil
		}
	}
	return pcm, err
}

// Song fetches and analyses a YouTube video.
func Song(ctx context.Context, mpvPath, ytdlpPath, id string, duration float64) (Result, error) {
	pcm, err := Fetch(ctx, mpvPath, ytdlpPath, id, duration)
	if err != nil {
		return Result{}, err
	}
	return Analyze(pcm)
}
