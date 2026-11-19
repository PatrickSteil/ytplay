package cover

import (
	"math"
	"regexp"
	"strconv"

	"github.com/lucasb-eyer/go-colorful"
)

// Accent derives the colours the screen is drawn in from a cover that Render
// produced: the colour that stands out most in the picture, as an accent (for
// highlights and the focused frame) and a duller, darker one of the same hue
// (for the frames of what is not focused), both as "#rrggbb". Both are set to
// a lightness and saturation that read on a dark terminal, whatever the cover
// looks like. ok is false when the cover has no colour to take (black, white
// or gray artwork, or something that is not a rendered cover).
func Accent(rendered string) (accent, frame string, ok bool) {
	const hueBins = 24
	type bin struct{ weight, sin, cos, sat float64 }
	var bins [hueBins]bin
	total, coloured := 0, 0

	for _, m := range pixelCode.FindAllStringSubmatch(rendered, -1) {
		var v [3]float64
		for i := range v {
			n, _ := strconv.Atoi(m[i+1])
			v[i] = float64(min(n, 255)) / 255
		}
		total++
		h, s, val := colorful.Color{R: v[0], G: v[1], B: v[2]}.Hsv()
		// Near-black, near-white and gray pixels are background, not the colour of the cover.
		if s < 0.25 || val < 0.15 {
			continue
		}
		coloured++
		w := s * val
		rad := h * math.Pi / 180
		b := &bins[int(h/360*hueBins)%hueBins]
		b.weight += w
		b.sin += w * math.Sin(rad)
		b.cos += w * math.Cos(rad)
		b.sat += w * s
	}
	if total == 0 || coloured*20 < total { // under 5 % coloured
		return "", "", false
	}

	// The hue range with the most weight: a bin and its two neighbours, so that
	// a colour that falls on the edge of a bin is not split in two.
	best, bestW := 0, 0.0
	for i := range bins {
		w := bins[i].weight + bins[(i+1)%hueBins].weight + bins[(i+hueBins-1)%hueBins].weight
		if w > bestW {
			best, bestW = i, w
		}
	}
	var sinSum, cosSum, satSum, wSum float64
	for _, i := range []int{best - 1 + hueBins, best, best + 1} {
		b := bins[i%hueBins]
		sinSum, cosSum, satSum, wSum = sinSum+b.sin, cosSum+b.cos, satSum+b.sat, wSum+b.weight
	}
	hue := math.Atan2(sinSum, cosSum) * 180 / math.Pi
	if hue < 0 {
		hue += 360
	}
	sat := satSum / wSum

	accent = colorful.Hsl(hue, clamp(sat, 0.5, 0.9), 0.66).Clamped().Hex()
	frame = colorful.Hsl(hue, clamp(sat*0.4, 0.15, 0.35), 0.45).Clamped().Hex()
	return accent, frame, true
}

func clamp(v, lo, hi float64) float64 { return math.Min(math.Max(v, lo), hi) }

// pixelCode matches the foreground and background colour codes that Render
// writes: each is one pixel of the cover.
var pixelCode = regexp.MustCompile(`\x1b\[[34]8;2;(\d+);(\d+);(\d+)m`)
