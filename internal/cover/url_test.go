package cover

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

// artServer serves one PNG at /art.png and counts the requests; images from it
// are allowed as thumbnails for the test.
func artServer(t *testing.T, img image.Image) (string, *atomic.Int32) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write(buf.Bytes())
	}))
	old := thumbAllowed
	thumbAllowed = func(u *url.URL) bool { return u.Host == srv.Listener.Addr().String() }
	t.Cleanup(func() { thumbAllowed = old; srv.Close() })
	return srv.URL + "/art.png", &hits
}

// bands is a square image: red rows, then green rows in the middle, then blue.
func bands(n int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		c := color.RGBA{0, 255, 0, 255}
		switch {
		case y < n*22/100:
			c = color.RGBA{255, 0, 0, 255}
		case y >= n*78/100:
			c = color.RGBA{0, 0, 255, 255}
		}
		for x := 0; x < n; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func TestFetchURLCropsASquareToSixteenByNineFromTheCentre(t *testing.T) {
	u, _ := artServer(t, bands(100))
	img, err := FetchURL(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 100 || b.Dy() != 56 {
		t.Fatalf("size %v, want 100x56", b)
	}
	b := img.Bounds()
	for _, p := range [][2]int{{0, 0}, {99, 0}, {50, 28}, {0, 55}, {99, 55}} {
		if r, g, bl, _ := img.At(b.Min.X+p[0], b.Min.Y+p[1]).RGBA(); r != 0 || bl != 0 || g == 0 {
			t.Errorf("pixel %v is not from the green middle", p)
		}
	}
}

func TestFetchURLKeepsASixteenByNineImageAsItIs(t *testing.T) {
	u, _ := artServer(t, solid(160, 90, color.RGBA{9, 9, 9, 255}))
	img, err := FetchURL(context.Background(), u)
	if err != nil || img.Bounds().Dx() != 160 || img.Bounds().Dy() != 90 {
		t.Errorf("err %v size %v", err, img.Bounds())
	}
}

func TestThumbnailsComeOnlyFromKnownHosts(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://i1.sndcdn.com/artworks-x-t500x500.jpg": true,
		"https://i.ytimg.com/vi/abc/mqdefault.jpg":      true,
		"http://i1.sndcdn.com/a.jpg":                    false,
		"https://sndcdn.com.evil.example/a.jpg":         false,
		"https://evil.example/sndcdn.com/a.jpg":         false,
		"https://localhost/a.jpg":                       false,
		"file:///etc/passwd":                            false,
	} {
		u, _ := url.Parse(raw)
		if got := thumbAllowed(u); got != want {
			t.Errorf("thumbAllowed(%q) = %v, want %v", raw, got, want)
		}
	}
	if _, err := FetchURL(context.Background(), "https://evil.example/a.jpg"); err == nil {
		t.Error("a thumbnail from an unknown host was fetched")
	}
}

func TestCacheGetsASoundCloudCoverFromItsThumbnailAndNeverFromYouTube(t *testing.T) {
	u, hits := artServer(t, bands(100))
	c := NewCache()
	out, err := c.Get(context.Background(), "sc:u/t", u)
	if err != nil || out == "" || hits.Load() != 1 {
		t.Fatalf("err %v hits %d", err, hits.Load())
	}
	if _, err := c.Get(context.Background(), "sc:u/t", u); err != nil || hits.Load() != 1 {
		t.Errorf("a second Get fetched again: err %v hits %d", err, hits.Load())
	}
	// no artwork given for a SoundCloud song: nothing to fetch, and no YouTube guess
	var yt atomic.Int32
	ytSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { yt.Add(1); http.NotFound(w, r) }))
	defer ytSrv.Close()
	oldBase := baseURL
	baseURL = ytSrv.URL
	defer func() { baseURL = oldBase }()
	if _, err := c.Get(context.Background(), "sc:u/other", ""); err == nil || hits.Load() != 1 || yt.Load() != 0 {
		t.Errorf("err %v hits %d youtube requests %d, want an error without any request", err, hits.Load(), yt.Load())
	}
}
