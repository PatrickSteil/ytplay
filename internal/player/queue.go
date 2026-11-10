package player

import (
	"errors"
	"fmt"
	"slices"
)

// queue is the list of URLs to play and the index of the one playing now
// (-1 when nothing is). Songs that were played stay in the list, like in an
// mpv playlist. It does no I/O.
type queue struct {
	urls []string
	pos  int
}

func (q *queue) append(urls ...string) { q.urls = append(q.urls, urls...) }

// insertAfterCurrent puts url right behind the playing entry (at the end when
// nothing is playing) and returns its index.
func (q *queue) insertAfterCurrent(url string) int {
	i := len(q.urls)
	if q.pos >= 0 {
		i = q.pos + 1
	}
	q.urls = slices.Insert(q.urls, i, url)
	return i
}

// remove deletes entry i. If that is the playing entry, pos is left on the
// entry that followed it, or -1 if there is none.
func (q *queue) remove(i int) error {
	if i < 0 || i >= len(q.urls) {
		return fmt.Errorf("no queue entry %d", i)
	}
	q.urls = slices.Delete(q.urls, i, i+1)
	switch {
	case i < q.pos:
		q.pos--
	case i == q.pos && i >= len(q.urls):
		q.pos = -1
	}
	return nil
}

// swap exchanges two neighbouring entries; pos follows the playing entry.
func (q *queue) swap(i, j int) error {
	if i < 0 || j < 0 || i >= len(q.urls) || j >= len(q.urls) || i-j != 1 && j-i != 1 {
		return fmt.Errorf("cannot swap queue entries %d and %d", i, j)
	}
	q.urls[i], q.urls[j] = q.urls[j], q.urls[i]
	switch q.pos {
	case i:
		q.pos = j
	case j:
		q.pos = i
	}
	return nil
}

// clearExceptCurrent keeps only the playing entry (everything when none plays).
func (q *queue) clearExceptCurrent() {
	if q.pos < 0 {
		q.urls = nil
		return
	}
	q.urls = []string{q.urls[q.pos]}
	q.pos = 0
}

// upcoming returns a copy of the entries after the playing one.
func (q *queue) upcoming() []string {
	if q.pos < 0 {
		return nil
	}
	return slices.Clone(q.urls[q.pos+1:])
}

// setUpcoming replaces the entries after the playing one (all of them when
// none plays: nothing, there must be no urls then).
func (q *queue) setUpcoming(urls []string) {
	if q.pos < 0 {
		return
	}
	q.urls = append(slices.Clone(q.urls[:q.pos+1]), urls...)
}

// reorderUpcoming puts the entries after the playing one in the given order.
// urls must hold exactly the upcoming URLs, duplicates counted.
func (q *queue) reorderUpcoming(urls []string) error {
	if q.pos < 0 {
		if len(urls) != 0 {
			return errors.New("nothing is playing, so there are no upcoming songs")
		}
		return nil
	}
	have := q.urls[q.pos+1:]
	if len(urls) != len(have) {
		return fmt.Errorf("got %d songs to reorder, %d are upcoming", len(urls), len(have))
	}
	left := map[string]int{}
	for _, u := range have {
		left[u]++
	}
	for _, u := range urls {
		if left[u]--; left[u] < 0 {
			return fmt.Errorf("%q is not upcoming (or listed too often)", u)
		}
	}
	copy(have, urls)
	return nil
}

// reorderFromCurrent puts the playing entry and the ones after it in the given
// order; urls must hold exactly those URLs, duplicates counted. It reports
// whether the playing entry is now a different song.
func (q *queue) reorderFromCurrent(urls []string) (changed bool, err error) {
	if q.pos < 0 {
		return false, errors.New("nothing is playing")
	}
	have := q.urls[q.pos:]
	if len(urls) != len(have) {
		return false, fmt.Errorf("got %d songs to arrange, %d are playing or upcoming", len(urls), len(have))
	}
	left := map[string]int{}
	for _, u := range have {
		left[u]++
	}
	for _, u := range urls {
		if left[u]--; left[u] < 0 {
			return false, fmt.Errorf("%q is not queued (or listed too often)", u)
		}
	}
	changed = urls[0] != have[0]
	copy(have, urls)
	return changed, nil
}
