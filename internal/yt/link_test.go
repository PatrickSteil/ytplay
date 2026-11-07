package yt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLink(t *testing.T) {
	const vid, list = "dQw4w9WgXcQ", "PLabc123_-xyzABCDEFGHIJKLMN"
	for raw, want := range map[string]Link{
		// playlists, however the link is written
		"https://www.youtube.com/playlist?list=" + list:                         {PlaylistLink, list},
		"https://www.youtube.com/watch?v=" + vid + "&list=" + list + "&index=2": {PlaylistLink, list},
		"https://youtu.be/" + vid + "?list=" + list:                             {PlaylistLink, list},
		"https://music.youtube.com/playlist?list=OLAK5uy_abcdefg":               {PlaylistLink, "OLAK5uy_abcdefg"},
		"https://m.youtube.com/playlist?list=" + list:                           {PlaylistLink, list},
		"youtube.com/playlist?list=" + list:                                     {PlaylistLink, list},
		"  https://www.youtube.com/playlist?list=" + list + "  ":                {PlaylistLink, list},
		"<https://www.youtube.com/playlist?list=" + list + ">":                  {PlaylistLink, list},
		"https://www.youtube.com/watch?v=" + vid + "&list=RD" + vid:             {PlaylistLink, "RD" + vid},
		// single videos
		"https://www.youtube.com/watch?v=" + vid:            {VideoLink, vid},
		"https://youtu.be/" + vid:                           {VideoLink, vid},
		"https://youtu.be/" + vid + "?t=42":                 {VideoLink, vid},
		"https://www.youtube.com/shorts/" + vid:             {VideoLink, vid},
		"https://www.youtube.com/live/" + vid:               {VideoLink, vid},
		"www.youtube.com/watch?v=" + vid:                    {VideoLink, vid},
		"'https://youtu.be/" + vid + "'":                    {VideoLink, vid},
		"https://www.youtube.com/watch?v=" + vid + "&t=10s": {VideoLink, vid},
	} {
		if got := ParseLink(raw); got != want {
			t.Errorf("ParseLink(%q) = %+v, want %+v", raw, got, want)
		}
	}
	for _, raw := range []string{
		"", "   ", "daft punk", "daft punk one more time",
		"https://example.com/watch?v=" + vid,
		"https://youtube.com.evil.com/watch?v=" + vid,
		"https://notyoutube.com/playlist?list=" + list,
		"https://evil.com/?u=youtube.com/playlist?list=" + list,
		"ftp://www.youtube.com/watch?v=" + vid,
		"https://www.youtube.com/watch?v=short",
		"https://www.youtube.com/watch?v=" + vid + "xx",
		"https://www.youtube.com/watch",
		"https://www.youtube.com/playlist",
		"https://www.youtube.com/playlist?list=",
		"https://www.youtube.com/playlist?list=a b",
		"https://www.youtube.com/playlist?list=PL;rm",
		"https://evil.com/playlist?list=" + list,
		"https://evil.com/watch?v=" + vid,
		"https://www.youtube.com/playlist?list=PL%24%28id%29",  // a list id that decodes to PL$(id)
		"https://www.youtube.com/playlist?list=PL%22%20-o%20x", // PL" -o x
		"https://www.youtube.com/playlist?list=PL%0Arm",        // a newline in the id
		"https://www.youtube.com/watch?v=" + vid + "&x=1 junk", // text after a link is not part of it
		"https://www.youtube.com/@somechannel",
		"https://www.youtube.com/",
		"-o /tmp/x", "--exec rm", "--playlist-end 3",
	} {
		if got := ParseLink(raw); got.Kind != NotALink {
			t.Errorf("ParseLink(%q) = %+v, want not a link", raw, got)
		}
	}
}

// What is run is rebuilt from the id, never the text the user pasted.
func TestLinkURLIsRebuiltFromTheID(t *testing.T) {
	if got := (Link{PlaylistLink, "PLabc"}).URL(); got != "https://www.youtube.com/playlist?list=PLabc" {
		t.Errorf("playlist URL = %q", got)
	}
	if got := (Link{VideoLink, "dQw4w9WgXcQ"}).URL(); got != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Errorf("video URL = %q", got)
	}
	if got := (Link{}).URL(); got != "" {
		t.Errorf("no link: URL = %q", got)
	}
}

func recordingYtdlp(t *testing.T, output string) (script, argsFile string) {
	t.Helper()
	argsFile = filepath.Join(t.TempDir(), "args")
	return fakeYtdlp(t, `
for a in "$@"; do printf '%s\n' "$a" >> `+argsFile+`; done
`+output), argsFile
}

func TestLoadLinkReadsAPlaylistAndSkipsWhatCannotPlay(t *testing.T) {
	script, argsFile := recordingYtdlp(t, `
echo '{"id":"a1","title":"One","channel":"C","duration":10,"playlist_title":"My mix"}'
echo '{"id":"a2","title":"Two","uploader":"U","duration":20,"playlist_title":"My mix"}'
echo '{"id":"p1","title":"[Private video]","duration":null,"playlist_title":"My mix"}'
echo '{"id":"d1","title":"[Deleted video]","duration":null,"playlist_title":"My mix"}'
echo 'garbage'
echo '{"id":"a3","title":"Three","channel":"C","duration":30,"playlist_title":"My mix"}'
`)
	got, err := LoadLink(context.Background(), script, Link{PlaylistLink, "PLabc123"}, 500)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range got.Entries {
		ids = append(ids, r.ID)
	}
	if strings.Join(ids, ",") != "a1,a2,a3" {
		t.Errorf("entries = %v, want the three that can play, in order", ids)
	}
	if got.Title != "My mix" || got.Truncated {
		t.Errorf("title %q truncated %v", got.Title, got.Truncated)
	}
	data, _ := os.ReadFile(argsFile)
	args := "\n" + string(data)
	for _, want := range []string{"--flat-playlist", "--dump-json", "--ignore-config", "--playlist-end\n501", "https://www.youtube.com/playlist?list=PLabc123"} {
		if !strings.Contains(args, "\n"+want+"\n") {
			t.Errorf("missing %q in the arguments %q", want, strings.Fields(args))
		}
	}
}

func TestLoadLinkStopsAtTheLimitAndSaysSo(t *testing.T) {
	var out strings.Builder
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&out, "echo '{\"id\":\"id%02d\",\"title\":\"T%d\",\"duration\":5}'\n", i, i)
	}
	script, _ := recordingYtdlp(t, out.String())
	got, err := LoadLink(context.Background(), script, Link{PlaylistLink, "PLabc123"}, 5)
	if err != nil || len(got.Entries) != 5 || !got.Truncated || got.Entries[4].ID != "id05" {
		t.Errorf("entries %d truncated %v err %v, want the first 5 and truncated", len(got.Entries), got.Truncated, err)
	}
	exact, _ := recordingYtdlp(t, strings.Join(strings.Split(out.String(), "\n")[:5], "\n")+"\n")
	if got, _ := LoadLink(context.Background(), exact, Link{PlaylistLink, "PLabc123"}, 5); len(got.Entries) != 5 || got.Truncated {
		t.Errorf("a playlist of exactly the limit: entries %d truncated %v, want not truncated", len(got.Entries), got.Truncated)
	}
}

func TestLoadLinkForAVideoAsksForOnlyThatVideo(t *testing.T) {
	script, argsFile := recordingYtdlp(t, `echo '{"id":"dQw4w9WgXcQ","title":"Never","channel":"Rick","duration":212}'`)
	got, err := LoadLink(context.Background(), script, Link{VideoLink, "dQw4w9WgXcQ"}, 500)
	if err != nil || len(got.Entries) != 1 || got.Entries[0].Title != "Never" || got.Title != "" {
		t.Fatalf("got %+v err %v", got, err)
	}
	data, _ := os.ReadFile(argsFile)
	args := "\n" + string(data)
	for _, want := range []string{"--no-playlist", "https://www.youtube.com/watch?v=dQw4w9WgXcQ"} {
		if !strings.Contains(args, "\n"+want+"\n") {
			t.Errorf("missing %q in the arguments %q", want, strings.Fields(args))
		}
	}
	if strings.Contains(args, "--playlist-end") {
		t.Errorf("a single video must not get a playlist limit: %q", strings.Fields(args))
	}
}

func TestLoadLinkErrors(t *testing.T) {
	failing, _ := recordingYtdlp(t, "echo 'ERROR: Private playlist' >&2\nexit 1\n")
	if _, err := LoadLink(context.Background(), failing, Link{PlaylistLink, "PLabc"}, 10); err == nil || !strings.Contains(err.Error(), "Private playlist") {
		t.Errorf("err = %v, want yt-dlp's last line", err)
	}
	empty, _ := recordingYtdlp(t, `echo '{"id":"p1","title":"[Private video]"}'`)
	if _, err := LoadLink(context.Background(), empty, Link{PlaylistLink, "PLabc"}, 10); err == nil || !strings.Contains(err.Error(), "nothing") {
		t.Errorf("a playlist with nothing playable: err = %v, want to say there is nothing in it", err)
	}
	if _, err := LoadLink(context.Background(), failing, Link{}, 10); err == nil {
		t.Errorf("no link: want an error")
	}
}

func TestLoadLinkCancelled(t *testing.T) {
	script := fakeYtdlp(t, `exec sleep 5`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := LoadLink(ctx, script, Link{PlaylistLink, "PLabc"}, 10); err == nil {
		t.Errorf("a cancelled context must give an error")
	}
}
