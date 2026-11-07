package analyze

import (
	"fmt"
	"reflect"
	"testing"
)

// noiseSamples is n samples of seeded white noise.
func noiseSamples(n int, seed lcg) []float32 {
	x := make([]float32, n)
	for i := range x {
		x[i] = float32(0.1 * seed.next())
	}
	return x
}

// noiseTrack is seconds of seeded white noise.
func noiseTrack(seconds float64, seed lcg) []float32 {
	return noiseSamples(int(seconds*float64(SampleRate)), seed)
}

// singleBatch runs fn with every frame in one batch: the plain one-pass loop
// that the batched, parallel analysis must reproduce exactly.
func singleBatch(fn func()) {
	old := frameBatch
	frameBatch = 1 << 30
	defer func() { frameBatch = old }()
	fn()
}

// Batched and parallel analysis is the same analysis: every value is computed by
// the same operations in the same order, only in pieces and on other goroutines,
// so the results are equal, not close. The reference is the single pass.
func TestParallelAnalysisGivesTheSameResults(t *testing.T) {
	inputs := map[string][]float32{
		"drum track":               drumTrack(124, 0.337, 45, drumOpts{}),
		"noise":                    noiseTrack(45, 5),
		"drum track with an intro": drumTrack(128, 0.337, 50, drumOpts{intro: 15}),
	}
	pool := NewPool(8)
	for name, x := range inputs {
		if n := len(onsetEnvelope(x, nil)); n < 100 {
			t.Fatalf("%s: the onset envelope has only %d values, the comparison would prove nothing", name, n)
		}
		check := func(what string, stage func(pool *Pool) any) {
			t.Helper()
			var want any
			singleBatch(func() { want = stage(nil) })
			for mode, p := range map[string]*Pool{"batched": nil, "batched and parallel": pool} {
				if got := stage(p); !reflect.DeepEqual(want, got) {
					t.Errorf("%s: %s differs from the single pass when %s", name, what, mode)
				}
			}
		}
		check("onsetEnvelope", func(p *Pool) any { return onsetEnvelope(x, p) })
		check("gridEnvelope", func(p *Pool) any { return gridEnvelope(x, p) })
		check("chroma", func(p *Pool) any { return chroma(x, p) })
		check("tempo", func(p *Pool) any { bpm, score := tempo(x, p); return [2]float64{bpm, score} })
		check("key", func(p *Pool) any { k, score := detectKey(x, p); return []any{k, score} })
		check("edgesOf", func(p *Pool) any { return edgesOf(x, 124, p) })
	}
}

// Batches of frames meet at the frame numbers around a multiple of frameBatch,
// where a batch needs the frame before its first one.
func TestBandFluxAtTheBatchBoundaries(t *testing.T) {
	const win, hop = 1024, 256
	pool := NewPool(4)
	for _, frames := range []int{0, 1, 2, 3, frameBatch - 1, frameBatch, frameBatch + 1, frameBatch + 2, 2*frameBatch + 1, 2*frameBatch + 2, 3*frameBatch + 7} {
		x := noiseSamples(win+frames*hop, 3) // exactly that many windows
		var want [3][]float64
		singleBatch(func() { want = bandFlux(x, win, hop, nil) })
		for mode, p := range map[string]*Pool{"batched": nil, "batched and parallel": pool} {
			if got := bandFlux(x, win, hop, p); !reflect.DeepEqual(want, got) {
				t.Errorf("%d frames: the flux differs from the single pass when %s", frames, mode)
			}
		}
		if n := max(frames-1, 0); len(want[0]) != n {
			t.Errorf("%d frames: %d flux values, want %d", frames, len(want[0]), n)
		}
	}
}

// BenchmarkStages times what one song's analysis computes (tempo, beat grids,
// key) for 4 minutes of audio, on no pool and on pools of 2 to 16 workers:
//
//	go test ./internal/analyze -run xxx -bench Stages -benchtime 2x
func BenchmarkStages(b *testing.B) {
	x := drumTrack(128, 0.337, 240, drumOpts{})
	for _, workers := range []int{0, 2, 4, 8, 16} {
		var pool *Pool
		name := "sequential"
		if workers > 0 {
			pool, name = NewPool(workers), fmt.Sprintf("pool%d", workers)
		}
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				bpm, _ := tempo(x, pool)
				edgesOf(x, bpm, pool)
				detectKey(x, pool)
			}
		})
	}
}
