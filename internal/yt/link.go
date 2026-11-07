package yt

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// LinkKind says what a YouTube link points to.
type LinkKind int

const (
	NotALink     LinkKind = iota // not a link to YouTube, or to nothing that can be played
	VideoLink                    // one video
	PlaylistLink                 // a playlist (a watch link that also names a list counts as the list)
)

// Link is what a pasted YouTube link points to.
type Link struct {
	Kind LinkKind
	ID   string // the video or the playlist
}

var (
	videoID  = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	playlist = regexp.MustCompile(`^[A-Za-z0-9_-]{2,100}$`)

	linkHosts = map[string]bool{
		"youtube.com": true, "www.youtube.com": true, "m.youtube.com": true,
		"music.youtube.com": true, "youtu.be": true, "www.youtu.be": true,
	}
)

// ParseLink reads a pasted link: a watch, short, shorts, live or playlist link
// to YouTube (with or without the scheme, in quotes or angle brackets). Anything
// else, a search term included, is NotALink.
func ParseLink(raw string) Link {
	s := strings.TrimSpace(strings.Trim(strings.TrimSpace(raw), "\"'<>"))
	if s == "" || strings.ContainsAny(s, " \t\r\n") {
		return Link{}
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !linkHosts[strings.ToLower(u.Hostname())] {
		return Link{}
	}
	q := u.Query()
	if list := q.Get("list"); playlist.MatchString(list) {
		return Link{PlaylistLink, list}
	}
	var id string
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case strings.HasSuffix(strings.ToLower(u.Hostname()), "youtu.be"):
		id = segments[0]
	case u.Path == "/watch":
		id = q.Get("v")
	case len(segments) == 2 && (segments[0] == "shorts" || segments[0] == "live" || segments[0] == "embed" || segments[0] == "v"):
		id = segments[1]
	}
	if videoID.MatchString(id) {
		return Link{VideoLink, id}
	}
	return Link{}
}

// URL is the address yt-dlp is given, rebuilt from the id: what the user pasted
// (and might have put options or other text in) is never passed on.
func (l Link) URL() string {
	switch l.Kind {
	case PlaylistLink:
		return "https://www.youtube.com/playlist?list=" + l.ID
	case VideoLink:
		return "https://www.youtube.com/watch?v=" + l.ID
	}
	return ""
}

// Loaded is what a link holds.
type Loaded struct {
	Title     string   // the playlist's name ("" for a single video)
	Entries   []Result // the songs that can be played, in order
	Truncated bool     // the playlist has more than was loaded
}

// MaxPlaylist is the most songs of a playlist that are loaded.
const MaxPlaylist = 500

const loadTimeout = 60 * time.Second

// LoadLink lists what a link holds: the videos of a playlist (at most max, the
// private and deleted ones left out) or the one video.
func LoadLink(ctx context.Context, ytdlpPath string, l Link, max int) (Loaded, error) {
	var extra []string
	switch l.Kind {
	case PlaylistLink:
		extra = []string{"--playlist-end", strconv.Itoa(max + 1)} // one more than wanted, to know there is more
	case VideoLink:
		extra = []string{"--no-playlist"}
	default:
		return Loaded{}, errors.New("that is not a YouTube link")
	}
	all, title, err := runListFor(ctx, loadTimeout, ytdlpPath, l.URL(), extra...)
	if err != nil {
		return Loaded{}, err
	}
	out := Loaded{Title: title, Truncated: len(all) > max}
	for _, r := range all {
		if !strings.HasPrefix(r.Title, "[") || !strings.HasSuffix(r.Title, "video]") { // "[Private video]", "[Deleted video]"
			out.Entries = append(out.Entries, r)
		}
	}
	if len(out.Entries) > max {
		out.Entries = out.Entries[:max]
	}
	if len(out.Entries) == 0 {
		return Loaded{}, errors.New("there is nothing to play in that link (it may be private, deleted or empty)")
	}
	return out, nil
}
