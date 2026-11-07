package analyze

import (
	"sync"
	"sync/atomic"
)

// Pool is a fixed number of workers that every analysis running at the same
// time shares: however many songs are being analysed, no more than the pool's
// workers compute at once. That keeps a burst of analysis from taking over the
// machine.
type Pool struct {
	tokens chan struct{}
}

// NewPool returns a pool of workers goroutines (at least 1).
func NewPool(workers int) *Pool {
	return &Pool{tokens: make(chan struct{}, max(workers, 1))}
}

// Run calls fn(0) ... fn(n-1), each once, and returns when all have returned.
// At most the pool's workers calls run at the same time across all concurrent
// Runs of the pool. A nil pool runs them one after the other, in order, in the
// caller. The calls must not themselves wait for the pool.
func (p *Pool) Run(n int, fn func(i int)) {
	if n <= 0 {
		return
	}
	if p == nil {
		for i := 0; i < n; i++ {
			fn(i)
		}
		return
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < min(n, cap(p.tokens)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.tokens <- struct{}{} // wait for a free worker
			defer func() { <-p.tokens }()
			for {
				i := int(next.Add(1)) - 1
				if i >= n {
					return
				}
				fn(i)
			}
		}()
	}
	wg.Wait()
}
