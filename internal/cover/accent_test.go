package cover

import (
	"image"
	"image/color"
	"image/draw"
	"strings"
	"testing"

	"github.com/lucasb-eyer/go-colorful"
)

func swatch(c color.Color) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 64, 36))
	draw.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
	return img
}

func hueOf(t *testing.T, hex string) (h, s, l float64) {
	t.Helper()
	c, err := colorful.Hex(hex)
	if err != nil {
		t.Fatalf("not a colour: %q", hex)
	}
	return c.Hsl()
}

func hueDist(a, b float64) float64 {
	d := a - b
	for d > 180 {
		d -= 360
	}
	for d < -180 {
		d += 360
	}
	if d < 0 {
		d = -d
	}
	return d
}

func TestAccentFollowsTheHueOfTheCover(t *testing.T) {
	for name, tc := range map[string]struct {
		c   color.RGBA
		hue float64
	}{
		"red":    {color.RGBA{200, 30, 30, 255}, 0},
		"orange": {color.RGBA{220, 120, 20, 255}, 30},
		"green":  {color.RGBA{30, 180, 60, 255}, 134},
		"blue":   {color.RGBA{30, 60, 200, 255}, 230},
		"pink":   {color.RGBA{220, 50, 150, 255}, 328},
	} {
		acc, frame, ok := Accent(Render(swatch(tc.c), 10, 5))
		if !ok {
			t.Errorf("%s: no accent", name)
			continue
		}
		for what, hex := range map[string]string{"accent": acc, "frame": frame} {
			if h, _, _ := hueOf(t, hex); hueDist(h, tc.hue) > 12 {
				t.Errorf("%s: %s %s has hue %.0f, want about %.0f", name, what, hex, h, tc.hue)
			}
		}
	}
}

// A dark or a pale cover must still give a colour that can be read on a dark
// terminal, and the frame is quieter than the accent.
func TestAccentIsReadableAndTheFrameQuieter(t *testing.T) {
	for name, c := range map[string]color.RGBA{
		"very dark red": {60, 5, 5, 255},
		"pale blue":     {170, 190, 255, 255},
		"saturated":     {255, 0, 0, 255},
	} {
		acc, frame, ok := Accent(Render(swatch(c), 10, 5))
		if !ok {
			t.Errorf("%s: no accent", name)
			continue
		}
		_, as, al := hueOf(t, acc)
		_, fs, fl := hueOf(t, frame)
		if al < 0.55 || al > 0.8 || as < 0.4 {
			t.Errorf("%s: accent %s has saturation %.2f, lightness %.2f: not readable", name, acc, as, al)
		}
		if fl >= al || fs >= as {
			t.Errorf("%s: frame %s should be duller and darker than accent %s", name, frame, acc)
		}
	}
}

func TestAccentIgnoresTheBackgroundAndTakesTheDominantColour(t *testing.T) {
	// a black cover with a bit of green in a corner and more red
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, 0, 30, 30), image.NewUniform(color.RGBA{0, 200, 0, 255}), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(50, 50, 100, 100), image.NewUniform(color.RGBA{200, 0, 0, 255}), image.Point{}, draw.Src)
	acc, _, ok := Accent(Render(img, 20, 10))
	if !ok {
		t.Fatal("no accent")
	}
	if h, _, _ := hueOf(t, acc); hueDist(h, 0) > 12 {
		t.Errorf("accent %s has hue %.0f, want red (the larger coloured area, not the black background)", acc, h)
	}
}

func TestAccentOfAGrayOrUnreadableCoverIsNone(t *testing.T) {
	for name, rendered := range map[string]string{
		"black":     Render(swatch(color.Black), 10, 5),
		"white":     Render(swatch(color.White), 10, 5),
		"gray":      Render(swatch(color.RGBA{128, 128, 128, 255}), 10, 5),
		"empty":     "",
		"text only": "COVER-a\nCOVER-a",
		"truncated": "\x1b[38;2;200;30m▀",
	} {
		if acc, frame, ok := Accent(rendered); ok {
			t.Errorf("%s: got accent %s, frame %s, want none", name, acc, frame)
		}
	}
}

func TestAccentIsAHexColour(t *testing.T) {
	acc, frame, _ := Accent(Render(swatch(color.RGBA{30, 60, 200, 255}), 10, 5))
	for _, hex := range []string{acc, frame} {
		if !strings.HasPrefix(hex, "#") || len(hex) != 7 {
			t.Errorf("%q is not #rrggbb", hex)
		}
	}
}
