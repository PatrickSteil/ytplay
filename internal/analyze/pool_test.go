package analyze

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPoolRunsEveryIndexExactlyOnce(t *testing.T) {
	counts := make([]atomic.Int32, 100)
	NewPool(4).Run(len(counts), func(i int) { counts[i].Add(1) })
	for i := range counts {
		if n := counts[i].Load(); n != 1 {
			t.Fatalf("index %d ran %d times", i, n)
		}
	}
}

func TestNilPoolRunsInOrderInTheCaller(t *testing.T) {
	var order []int
	var p *Pool
	p.Run(5, func(i int) { order = append(order, i) }) // no locking needed: it is all one goroutine
	if len(order) != 5 {
		t.Fatalf("ran %v", order)
	}
	for i, v := range order {
		if v != i {
			t.Errorf("ran %v, want 0..4 in order", order)
			break
		}
	}
}

func TestPoolRunOfNothingReturns(t *testing.T) {
	NewPool(2).Run(0, func(int) { t.Error("must not run") })
	var p *Pool
	p.Run(0, func(int) { t.Error("must not run") })
}

func TestPoolWithNoWorkersStillRuns(t *testing.T) {
	var n atomic.Int32
	NewPool(0).Run(7, func(int) { n.Add(1) })
	if n.Load() != 7 {
		t.Errorf("ran %d of 7", n.Load())
	}
}

// The workers are the pool's limit across all callers, not per call.
func TestPoolNeverRunsMoreThanItsWorkersAtOnce(t *testing.T) {
	const workers = 3
	p := NewPool(workers)
	var running, most atomic.Int32
	var wg sync.WaitGroup
	for c := 0; c < 8; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.Run(20, func(int) {
				n := running.Add(1)
				for {
					m := most.Load()
					if n <= m || most.CompareAndSwap(m, n) {
						break
					}
				}
				time.Sleep(time.Millisecond)
				running.Add(-1)
			})
		}()
	}
	wg.Wait()
	if m := most.Load(); m > workers {
		t.Errorf("%d tasks ran at once, the pool has %d workers", m, workers)
	}
	if most.Load() < 2 {
		t.Errorf("only %d task at a time: the pool did not run anything in parallel", most.Load())
	}
}

// More callers than workers must all get through.
func TestPoolManyCallersDoNotDeadlock(t *testing.T) {
	p := NewPool(2)
	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for c := 0; c < 8; c++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				p.Run(10, func(int) { time.Sleep(time.Millisecond) })
			}()
		}
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("8 callers on a pool of 2 did not finish in 10 s")
	}
}
