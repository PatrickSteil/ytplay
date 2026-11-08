package yt

import (
	"context"
	"strconv"
	"sync"
)

// SearchAll searches YouTube and SoundCloud at the same time for up to n results
// each, and interleaves them (the first of YouTube, the first of SoundCloud, the
// second of YouTube, ...). A source that fails is named in failed and the other's
// results are returned; only if both fail is err set.
func SearchAll(ctx context.Context, ytdlpPath, query string, n int) (results []Result, failed map[Source]error, err error) {
	targets := []struct {
		src    Source
		prefix string
	}{{YouTube, "ytsearch"}, {SoundCloud, "scsearch"}}
	lists := make([][]Result, len(targets))
	errs := make([]error, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lists[i], errs[i] = runList(ctx, ytdlpPath, t.prefix+strconv.Itoa(n)+":"+query)
		}()
	}
	wg.Wait()
	failed = map[Source]error{}
	for i, e := range errs {
		if e != nil {
			failed[targets[i].src] = e
		}
	}
	if len(failed) == len(targets) {
		return nil, failed, errs[0]
	}
	return interleaveAll(lists), failed, nil
}

// interleaveAll takes one from each list in turn until all are used up.
func interleaveAll(lists [][]Result) []Result {
	var out []Result
	for i := 0; ; i++ {
		any := false
		for _, l := range lists {
			if i < len(l) {
				out = append(out, l[i])
				any = true
			}
		}
		if !any {
			return out
		}
	}
}
