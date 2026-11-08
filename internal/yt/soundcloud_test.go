package yt

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

const scEntry = `{"id":"2427716250","title":"Mix","extractor_key":"Soundcloud","uploader":"ralph","duration":3639.06,"webpage_url":"https://soundcloud.com/ralph/mix","thumbnails":[{"id":"large","url":"https://i1.sndcdn.com/a-large.jpg"},{"id":"t500x500","url":"https://i1.sndcdn.com/a-t500.jpg"}]}`

// bothSources is a fake yt-dlp that answers a YouTube and a SoundCloud search.
func bothSources(t *testing.T, yt, sc string) (script, argsFile string) {
	return recordingYtdlp(t, `
for a in "$@"; do last="$a"; done
case "$last" in
  ytsearch*) `+yt+` ;;
  scsearch*) `+sc+` ;;
esac
`)
}

func TestSearchAllInterleavesBothSources(t *testing.T) {
	script, argsFile := bothSources(t, `
echo '{"id":"a1","title":"Y1","channel":"C","duration":10}'
echo '{"id":"a2","title":"Y2","channel":"C","duration":10}'
echo '{"id":"a3","title":"Y3","channel":"C","duration":10}'`, `
echo '`+scEntry+`'
echo '{"id":"7","title":"S2","extractor_key":"Soundcloud","uploader":"u","duration":5,"webpage_url":"https://soundcloud.com/u/s2"}'`)
	got, failed, err := SearchAll(context.Background(), script, "q", 10)
	if err != nil || len(failed) != 0 {
		t.Fatalf("err %v failed %v", err, failed)
	}
	var ids []string
	for _, r := range got {
		ids = append(ids, r.ID)
	}
	if want := "a1 sc:ralph/mix a2 sc:u/s2 a3"; strings.Join(ids, " ") != want {
		t.Errorf("order %q, want %q", strings.Join(ids, " "), want)
	}
	sc := got[1]
	if sc.Source != SoundCloud || sc.Title != "Mix" || sc.Channel != "ralph" || sc.Duration != 3639.06 {
		t.Errorf("soundcloud result %+v", sc)
	}
	if sc.Thumb != "https://i1.sndcdn.com/a-t500.jpg" || sc.ThumbURL() != sc.Thumb {
		t.Errorf("thumb %q", sc.Thumb)
	}
	if got[0].Source != YouTube || got[0].ThumbURL() != "https://i.ytimg.com/vi/a1/mqdefault.jpg" {
		t.Errorf("youtube result %+v", got[0])
	}
	if sc.URL() != "https://soundcloud.com/ralph/mix" {
		t.Errorf("url %q", sc.URL())
	}
	args, _ := os.ReadFile(argsFile)
	for _, want := range []string{"ytsearch10:q", "scsearch10:q"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("yt-dlp was not asked for %s:\n%s", want, args)
		}
	}
}

func TestSearchAllShowsTheWorkingSourceWhenTheOtherFails(t *testing.T) {
	script, _ := bothSources(t, `echo '{"id":"a1","title":"Y1","duration":10}'`, `echo "ERROR: no client id" >&2; exit 1`)
	got, failed, err := SearchAll(context.Background(), script, "q", 5)
	if err != nil || len(got) != 1 || got[0].ID != "a1" {
		t.Fatalf("got %v err %v", got, err)
	}
	if e := failed[SoundCloud]; e == nil || !strings.Contains(e.Error(), "no client id") || failed[YouTube] != nil {
		t.Errorf("failed = %v", failed)
	}
	script, _ = bothSources(t, `echo "ERROR: offline" >&2; exit 1`, `echo '`+scEntry+`'`)
	got, failed, err = SearchAll(context.Background(), script, "q", 5)
	if err != nil || len(got) != 1 || failed[YouTube] == nil {
		t.Errorf("youtube failing: got %v failed %v err %v", got, failed, err)
	}
}

func TestSearchAllFailsWhenBothSourcesFail(t *testing.T) {
	script, _ := bothSources(t, `echo "ERROR: a" >&2; exit 1`, `echo "ERROR: b" >&2; exit 1`)
	got, _, err := SearchAll(context.Background(), script, "q", 5)
	if err == nil || got != nil {
		t.Errorf("got %v err %v, want an error", got, err)
	}
}

func TestSearchAllAsksBothSourcesAtTheSameTime(t *testing.T) {
	dir := t.TempDir()
	wait := func(me, other string) string {
		return `touch ` + dir + `/` + me + `; i=0; while [ ! -e ` + dir + `/` + other + ` ] && [ $i -lt 50 ]; do sleep 0.1; i=$((i+1)); done
[ -e ` + dir + `/` + other + ` ] || { echo "ERROR: ran one after the other" >&2; exit 1; }`
	}
	script, _ := bothSources(t, wait("yt", "sc")+`; echo '{"id":"a1","title":"Y","duration":1}'`, wait("sc", "yt")+`; echo '`+scEntry+`'`)
	got, failed, err := SearchAll(context.Background(), script, "q", 5)
	if err != nil || len(failed) != 0 || len(got) != 2 {
		t.Errorf("got %v failed %v err %v", got, failed, err)
	}
}

func TestSearchAllSkipsSoundCloudEntriesThatAreNotTracks(t *testing.T) {
	script, _ := bothSources(t, ``, `
echo '{"id":"1","title":"no url","extractor_key":"Soundcloud","uploader":"u","duration":5}'
echo '{"id":"2","title":"a profile","extractor_key":"Soundcloud","uploader":"u","duration":5,"webpage_url":"https://soundcloud.com/u"}'
echo '{"id":"3","title":"a set","extractor_key":"Soundcloud","uploader":"u","duration":5,"webpage_url":"https://soundcloud.com/u/sets/x"}'
echo '{"id":"4","title":"elsewhere","extractor_key":"Soundcloud","uploader":"u","duration":5,"webpage_url":"https://evil.example/u/x"}'
echo '`+scEntry+`'`)
	got, _, err := SearchAll(context.Background(), script, "q", 5)
	if err != nil || len(got) != 1 || got[0].ID != "sc:ralph/mix" {
		t.Errorf("got %v err %v, want only the track", got, err)
	}
}

func TestSearchAllStopsWhenCancelled(t *testing.T) {
	script, _ := bothSources(t, `sleep 5`, `sleep 5`)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	start := time.Now()
	if _, _, err := SearchAll(ctx, script, "q", 5); err == nil || time.Since(start) > 3*time.Second {
		t.Errorf("err %v after %v", err, time.Since(start))
	}
}

func TestASoundCloudTrackWithoutArtworkHasNoThumbnailURL(t *testing.T) {
	r, _, ok := parseEntry([]byte(`{"id":"7","title":"S","extractor_key":"Soundcloud","webpage_url":"https://soundcloud.com/u/s"}`))
	if !ok || r.ThumbURL() != "" {
		t.Errorf("ok %v thumb %q: a SoundCloud track must not get a YouTube thumbnail URL", ok, r.ThumbURL())
	}
}
