package yt

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestStreamURLGivesTheDirectAddressAndItsType(t *testing.T) {
	for _, c := range []struct{ ext, protocol, wantType string }{
		{"m4a", "https", "audio/mp4"},
		{"mp3", "https", "audio/mpeg"},
		{"webm", "https", "audio/webm"},
		{"opus", "https", "audio/ogg"},
		{"mp4", "m3u8_native", "application/x-mpegURL"}, // SoundCloud's HLS
		{"aac", "m3u8", "application/x-mpegURL"},
		{"weird", "https", "audio/mp4"}, // what a speaker most likely takes
	} {
		script, argsFile := recordingYtdlp(t, `printf '%s\n%s\n%s\n' 'https://cdn.example/a?x=1' '`+c.ext+`' '`+c.protocol+`'`)
		url, typ, err := StreamURL(context.Background(), script, "https://www.youtube.com/watch?v=abc")
		if err != nil || url != "https://cdn.example/a?x=1" || typ != c.wantType {
			t.Errorf("%s/%s: %q %q %v, want type %q", c.ext, c.protocol, url, typ, err, c.wantType)
		}
		args, _ := os.ReadFile(argsFile)
		for _, want := range []string{"--no-playlist", "--check-formats", "https://www.youtube.com/watch?v=abc", "bestaudio"} {
			if !strings.Contains(string(args), want) {
				t.Errorf("yt-dlp args lack %q:\n%s", want, args)
			}
		}
	}
}

func TestStreamURLOnlyAsksAboutSongs(t *testing.T) {
	script, argsFile := recordingYtdlp(t, `echo x`)
	for _, u := range []string{"", "--exec x", "https://example.com/a", "file:///etc/passwd", "https://soundcloud.com/a"} {
		if _, _, err := StreamURL(context.Background(), script, u); err == nil {
			t.Errorf("%q was accepted", u)
		}
	}
	if _, err := os.Stat(argsFile); err == nil {
		t.Error("yt-dlp was run for something that is not a song")
	}
}

func TestStreamURLErrors(t *testing.T) {
	script := fakeYtdlp(t, `echo 'ERROR: [soundcloud] 1: This video is DRM protected' >&2; exit 1`)
	if _, _, err := StreamURL(context.Background(), script, "https://soundcloud.com/a/b"); err == nil || !strings.Contains(err.Error(), "DRM") {
		t.Errorf("err %v", err)
	}
	short := fakeYtdlp(t, `echo only-one-line`)
	if _, _, err := StreamURL(context.Background(), short, "https://www.youtube.com/watch?v=abc"); err == nil {
		t.Error("an answer without its parts was accepted")
	}
	notURL := fakeYtdlp(t, `printf '%s\n%s\n%s\n' 'not a url' m4a https`)
	if _, _, err := StreamURL(context.Background(), notURL, "https://www.youtube.com/watch?v=abc"); err == nil {
		t.Error("an answer that is not an http address was accepted")
	}
}
