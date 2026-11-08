package yt

import (
	"net/url"
	"regexp"
	"strings"
)

// A song is identified everywhere by a key: for YouTube the video id, for
// SoundCloud "sc:user/track". URLs given to yt-dlp and mpv are always rebuilt
// from a key that matched, never taken from pasted or listed text.

// Source is where a song comes from. YouTube is the zero value.
type Source int

const (
	YouTube Source = iota
	SoundCloud
)

const scPrefix = "sc:"

var (
	youtubeKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	scKey      = regexp.MustCompile(`^sc:([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)$`)
	// pages of a SoundCloud user that look like user/slug but are not tracks
	scNotTracks = map[string]bool{"likes": true, "tracks": true, "albums": true, "sets": true, "reposts": true,
		"popular-tracks": true, "following": true, "followers": true, "comments": true, "spotlight": true}
)

// SourceOf is the source of a key.
func SourceOf(key string) Source {
	if strings.HasPrefix(key, scPrefix) {
		return SoundCloud
	}
	return YouTube
}

func dots(s string) bool { return strings.Trim(s, ".") == "" }

func validSC(user, slug string) bool {
	return !dots(user) && !dots(slug) && !scNotTracks[strings.ToLower(slug)]
}

// KeyOf is the key of a song URL (a YouTube watch URL or a SoundCloud track
// URL), "" for anything else.
func KeyOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	switch strings.ToLower(u.Hostname()) {
	case "soundcloud.com", "www.soundcloud.com", "m.soundcloud.com":
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) != 2 || strings.HasSuffix(u.Path, "//") {
			return ""
		}
		key := scPrefix + parts[0] + "/" + parts[1]
		if m := scKey.FindStringSubmatch(key); m == nil || !validSC(m[1], m[2]) {
			return ""
		}
		return key
	}
	if v := u.Query().Get("v"); youtubeKey.MatchString(v) {
		return v
	}
	return ""
}

// URLOf is the URL that plays a key, "" if the key is not valid.
func URLOf(key string) string {
	if SourceOf(key) == SoundCloud {
		m := scKey.FindStringSubmatch(key)
		if m == nil || !validSC(m[1], m[2]) {
			return ""
		}
		return "https://soundcloud.com/" + m[1] + "/" + m[2]
	}
	if !youtubeKey.MatchString(key) {
		return ""
	}
	return "https://www.youtube.com/watch?v=" + key
}
