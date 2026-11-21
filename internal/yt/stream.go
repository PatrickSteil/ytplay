package yt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const streamTimeout = 40 * time.Second

// StreamURL finds the direct audio address of a song, for a device that fetches
// the audio itself (a Chromecast), and the content type to tell it. songURL must
// be the address of a song as URLOf makes it.
func StreamURL(ctx context.Context, ytdlpPath, songURL string) (stream, contentType string, err error) {
	if songURL == "" || URLOf(KeyOf(songURL)) != songURL { // only addresses the program made itself
		return "", "", fmt.Errorf("not a song: %q", songURL)
	}
	ctx, cancel := context.WithTimeout(ctx, streamTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, ytdlpPath,
		"-f", "bestaudio[ext=m4a]/bestaudio[acodec^=mp4a]/bestaudio/best", "--no-playlist", "--no-warnings", "--ignore-config",
		"--check-formats",
		"--print", "%(url)s", "--print", "%(ext)s", "--print", "%(protocol)s", songURL)
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", "", fmt.Errorf("yt-dlp: %w", ctxErr)
		}
		if msg := lastNonEmptyLine(stderr.String()); msg != "" {
			return "", "", errors.New(msg)
		}
		return "", "", fmt.Errorf("run yt-dlp: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) < 3 || !strings.HasPrefix(lines[0], "http") {
		return "", "", errors.New("yt-dlp gave no audio address")
	}
	return lines[0], contentTypeOf(strings.TrimSpace(lines[1]), strings.TrimSpace(lines[2])), nil
}

// contentTypeOf is the type a cast device is told for a stream of the given
// file extension and protocol.
func contentTypeOf(ext, protocol string) string {
	if strings.Contains(protocol, "m3u8") {
		return "application/x-mpegURL"
	}
	switch ext {
	case "mp3":
		return "audio/mpeg"
	case "webm":
		return "audio/webm"
	case "opus", "ogg", "oga":
		return "audio/ogg"
	case "flac":
		return "audio/flac"
	case "wav":
		return "audio/wav"
	}
	return "audio/mp4"
}
