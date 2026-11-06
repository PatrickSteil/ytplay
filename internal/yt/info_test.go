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
	if len(info.Comments) != 2 || info.Comments[0] != (Comment{"@alice", "first!", 48000}) ||
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
