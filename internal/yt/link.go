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

// LinkKind says what a YouTube or SoundCloud link points to.
type LinkKind int

const (
	NotALink     LinkKind = iota // not a link to YouTube, or to nothing that can be played
	VideoLink                    // one video
	PlaylistLink                 // a playlist (a watch link that also names a list counts as the list)
)

// Link is what a pasted YouTube link points to.
type Link struct {
	Kind LinkKind
	ID   string // the video or the playlist; for SoundCloud "sc:user/track" or "sc:user/sets/name"
}

var (
	videoID  = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	playlist = regexp.MustCompile(`^[A-Za-z0-9_-]{2,100}$`)

	scSet    = regexp.MustCompile(`^sc:([A-Za-z0-9_.-]+)/sets/([A-Za-z0-9_.-]+)$`)
	scSetKey = "sc:"
	scHosts  = map[string]bool{"soundcloud.com": true, "www.soundcloud.com": true, "m.soundcloud.com": true}

	linkHosts = map[string]bool{
		"youtube.com": true, "www.youtube.com": true, "m.youtube.com": true,
		"music.youtube.com": true, "youtu.be": true, "www.youtu.be": true,
	}
)

// ParseLink reads a pasted link: a watch, short, shorts, live or playlist link
// to YouTube, or a track or set link to SoundCloud (with or without the scheme, in quotes or angle brackets). Anything
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
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return Link{}
	}
	if scHosts[strings.ToLower(u.Hostname())] {
		return parseSoundCloud(u)
	}
	if !linkHosts[strings.ToLower(u.Hostname())] {
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

// parseSoundCloud reads a SoundCloud track link (user/track) or set link
// (user/sets/name, with or without a secret token after it).
func parseSoundCloud(u *url.URL) Link {
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case len(parts) == 2 && !strings.HasSuffix(u.Path, "//"):
		if key := KeyOf("https://soundcloud.com/" + parts[0] + "/" + parts[1]); key != "" {
			return Link{VideoLink, key}
		}
	case (len(parts) == 3 || len(parts) == 4) && parts[1] == "sets":
		key := scSetKey + parts[0] + "/sets/" + parts[2]
		if scSet.MatchString(key) && !dots(parts[0]) && !dots(parts[2]) {
			return Link{PlaylistLink, key}
		}
	}
	return Link{}
}

// URL is the address yt-dlp is given, rebuilt from the id: what the user pasted
// (and might have put options or other text in) is never passed on.
func (l Link) URL() string {
	if SourceOf(l.ID) == SoundCloud {
		if l.Kind == PlaylistLink {
			if m := scSet.FindStringSubmatch(l.ID); m != nil && !dots(m[1]) && !dots(m[2]) {
				return "https://soundcloud.com/" + m[1] + "/sets/" + m[2]
			}
			return ""
		}
		if l.Kind == VideoLink {
			return URLOf(l.ID)
		}
		return ""
	}
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
		return Loaded{}, errors.New("that is not a YouTube or SoundCloud link")
	}
	target := l.URL()
	if target == "" {
		return Loaded{}, errors.New("that is not a YouTube or SoundCloud link")
	}
	all, title, err := runListFor(ctx, loadTimeout, ytdlpPath, target, extra...)
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
