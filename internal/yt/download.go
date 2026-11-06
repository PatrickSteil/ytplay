package yt

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const downloadTimeout = 10 * time.Minute

var audioFormats = map[string]bool{
	"mp3": true, "m4a": true, "opus": true, "flac": true, "wav": true, "aac": true, "vorbis": true,
}

// SplitAudioPath splits a target path into the path without extension and
// the audio format. A missing or unknown extension becomes mp3 (appended).
func SplitAudioPath(path string) (base, format string) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	if audioFormats[ext] {
		return strings.TrimSuffix(path, filepath.Ext(path)), ext
	}
	return path, "mp3"
}

// Download saves the audio of a video to path. The extension of path picks
// the format (default mp3); converting needs ffmpeg.
func Download(ctx context.Context, ytdlpPath, id, path string) error {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	base, format := SplitAudioPath(path)
	cmd := exec.CommandContext(ctx, ytdlpPath,
		"--no-playlist", "--no-warnings", "--ignore-config", "--force-overwrites",
		"-x", "--audio-format", format,
		"-o", strings.ReplaceAll(base, "%", "%%")+".%(ext)s",
		"https://www.youtube.com/watch?v="+id)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("yt-dlp: %w", ctxErr)
		}
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = msg[i+1:]
		}
		return fmt.Errorf("yt-dlp: %s", msg)
	}
	return nil
}
