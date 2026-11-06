package cover

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func solid(w, h int, c color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func TestRenderShape(t *testing.T) {
	out := Render(solid(4, 4, color.RGBA{200, 100, 50, 255}), 4, 2)
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%q", len(lines), out)
	}
	for i, l := range lines {
		if n := strings.Count(l, "▀"); n != 4 {
			t.Errorf("line %d has %d ▀, want 4", i, n)
		}
		if !strings.HasSuffix(l, "\x1b[0m") {
			t.Errorf("line %d does not end with a reset: %q", i, l)
		}
	}
}

func TestRenderUsesTopAsForegroundAndBottomAsBackground(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 2))
	img.Set(0, 0, color.RGBA{10, 20, 30, 255}) // top
	img.Set(0, 1, color.RGBA{40, 50, 60, 255}) // bottom
	out := Render(img, 1, 1)
	want := "\x1b[38;2;10;20;30m\x1b[48;2;40;50;60m▀\x1b[0m"
	if out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

func TestRenderScalesToRequestedSize(t *testing.T) {
	// A 320x180 source must come out as 32 cols x 9 rows.
	out := Render(solid(320, 180, color.RGBA{1, 2, 3, 255}), Cols, Rows)
	lines := strings.Split(out, "\n")
	if len(lines) != Rows {
		t.Fatalf("got %d lines, want %d", len(lines), Rows)
	}
	if n := strings.Count(lines[0], "▀"); n != Cols {
		t.Errorf("got %d columns, want %d", n, Cols)
	}
}

func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, solid(w, h, color.RGBA{90, 90, 200, 255}), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// fakeYT serves thumbnails; paths not in `ok` return 404.
func fakeYT(t *testing.T, hits *atomic.Int32, ok map[string][]byte) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		body, found := ok[r.URL.Path]
		if !found {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	old := baseURL
	baseURL = srv.URL
	t.Cleanup(func() { baseURL = old; srv.Close() })
}

func TestFetchUsesMqdefault(t *testing.T) {
	var hits atomic.Int32
	fakeYT(t, &hits, map[string][]byte{"/vi/abc/mqdefault.jpg": jpegBytes(t, 320, 180)})
	img, err := Fetch(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 320 || b.Dy() != 180 {
		t.Errorf("bounds = %v", b)
	}
	if hits.Load() != 1 {
		t.Errorf("hits = %d", hits.Load())
	}
}

func TestFetchFallsBackToHqdefaultAndCropsBlackBars(t *testing.T) {
	var hits atomic.Int32
	fakeYT(t, &hits, map[string][]byte{"/vi/abc/hqdefault.jpg": jpegBytes(t, 480, 360)})
	img, err := Fetch(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	// rows 45..314 inclusive of the 480x360 image: 270 rows.
	if b := img.Bounds(); b.Dx() != 480 || b.Dy() != 270 {
		t.Errorf("cropped size = %dx%d, want 480x270", b.Dx(), b.Dy())
	}
	if hits.Load() != 2 {
		t.Errorf("hits = %d, want 2", hits.Load())
	}
	// And it must render without panicking despite the non-zero origin.
	if out := Render(img, Cols, Rows); strings.Count(out, "\n") != Rows-1 {
		t.Errorf("render of cropped image has wrong line count")
	}
}

func TestFetchFailsWhenBothMissing(t *testing.T) {
	var hits atomic.Int32
	fakeYT(t, &hits, nil)
	if _, err := Fetch(context.Background(), "abc"); err == nil {
		t.Fatal("expected error")
	}
}

func TestFetchRejectsGarbage(t *testing.T) {
	var hits atomic.Int32
	fakeYT(t, &hits, map[string][]byte{
		"/vi/abc/mqdefault.jpg": []byte("not a jpeg"),
		"/vi/abc/hqdefault.jpg": []byte("not a jpeg"),
	})
	if _, err := Fetch(context.Background(), "abc"); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestCacheDownloadsEachCoverOnce(t *testing.T) {
	var hits atomic.Int32
	fakeYT(t, &hits, map[string][]byte{"/vi/abc/mqdefault.jpg": jpegBytes(t, 320, 180)})
	c := NewCache()
	a, err := c.Get(context.Background(), "abc")
	if err != nil || a == "" {
		t.Fatalf("Get: %q, %v", a, err)
	}
	b, err := c.Get(context.Background(), "abc")
	if err != nil || b != a {
		t.Fatalf("second Get differs: %v", err)
	}
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1", hits.Load())
	}
}

func TestCacheDoesNotRetryFailures(t *testing.T) {
	var hits atomic.Int32
	fakeYT(t, &hits, nil)
	c := NewCache()
	if _, err := c.Get(context.Background(), "abc"); err == nil {
		t.Fatal("expected error")
	}
	after := hits.Load()
	if _, err := c.Get(context.Background(), "abc"); err == nil {
		t.Fatal("expected cached error")
	}
	if hits.Load() != after {
		t.Errorf("retried a failed download: %d -> %d requests", after, hits.Load())
	}
}
