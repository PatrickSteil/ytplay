package yt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseLine(t *testing.T) {
	tests := []struct {
		name   string
		line   string
		wantOK bool
		want   Result
	}{
		{
			name:   "normal entry",
			line:   `{"id":"dQw4w9WgXcQ","title":"Never Gonna Give You Up","channel":"Rick Astley","uploader":"RickAstleyVEVO","duration":212,"live_status":"not_live"}`,
			wantOK: true,
			want:   Result{ID: "dQw4w9WgXcQ", Title: "Never Gonna Give You Up", Channel: "Rick Astley", Duration: 212},
		},
		{
			name:   "null duration",
			line:   `{"id":"abc","title":"T","channel":"C","duration":null}`,
			wantOK: true,
			want:   Result{ID: "abc", Title: "T", Channel: "C", Duration: 0},
		},
		{
			name:   "missing channel falls back to uploader",
			line:   `{"id":"abc","title":"T","uploader":"Up","duration":10.5}`,
			wantOK: true,
			want:   Result{ID: "abc", Title: "T", Channel: "Up", Duration: 10.5},
		},
		{
			name:   "null channel falls back to uploader",
			line:   `{"id":"abc","title":"T","channel":null,"uploader":"Up","duration":1}`,
			wantOK: true,
			want:   Result{ID: "abc", Title: "T", Channel: "Up", Duration: 1},
		},
		{name: "live stream skipped", line: `{"id":"abc","title":"T","live_status":"is_live"}`},
		{name: "empty id skipped", line: `{"id":"","title":"T"}`},
		{name: "invalid json skipped", line: `{not json`},
		{name: "empty line skipped", line: ``},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseLine([]byte(tt.line))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestResultURLs(t *testing.T) {
	r := Result{ID: "abc_-123"}
	if got, want := r.URL(), "https://www.youtube.com/watch?v=abc_-123"; got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
	if got, want := r.ThumbURL(), "https://i.ytimg.com/vi/abc_-123/mqdefault.jpg"; got != want {
		t.Errorf("ThumbURL = %q, want %q", got, want)
	}
}

func TestLastNonEmptyLine(t *testing.T) {
	if got := lastNonEmptyLine("a\nERROR: boom\n\n  \n"); got != "ERROR: boom" {
		t.Errorf("got %q", got)
	}
	if got := lastNonEmptyLine(""); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestVideoIDFromURL(t *testing.T) {
	tests := []struct {
		url, want string
	}{
		{"https://www.youtube.com/watch?v=abc_-123", "abc_-123"},
		{"https://www.youtube.com/watch?v=abc_-123&list=PL1&t=30s", "abc_-123"},
		{"https://www.youtube.com/watch?feature=share&v=xyz", "xyz"},
		{"https://example.com/nothing", ""},
		{"", ""},
		{"::not a url", ""},
	}
	for _, tt := range tests {
		if got := VideoID(tt.url); got != tt.want {
			t.Errorf("VideoID(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

// fakeYtdlp writes a shell script standing in for yt-dlp.
func fakeYtdlp(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "yt-dlp")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSearchParsesOutputAndPassesQueryAsOneArg(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	script := fakeYtdlp(t, `
for a in "$@"; do printf '%s\n' "$a" >> `+argsFile+`; done
echo '{"id":"a1","title":"One","channel":"C","duration":10}'
echo 'garbage line'
echo '{"id":"live","title":"Live","live_status":"is_live"}'
echo '{"id":"a2","title":"Two","uploader":"U","duration":null}'
`)
	res, err := Search(context.Background(), script, "daft punk; rm -rf /", 15)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].ID != "a1" || res[1].ID != "a2" || res[1].Channel != "U" {
		t.Errorf("results = %+v", res)
	}
	data, _ := os.ReadFile(argsFile)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if got := lines[len(lines)-1]; got != "ytsearch15:daft punk; rm -rf /" {
		t.Errorf("query arg = %q", got)
	}
	for _, want := range []string{"--flat-playlist", "--dump-json", "--no-warnings", "--ignore-config"} {
		if !strings.Contains(string(data), want+"\n") {
			t.Errorf("missing flag %s in %q", want, lines)
		}
	}
}

func TestSearchErrorCarriesLastStderrLine(t *testing.T) {
	script := fakeYtdlp(t, `
echo 'WARNING: something' >&2
echo 'ERROR: unable to download webpage' >&2
echo >&2
exit 1
`)
	_, err := Search(context.Background(), script, "x", 5)
	if err == nil || err.Error() != "ERROR: unable to download webpage" {
		t.Errorf("err = %v", err)
	}
}

func TestSearchCancelledContext(t *testing.T) {
	script := fakeYtdlp(t, `exec sleep 5`)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	start := time.Now()
	_, err := Search(ctx, script, "x", 5)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Error("cancel did not stop yt-dlp promptly")
	}
}

func TestMixAsksForTheSeedsRadioPlaylist(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	script := fakeYtdlp(t, `
for a in "$@"; do printf '%s\n' "$a" >> `+argsFile+`; done
echo '{"id":"m1","title":"Mix One","channel":"C","duration":100}'
`)
	res, err := Mix(context.Background(), script, "gAjR4_CbPpQ", 40)
	if err != nil || len(res) != 1 || res[0].ID != "m1" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	data, _ := os.ReadFile(argsFile)
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	if got := args[len(args)-1]; got != "https://www.youtube.com/watch?v=gAjR4_CbPpQ&list=RDgAjR4_CbPpQ" {
		t.Errorf("url arg = %q", got)
	}
	joined := "\n" + string(data)
	for _, want := range []string{"--flat-playlist", "--dump-json", "--ignore-config", "--playlist-end\n40"} {
		if !strings.Contains(joined, "\n"+want+"\n") {
			t.Errorf("missing %q in args %q", want, args)
		}
	}
}

// mixScript serves a different mix per seed; seeds in `failing` exit with an error.
func mixScript(t *testing.T, failing string) string {
	return fakeYtdlp(t, `
case "$*" in
*RDs1*) printf '%s\n' '{"id":"a","title":"A"}' '{"id":"b","title":"B"}' '{"id":"c","title":"C"}' '{"id":"s1","title":"S1"}' ;;
*RDs2*) `+failing+`printf '%s\n' '{"id":"x","title":"X"}' '{"id":"b","title":"B"}' '{"id":"y","title":"Y"}' ;;
*) exit 1 ;;
esac
`)
}

func ids(rs []Result) string {
	var out []string
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return strings.Join(out, ",")
}

func TestSimilarInterleavesDedupesAndExcludes(t *testing.T) {
	script := mixScript(t, "")
	exclude := map[string]bool{"a": true, "s1": true, "s2": true}
	// s1 -> b,c   s2 -> x,b,y   round robin: b,x,c,(b dup),y
	res, err := Similar(context.Background(), script, []string{"s1", "s2"}, exclude, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(res); got != "b,x,c,y" {
		t.Errorf("got %s, want b,x,c,y", got)
	}
	res, _ = Similar(context.Background(), script, []string{"s1", "s2"}, exclude, 2)
	if got := ids(res); got != "b,x" {
		t.Errorf("limit 2: got %s, want b,x", got)
	}
}

func TestSimilarToleratesOneFailingSeed(t *testing.T) {
	script := mixScript(t, "echo 'ERROR: nope' >&2; exit 1\n")
	res, err := Similar(context.Background(), script, []string{"s1", "s2"}, nil, 10)
	if err != nil {
		t.Fatalf("one good seed should be enough: %v", err)
	}
	if got := ids(res); got != "a,b,c,s1" {
		t.Errorf("got %s", got)
	}
}

func TestSimilarFailsWhenEveryMixFails(t *testing.T) {
	script := mixScript(t, "")
	if _, err := Similar(context.Background(), script, []string{"unknown1", "unknown2"}, nil, 10); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := Similar(context.Background(), script, nil, nil, 10); err == nil {
		t.Fatal("no seeds should be an error")
	}
}
