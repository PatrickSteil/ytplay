package autodj

import (
	"math"
	"strconv"
	"strings"
)

// Ordering a queue: the songs are laid out so that each follows the one before
// it as smoothly as it can, as a DJ would pick them, instead of sorted by one
// number. A "sort by BPM" jumps from 155 to the slowest song of the queue (138)
// and then climbs; this keeps the tempo and the key drifting from song to song.

// Track is what the ordering knows of a song.
type Track struct {
	BPM     float64 // 0 if unknown
	Camelot string  // e.g. "11A", "" if unknown
}

// Costs are in "percent of tempo": a transition between songs 1% apart in tempo
// costs 1.
const (
	// UnknownCost is what a transition costs when the tempo of either song is
	// not known: a middling price, so such songs are not placed anywhere in
	// particular (they are put at the end, see Arrange).
	UnknownCost = 8.0
	// keyStep is the price of each step the keys are apart on the Camelot wheel
	// beyond the neighbours (which blend well).
	keyStep = 1.5
	// unknownKey is what a transition costs when a key is not known.
	unknownKey = 1.0
	// maxKeyCost caps the price of clashing keys.
	maxKeyCost = 4.0
)

// tempoGap is how far apart in percent two tempos are, double and half time
// counted as no gap at all (70 and 140 BPM go together).
func tempoGap(a, b float64) float64 {
	r := b / a
	best := math.Abs(math.Log(r))
	for _, f := range []float64{0.5, 2} {
		best = math.Min(best, math.Abs(math.Log(r*f)))
	}
	return (math.Exp(best) - 1) * 100
}

// camelot parses "11A" into 11 and 'A'.
func camelot(code string) (n int, letter byte, ok bool) {
	if len(code) < 2 {
		return 0, 0, false
	}
	letter = strings.ToUpper(code[len(code)-1:])[0]
	n, err := strconv.Atoi(code[:len(code)-1])
	if err != nil || n < 1 || n > 12 || (letter != 'A' && letter != 'B') {
		return 0, 0, false
	}
	return n, letter, true
}

// keyCost is the price of going from one key to another: nothing for the same
// key, its relative major or minor, or the next one up or down the Camelot
// wheel (these mix well), then growing with the distance.
func keyCost(a, b string) float64 {
	na, la, oka := camelot(a)
	nb, lb, okb := camelot(b)
	if !oka || !okb {
		return unknownKey
	}
	d := na - nb
	if d < 0 {
		d = -d
	}
	d = min(d, 12-d)
	if la != lb {
		d++ // the relative key is one step, a diagonal move costs one more
		if na == nb {
			d = 0
		}
	}
	if d <= 1 {
		return 0
	}
	return math.Min(float64(d-1)*keyStep, maxKeyCost)
}

// Cost is the price of playing b after a: the tempo gap that has to be bridged
// plus a penalty for clashing keys. The beat-match limit (MaxTempoGap, 6%) is
// where a smooth transition stops being possible, so beyond it every further
// percent costs double: a fast climb is worse than a few small steps.
func Cost(a, b Track) float64 {
	if a.BPM <= 0 || b.BPM <= 0 {
		return UnknownCost
	}
	gap := tempoGap(a.BPM, b.BPM)
	limit := MaxTempoGap * 100
	cost := math.Min(gap, limit) + 2*math.Max(gap-limit, 0)
	return cost + keyCost(a.Camelot, b.Camelot)
}

// pathCost is the total price of playing tracks in order, after first if
// first >= 0 (an index into tracks that is not part of order).
func pathCost(tracks []Track, first int, order []int) float64 {
	total, prev := 0.0, first
	for _, i := range order {
		if prev >= 0 {
			total += Cost(tracks[prev], tracks[i])
		}
		prev = i
	}
	return total
}

// Arrange orders the songs rest (indexes into tracks) to follow the song first
// (an index, or -1 for none: then the best opener is chosen too) as cheaply as
// it can find. Songs without a known tempo keep their order at the end. The
// result is deterministic.
func Arrange(tracks []Track, first int, rest []int) []int {
	var known, unknown []int
	for _, i := range rest {
		if tracks[i].BPM > 0 {
			known = append(known, i)
		} else {
			unknown = append(unknown, i)
		}
	}
	if first >= 0 && tracks[first].BPM <= 0 {
		// Nothing to follow: sort by tempo from the slowest, a plain warm-up.
		known = sortedByTempo(tracks, known)
		return append(known, unknown...)
	}
	var best []int
	bestCost := math.Inf(1)
	try := func(start int) {
		order := nearest(tracks, first, start, known)
		order = improve(tracks, first, order)
		c := pathCost(tracks, first, order)
		if c < bestCost-1e-9 {
			best, bestCost = order, c
		}
	}
	if first >= 0 {
		try(-1)
	} else {
		// Free opener: every song gets a turn, in order of tempo so that ties
		// go to the slower one (the set starts low).
		for _, s := range sortedByTempo(tracks, known) {
			try(s)
		}
	}
	return append(best, unknown...)
}

func sortedByTempo(tracks []Track, ids []int) []int {
	out := append([]int(nil), ids...)
	for i := 1; i < len(out); i++ { // insertion sort: stable, and the lists are short
		for j := i; j > 0 && tracks[out[j]].BPM < tracks[out[j-1]].BPM; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// nearest builds an order greedily: each step takes the cheapest song to follow
// the last one. start >= 0 forces the first song of the order.
func nearest(tracks []Track, first, start int, ids []int) []int {
	left := append([]int(nil), ids...)
	var order []int
	prev := first
	take := func(k int) {
		prev = left[k]
		order = append(order, prev)
		left = append(left[:k], left[k+1:]...)
	}
	if start >= 0 {
		for k, i := range left {
			if i == start {
				take(k)
				break
			}
		}
	}
	for len(left) > 0 {
		bestK, bestC := 0, math.Inf(1)
		for k, i := range left {
			c := 0.0
			if prev >= 0 {
				c = Cost(tracks[prev], tracks[i])
			} else {
				c = tracks[i].BPM / 1e6 // no one to follow: the slowest
			}
			if c < bestC-1e-12 {
				bestK, bestC = k, c
			}
		}
		take(bestK)
	}
	return order
}

// improve makes an order cheaper by moving one song, or reversing a stretch,
// until no such change helps. The first song is only moved when first < 0 and
// start was not forced (see Arrange: it is kept here, the opener is chosen by
// trying them all).
func improve(tracks []Track, first int, order []int) []int {
	order = append([]int(nil), order...)
	n := len(order)
	lo := 0
	if first < 0 {
		lo = 1 // the opener was chosen by the caller
	}
	cur := pathCost(tracks, first, order)
	for improved := true; improved; {
		improved = false
		// move song i to position j
		for i := lo; i < n; i++ {
			for j := lo; j < n; j++ {
				if i == j {
					continue
				}
				cand := append([]int(nil), order...)
				v := cand[i]
				cand = append(cand[:i], cand[i+1:]...)
				cand = append(cand[:j], append([]int{v}, cand[j:]...)...)
				if c := pathCost(tracks, first, cand); c < cur-1e-9 {
					order, cur, improved = cand, c, true
				}
			}
		}
		// reverse the stretch i..j
		for i := lo; i < n; i++ {
			for j := i + 1; j < n; j++ {
				cand := append([]int(nil), order...)
				for a, b := i, j; a < b; a, b = a+1, b-1 {
					cand[a], cand[b] = cand[b], cand[a]
				}
				if c := pathCost(tracks, first, cand); c < cur-1e-9 {
					order, cur, improved = cand, c, true
				}
			}
		}
	}
	return order
}
