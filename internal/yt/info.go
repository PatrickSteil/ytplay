package yt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"time"
)

// Comment is one YouTube comment.
type Comment struct {
	Author string
	Text   string
	Likes  int
}

// Info is the extra data shown about the current song.
type Info struct {
	Description string
	Views       int
	Likes       int
	Year        int // upload year, 0 if unknown
	Comments    []Comment
}

const infoTimeout = 30 * time.Second

type rawInfo struct {
	Description *string `json:"description"`
	ViewCount   *int    `json:"view_count"`
	LikeCount   *int    `json:"like_count"`
	UploadDate  *string `json:"upload_date"` // YYYYMMDD
	Comments    []struct {
		Author *string `json:"author"`
		Text   *string `json:"text"`
		Likes  *int    `json:"like_count"`
	} `json:"comments"`
}

// FetchInfo asks yt-dlp for the description, statistics and the top
// maxComments comments of a video. It resolves the video, so it takes a
// couple of seconds.
func FetchInfo(ctx context.Context, ytdlpPath, id string, maxComments int) (Info, error) {
	ctx, cancel := context.WithTimeout(ctx, infoTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, ytdlpPath,
		"--skip-download", "--dump-single-json", "--no-warnings", "--ignore-config", "--write-comments",
		"--extractor-args", "youtube:max_comments="+strconv.Itoa(maxComments)+",all,0,0;comment_sort=top",
		"https://www.youtube.com/watch?v="+id)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Info{}, fmt.Errorf("yt-dlp: %w", ctxErr)
		}
		if msg := lastNonEmptyLine(stderr.String()); msg != "" {
			return Info{}, errors.New(msg)
		}
		return Info{}, fmt.Errorf("run yt-dlp: %w", err)
	}

	var raw rawInfo
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		return Info{}, fmt.Errorf("parse yt-dlp output: %w", err)
	}
	var info Info
	if raw.Description != nil {
		info.Description = *raw.Description
	}
	if raw.ViewCount != nil {
		info.Views = *raw.ViewCount
	}
	if raw.LikeCount != nil {
		info.Likes = *raw.LikeCount
	}
	if raw.UploadDate != nil && len(*raw.UploadDate) >= 4 {
		info.Year, _ = strconv.Atoi((*raw.UploadDate)[:4])
	}
	for _, c := range raw.Comments {
		if c.Text == nil || *c.Text == "" {
			continue
		}
		cm := Comment{Text: *c.Text}
		if c.Author != nil {
			cm.Author = *c.Author
		}
		if c.Likes != nil {
			cm.Likes = *c.Likes
		}
		info.Comments = append(info.Comments, cm)
	}
	return info, nil
}
