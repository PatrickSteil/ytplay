package analyze

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/PatrickSteil/ytplay/internal/yt"
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
	pcm, _, err := fetch(ctx, mpvPath, ytdlpPath, id, duration)
	return pcm, err
}

// fetch is Fetch, and also says how many seconds into the song the audio starts:
// where the middle begins, or 0 if the beginning was used instead.
func fetch(ctx context.Context, mpvPath, ytdlpPath, id string, duration float64) ([]float32, int, error) {
	url := yt.URLOf(id)
	if url == "" {
		return nil, 0, fmt.Errorf("not a song: %q", id)
	}
	start := windowStart(duration)
	pcm, err := decode(ctx, mpvPath, ytdlpPath, url, start, maxSeconds)
	if (err != nil || len(pcm) < minSeconds*SampleRate) && start > 0 && ctx.Err() == nil {
		if retry, rerr := decode(ctx, mpvPath, ytdlpPath, url, 0, maxSeconds); rerr == nil {
			return retry, 0, nil
		}
	}
	return pcm, start, err
}

// Excerpt is the part of a song that the audio is: Offset seconds into a song
// that is Duration seconds long (0 if unknown), as many seconds as the audio
// has. A song is analysed from an excerpt when only part of it arrived in time,
// or when it is longer than maxSeconds and only its middle is read.
type Excerpt struct {
	Offset, Duration float64
}

// wholeSlack is how much shorter than the song's duration the audio may be and
// still count as all of it (durations are whole seconds, decoding rounds).
const wholeSlack = 3.0

// analyzeExcerpt analyses pcm, which is an excerpt of a song, and keeps of the
// edges only what the excerpt can tell about the song. Edges are times in the
// song: the end of an excerpt is not the end of the song (the planner would cut
// the song off there), and in the middle of a long track nothing is where it
// seems. Tempo and key are those of the excerpt.
func analyzeExcerpt(pcm []float32, x Excerpt, o Options) (Result, error) {
	res, err := AnalyzeWith(pcm, o)
	if err != nil {
		return res, err
	}
	res.Wave.Offset = x.Offset
	if res.Level == Light {
		return res, nil
	}
	seconds := float64(len(pcm)) / SampleRate
	switch {
	case x.Offset > 0:
		res.Edges = Edges{} // times in the excerpt are not times in the song
	case x.Duration > 0 && seconds < x.Duration-wholeSlack:
		// The beginning is the song's, the end is not.
		res.Edges = Edges{AudioStart: res.Edges.AudioStart, Start: res.Edges.Start}
	}
	return res, nil
}

// SongWith fetches a YouTube video and analyses it as Options asks. If the audio
// is not all of the song (see Excerpt) the edges that cannot be known are left
// out. (If reading the middle of a long track failed and the beginning was read
// instead, the audio is the beginning of the song, and is described as that.)
func SongWith(ctx context.Context, mpvPath, ytdlpPath, id string, duration float64, o Options) (Result, error) {
	pcm, start, err := fetch(ctx, mpvPath, ytdlpPath, id, duration)
	if err != nil {
		return Result{}, err
	}
	return analyzeExcerpt(pcm, Excerpt{Offset: float64(start), Duration: duration}, o)
}

// Song fetches and analyses a YouTube video completely.
func Song(ctx context.Context, mpvPath, ytdlpPath, id string, duration float64) (Result, error) {
	return SongWith(ctx, mpvPath, ytdlpPath, id, duration, Options{})
}
