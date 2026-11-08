// Package yt searches YouTube by running yt-dlp in flat mode.
package yt

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Result is one search hit.
type Result struct {
	ID       string // "dQw4w9WgXcQ"
	Title    string
	Channel  string  // "channel" field, falling back to "uploader"
	Duration float64 // seconds; 0 when unknown
	Source   Source
	Thumb    string // thumbnail URL when the source gives one ("" for YouTube: derived from the ID)
}

// URL is the watch URL mpv plays.
func (r Result) URL() string { return URLOf(r.ID) }

// ThumbURL is the 320x180 thumbnail.
func (r Result) ThumbURL() string {
	if r.Thumb != "" || r.Source == SoundCloud {
		return r.Thumb
	}
	return "https://i.ytimg.com/vi/" + r.ID + "/mqdefault.jpg"
}

const searchTimeout = 20 * time.Second

// Search runs yt-dlp and returns up to n results.
func Search(ctx context.Context, ytdlpPath, query string, n int) ([]Result, error) {
	return runList(ctx, ytdlpPath, "ytsearch"+strconv.Itoa(n)+":"+query)
}

// runList runs yt-dlp in flat mode on target (a search term or URL) with extra
// leading args, and parses its one-JSON-object-per-line output.
func runList(ctx context.Context, ytdlpPath, target string, extra ...string) ([]Result, error) {
	res, _, err := runListFor(ctx, searchTimeout, ytdlpPath, target, extra...)
	return res, err
}

// runListFor is runList with its own time limit; it also returns the name of
// the playlist the entries belong to, if yt-dlp says one.
func runListFor(ctx context.Context, timeout time.Duration, ytdlpPath, target string, extra ...string) ([]Result, string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{"--flat-playlist", "--dump-json", "--no-warnings", "--ignore-config"}
	args = append(append(args, extra...), target)
	cmd := exec.CommandContext(ctx, ytdlpPath, args...)
	cmd.WaitDelay = 2 * time.Second // a child that keeps the pipes open must not hold up a cancelled search
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			if errors.Is(ctxErr, context.DeadlineExceeded) {
				return nil, "", fmt.Errorf("yt-dlp timed out after %s", timeout)
			}
			return nil, "", fmt.Errorf("yt-dlp: %w", ctxErr)
		}
		if msg := lastNonEmptyLine(stderr.String()); msg != "" {
			return nil, "", errors.New(msg)
		}
		return nil, "", fmt.Errorf("run yt-dlp: %w", err)
	}

	var results []Result
	var title string
	sc := bufio.NewScanner(&stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		if r, listTitle, ok := parseEntry(sc.Bytes()); ok {
			results = append(results, r)
			if title == "" {
				title = listTitle
			}
		}
	}
	return results, title, nil
}

// Mix returns YouTube's auto-generated "Mix" (radio) playlist for a video: up
// to n songs similar to it, usually starting with the seed itself.
func Mix(ctx context.Context, ytdlpPath, seedID string, n int) ([]Result, error) {
	url := "https://www.youtube.com/watch?v=" + seedID + "&list=RD" + seedID
	return runList(ctx, ytdlpPath, url, "--playlist-end", strconv.Itoa(n))
}

// mixSize is how many entries are fetched per seed. More than the wanted
// count, because duplicates and songs already queued are dropped afterwards.
const mixSize = 40

// Similar fetches the Mix of every seed in parallel and merges them: results
// are interleaved round-robin (so every seed is represented), duplicates and
// anything in exclude are dropped, and at most n songs are returned. A seed
// whose Mix cannot be fetched is skipped; it is an error only if all fail.
func Similar(ctx context.Context, ytdlpPath string, seeds []string, exclude map[string]bool, n int) ([]Result, error) {
	if len(seeds) == 0 {
		return nil, errors.New("no seed songs")
	}
	var youtube []string // only YouTube has a Mix to ask for
	for _, seed := range seeds {
		if SourceOf(seed) == YouTube {
			youtube = append(youtube, seed)
		}
	}
	if len(youtube) == 0 {
		return nil, errors.New("radio needs a YouTube song in the queue")
	}
	seeds = youtube
	lists := make([][]Result, len(seeds))
	errs := make([]error, len(seeds))
	var wg sync.WaitGroup
	for i, seed := range seeds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lists[i], errs[i] = Mix(ctx, ytdlpPath, seed, mixSize)
		}()
	}
	wg.Wait()

	failed := 0
	var firstErr error
	for _, err := range errs {
		if err != nil {
			failed++
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if failed == len(seeds) {
		return nil, firstErr
	}
	return interleave(lists, exclude, n), nil
}

// interleave drops excluded entries from every list first (so an excluded
// song never costs its seed a turn), then takes one entry from each list in
// turn, skipping IDs already taken, until n entries are collected.
func interleave(lists [][]Result, exclude map[string]bool, n int) []Result {
	filtered := make([][]Result, len(lists))
	for i, l := range lists {
		for _, r := range l {
			if !exclude[r.ID] {
				filtered[i] = append(filtered[i], r)
			}
		}
	}
	taken := map[string]bool{}
	var out []Result
	for round := 0; len(out) < n; round++ {
		progressed := false
		for _, l := range filtered {
			if round >= len(l) {
				continue
			}
			progressed = true
			if r := l[round]; !taken[r.ID] {
				taken[r.ID] = true
				out = append(out, r)
				if len(out) == n {
					return out
				}
			}
		}
		if !progressed {
			break
		}
	}
	return out
}

type rawEntry struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Channel    *string  `json:"channel"`
	Uploader   *string  `json:"uploader"`
	Duration   *float64 `json:"duration"`
	LiveStatus *string  `json:"live_status"`
	// SoundCloud entries are known by their page and carry their own artwork.
	WebpageURL   string `json:"webpage_url"`
	ExtractorKey string `json:"extractor_key"`
	Thumbnails   []struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	} `json:"thumbnails"`
	// PlaylistTitle is the playlist an entry of a playlist belongs to.
	PlaylistTitle *string `json:"playlist_title"`
}

// parseLine decodes one yt-dlp JSON line. ok is false for lines that are
// invalid, have no id, or are live streams.
func parseLine(line []byte) (Result, bool) {
	r, _, ok := parseEntry(line)
	return r, ok
}

// parseEntry is parseLine and the title of the playlist the entry is in.
func parseEntry(line []byte) (Result, string, bool) {
	var e rawEntry
	if err := json.Unmarshal(line, &e); err != nil {
		return Result{}, "", false
	}
	if e.ID == "" {
		return Result{}, "", false
	}
	soundcloud := strings.HasPrefix(strings.ToLower(e.ExtractorKey), "soundcloud") || strings.Contains(strings.ToLower(e.WebpageURL), "soundcloud.com")
	if e.LiveStatus != nil && *e.LiveStatus == "is_live" {
		return Result{}, "", false
	}
	r := Result{ID: e.ID, Title: e.Title}
	if soundcloud {
		key := KeyOf(e.WebpageURL)
		if SourceOf(key) != SoundCloud {
			return Result{}, "", false // a profile, a set or something else: not a track
		}
		r.ID, r.Source = key, SoundCloud
		for _, want := range []string{"t500x500", "large"} {
			for _, t := range e.Thumbnails {
				if t.ID == want && r.Thumb == "" {
					r.Thumb = t.URL
				}
			}
		}
	}
	switch {
	case e.Channel != nil && *e.Channel != "":
		r.Channel = *e.Channel
	case e.Uploader != nil:
		r.Channel = *e.Uploader
	}
	if e.Duration != nil {
		r.Duration = *e.Duration
	}
	var listTitle string
	if e.PlaylistTitle != nil {
		listTitle = *e.PlaylistTitle
	}
	return r, listTitle, true
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}
