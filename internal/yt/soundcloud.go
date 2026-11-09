package yt

import (
	"context"
	"strconv"
	"sync"
)

// SearchAll searches YouTube and SoundCloud at the same time for up to n results
// each, and lists them one after the other: the YouTube results, then the
// SoundCloud ones. A source that fails is named in failed and the other's
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
	for _, l := range lists {
		results = append(results, l...)
	}
	return results, failed, nil
}
