// Package cover downloads YouTube thumbnails and draws them with Unicode
// half blocks and 24-bit colour.
package cover

import (
	"context"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg" // register the JPEG decoder
	"net/http"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
)

// Default cover size in terminal cells.
const (
	Cols = 32
	Rows = 9
)

// baseURL is a variable so tests can point it at a local server.
var baseURL = "https://i.ytimg.com"

var httpClient = &http.Client{Timeout: 10 * time.Second}

func download(ctx context.Context, path string) (image.Image, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download cover: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download cover: %s", resp.Status)
	}
	img, _, err := image.Decode(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("decode cover: %w", err)
	}
	return img, nil
}

// Fetch downloads the 16:9 mqdefault thumbnail for a video. If that fails it
// falls back to hqdefault and crops its black bars.
func Fetch(ctx context.Context, id string) (image.Image, error) {
	img, err := download(ctx, "/vi/"+id+"/mqdefault.jpg")
	if err == nil {
		return img, nil
	}
	if ctx.Err() != nil {
		return nil, err
	}
	hq, hqErr := download(ctx, "/vi/"+id+"/hqdefault.jpg")
	if hqErr != nil {
		return nil, fmt.Errorf("%w; fallback: %v", err, hqErr)
	}
	return cropBars(hq), nil
}

// cropBars removes the black bars of a 4:3 hqdefault image (480x360): the
// picture occupies rows 45..314.
func cropBars(img image.Image) image.Image {
	b := img.Bounds()
	if b.Dx() != 480 || b.Dy() != 360 {
		return img // unexpected size: keep as is
	}
	r := image.Rect(b.Min.X, b.Min.Y+45, b.Max.X, b.Min.Y+315)
	if s, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return s.SubImage(r)
	}
	dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(dst, dst.Bounds(), img, r.Min, draw.Src)
	return dst
}

// Render draws img as cols x rows terminal cells. Each cell shows two vertical
// pixels: the top one as the foreground of "▀", the bottom one as background.
// Lines are separated by "\n" (no trailing newline) and each ends with a
// colour reset.
func Render(img image.Image, cols, rows int) string {
	dst := image.NewRGBA(image.Rect(0, 0, cols, rows*2))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), xdraw.Src, nil)

	var sb strings.Builder
	for y := 0; y < rows*2; y += 2 {
		if y > 0 {
			sb.WriteByte('\n')
		}
		for x := 0; x < cols; x++ {
			tr, tg, tb, _ := dst.At(x, y).RGBA()
			br, bg, bb, _ := dst.At(x, y+1).RGBA()
			fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀",
				tr>>8, tg>>8, tb>>8, br>>8, bg>>8, bb>>8)
		}
		sb.WriteString("\x1b[0m")
	}
	return sb.String()
}

// Cache downloads and renders each cover at most once.
type Cache struct {
	mu   sync.Mutex
	done map[string]result
}

type result struct {
	rendered string
	err      error
}

// NewCache returns an empty cache.
func NewCache() *Cache { return &Cache{done: map[string]result{}} }

// Get returns the rendered default-size cover for a video, downloading it on
// first use. Failures are cached too, so a broken cover is never retried.
func (c *Cache) Get(ctx context.Context, id string) (string, error) {
	c.mu.Lock()
	if r, ok := c.done[id]; ok {
		c.mu.Unlock()
		return r.rendered, r.err
	}
	c.mu.Unlock()

	img, err := Fetch(ctx, id)
	r := result{err: err}
	if err == nil {
		r.rendered = Render(img, Cols, Rows)
	}
	if ctx.Err() == nil { // a cancelled download says nothing about the cover
		c.mu.Lock()
		c.done[id] = r
		c.mu.Unlock()
	}
	return r.rendered, r.err
}
