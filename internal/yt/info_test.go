package yt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFetchInfoParsesDescriptionStatsAndTopComments(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	script := fakeYtdlp(t, `
for a in "$@"; do printf '%s\n' "$a" >> `+argsFile+`; done
cat <<'JSON'
{"id":"abc","title":"T","description":"Line one\n\nLine two","view_count":1234567,"like_count":8900,
 "upload_date":"20130419",
 "comments":[{"author":"@alice","text":"first!","like_count":48000},{"author":"@bob","text":"nice\nsong","like_count":null}]}
JSON
`)
	info, err := FetchInfo(context.Background(), script, "abc", 5)
	if err != nil {
		t.Fatal(err)
	}
	if info.Description != "Line one\n\nLine two" || info.Views != 1234567 || info.Likes != 8900 || info.Year != 2013 {
		t.Errorf("info = %+v", info)
	}
	if len(info.Comments) != 2 || info.Comments[0] != (Comment{Author: "@alice", Text: "first!", Likes: 48000}) ||
		info.Comments[1].Likes != 0 || info.Comments[1].Text != "nice\nsong" {
		t.Errorf("comments = %+v", info.Comments)
	}
	data, _ := os.ReadFile(argsFile)
	args := "\n" + string(data)
	for _, want := range []string{"--skip-download", "--dump-single-json", "--ignore-config", "--no-warnings", "--write-comments",
		"youtube:max_comments=5,all,0,0;comment_sort=top", "https://www.youtube.com/watch?v=abc"} {
		if !strings.Contains(args, "\n"+want+"\n") {
			t.Errorf("missing arg %q in %q", want, args)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(args), "https://www.youtube.com/watch?v=abc") {
		t.Errorf("url must come last: %q", args)
	}
}

func TestFetchInfoToleratesMissingFields(t *testing.T) {
	script := fakeYtdlp(t, `echo '{"id":"abc","description":null,"view_count":null}'`)
	info, err := FetchInfo(context.Background(), script, "abc", 5)
	if err != nil {
		t.Fatal(err)
	}
	if info.Description != "" || info.Views != 0 || info.Year != 0 || len(info.Comments) != 0 {
		t.Errorf("info = %+v", info)
	}
}

func TestFetchInfoErrors(t *testing.T) {
	script := fakeYtdlp(t, `echo 'ERROR: Video unavailable' >&2; exit 1`)
	if _, err := FetchInfo(context.Background(), script, "abc", 5); err == nil || err.Error() != "ERROR: Video unavailable" {
		t.Errorf("err = %v", err)
	}
	script = fakeYtdlp(t, `echo 'not json'`)
	if _, err := FetchInfo(context.Background(), script, "abc", 5); err == nil {
		t.Error("expected a parse error")
	}
}

const scInfoScript = `
printf '{"id":"1","description":"A mix","view_count":91661,"like_count":21142,"release_year":2024,"comments":['
i=1; while [ $i -le 40 ]; do [ $i -gt 1 ] && printf ','; printf '{"author":"u%s","text":"c%s","start_time":%s.5,"timestamp":1}' $i $i $i; i=$((i+1)); done
printf ']}\n'
`

func TestFetchInfoOfASoundCloudTrack(t *testing.T) {
	script, argsFile := recordingYtdlp(t, scInfoScript)
	info, err := FetchInfo(context.Background(), script, "sc:ralph/mix", 10)
	if err != nil {
		t.Fatal(err)
	}
	if info.Description != "A mix" || info.Views != 91661 || info.Likes != 21142 || info.Year != 2024 {
		t.Errorf("info %+v", info)
	}
	// yt-dlp sends every comment whatever was asked: only as many as asked are kept
	if len(info.Comments) != 10 || info.Comments[0].Text != "c1" || info.Comments[0].Author != "u1" || info.Comments[9].Text != "c10" {
		t.Errorf("comments %+v", info.Comments)
	}
	if c := info.Comments[0]; c.Likes != 0 || c.At != 1.5 {
		t.Errorf("a SoundCloud comment has no likes and says where in the track it was left: %+v", c)
	}
	if info.Source != SoundCloud {
		t.Errorf("source %v", info.Source)
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "\nhttps://soundcloud.com/ralph/mix") && !strings.HasPrefix(string(args), "https://soundcloud.com/ralph/mix") {
		t.Errorf("yt-dlp was not given the track's page:\n%s", args)
	}
	more, err := FetchInfo(context.Background(), script, "sc:ralph/mix", 30)
	if err != nil || len(more.Comments) != 30 {
		t.Errorf("30 asked: %d comments, err %v", len(more.Comments), err)
	}
}

func TestFetchInfoSaysWhyASoundCloudTrackCannotBeUsed(t *testing.T) {
	script := fakeYtdlp(t, `echo 'ERROR: [soundcloud] 254112221: This video is DRM protected' >&2; exit 1`)
	_, err := FetchInfo(context.Background(), script, "sc:a/b", 10)
	if err == nil || !strings.Contains(err.Error(), "DRM protected") {
		t.Errorf("err %v, want it to say DRM protected", err)
	}
}

func TestFetchInfoRefusesAKeyThatIsNotASong(t *testing.T) {
	script, argsFile := recordingYtdlp(t, scInfoScript)
	for _, key := range []string{"sc:../x", "sc:a/b/c", "--exec x", ""} {
		if _, err := FetchInfo(context.Background(), script, key, 5); err == nil {
			t.Errorf("%q was accepted", key)
		}
	}
	if _, err := os.Stat(argsFile); err == nil {
		t.Error("yt-dlp was run for a key that is not a song")
	}
}
