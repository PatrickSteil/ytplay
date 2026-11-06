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
)

const clipSeconds = 60

// clipStart picks where in the song to take the analysed minute from: past
// the intro for normal songs, the middle for shorter ones.
func clipStart(duration float64) int {
	switch {
	case duration <= 0 || duration >= 40+clipSeconds+10:
		return 40
	case duration > clipSeconds:
		return int((duration - clipSeconds) / 2)
	}
	return 0
}

// decode lets mpv (already required by ytplay, and already able to play
// YouTube through yt-dlp) write length seconds of the stream, starting at
// start, as raw mono float PCM at SampleRate. This runs faster than playback.
func decode(ctx context.Context, mpvPath, ytdlpPath, url string, start, length int) ([]float32, error) {
	// The PCM goes to stdout: no temp file to leave behind, and mpv gets
	// SIGPIPE and stops by itself if ytplay is gone.
	cmd := exec.CommandContext(ctx, mpvPath,
		"--no-config", "--no-video", "--no-terminal", "--really-quiet", "--idle=no",
		"--ao=pcm", "--ao-pcm-file=/dev/stdout", "--ao-pcm-waveheader=no",
		"--audio-channels=mono", "--audio-samplerate="+strconv.Itoa(SampleRate), "--audio-format=float",
		"--start="+strconv.Itoa(start), "--length="+strconv.Itoa(length),
		"--script-opts=ytdl_hook-ytdl_path="+ytdlpPath, "--ytdl-format=bestaudio/best",
		url)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("decode audio: %w", ctx.Err())
		}
		return nil, fmt.Errorf("decode audio: %w (%s)", err, strings.TrimSpace(stderr.String()))
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

// Fetch returns about a minute of audio of a YouTube video as PCM. duration
// (seconds, 0 if unknown) decides which minute. If that part does not exist
// the beginning of the song is used instead.
func Fetch(ctx context.Context, mpvPath, ytdlpPath, id string, duration float64) ([]float32, error) {
	url := "https://www.youtube.com/watch?v=" + id
	start := clipStart(duration)
	pcm, err := decode(ctx, mpvPath, ytdlpPath, url, start, clipSeconds)
	if (err != nil || len(pcm) < minSeconds*SampleRate) && start > 0 && ctx.Err() == nil {
		if retry, rerr := decode(ctx, mpvPath, ytdlpPath, url, 0, clipSeconds); rerr == nil {
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
