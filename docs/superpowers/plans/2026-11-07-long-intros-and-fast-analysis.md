# Long intros and fast analysis Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Mix into songs whose beat starts late (up to 150 s in), keep the playing song's overlap inside its last 30 s, and analyse songs faster in two levels (light for the playing song, full and parallel once the DJ popup was opened).

**Architecture:** Part 1 changes one planner rule (`internal/autodj`) and the search for the start of the beat (`internal/analyze/edges.go`). Part 2 turns the analyser's per-frame FFT work into batches run on a shared `Pool` (results bit-for-bit the same), adds a `Level` (Light: tempo and key; Full: also the beat grids), and lets the UI pick the level by whether the DJ popup was opened.

**Tech Stack:** Go (standard library only), mpv, yt-dlp. Tests: `go test`, `make check`, `-race`.

**Spec:** `docs/superpowers/specs/2026-11-07-long-intros-and-fast-analysis-design.md` (builds on `2026-11-07-autodj-design.md`).

## Global Constraints

- Four bars (`autodj.Beats` = 16) of overlap, the 6% tempo rule, half and double time, sureness gates, volumes and the phase loop are not touched.
- `autodj.LastSeconds` = 30: a beat-matched overlap starts no earlier than 30 s before the playing song's `AudioEnd`; if the beat ended too early the plan is the 6 s crossfade, saying why.
- The beat start is searched in the first 150 s of audible music (`startSearchSeconds`); later than that: not sure, crossfade.
- Parallel analysis returns exactly the results of sequential analysis (compare with `==`/bit equality, not tolerances).
- Light = the playing song only, tempo and key only, one core, no beat grids. Full + parallel starts only after the DJ popup was opened. DJ-mode compute budget: `max(1, runtime.NumCPU()/2)` workers in one shared pool; memory for analysis about 500 MB, so at most 4 songs in flight (`queueAnalysisWorkers` = 4).
- Standard library only, no new dependencies. `gofmt`, `go vet`, `make check` and `go test -race` on touched packages stay green.
- **Do not commit** unless the user asks (the tree already holds a large uncommitted autodj change); the final report names the files touched.
- Test files: use the existing helpers in `internal/analyze/edges_test.go` (`drumTrack`, `drumBeats`, `clickTrack`, `concat`, `silence`, `tone`, `beatOffset`) and `kicks` in `internal/autodj/plan_test.go`.

## Review Focus

- A song whose beat starts after 150 s, or never (noise bursts), must not get a made-up start: not sure, crossfade. (Task 3)
- A playing song shorter than 30 s must still be beat-matchable (the 30 s window cannot start before the song). (Task 2)
- The DJ popup opened while the playing song's light analysis is still running: the song is upgraded exactly once, and a failed or beatless song is not re-analysed in a loop. (Task 8)
- Clearing the queue or skipping with several analyses in flight on a shared pool: no deadlock, and results that arrive after the cancellation are dropped (the compute itself cannot be cancelled, as today). (Tasks 5, 8)
- A deck seeked 60 to 150 s into a stream may not be ready in time: it must end as a crossfade of the time left, never silence. (Task 4)

---

### Task 1: Baseline timing (before anything changes)

**Files:**
- Create: `internal/analyze/timing_test.go` (gated measurements, kept for Task 9)
- Modify: `docs/superpowers/specs/2026-11-07-long-intros-and-fast-analysis-design.md` (record numbers)

**Interfaces:**
- Produces: `TestQueueTiming` and `TestFetchTiming` (both skip unless their env var is set); output lines Task 9 compares with.

- [ ] **Step 1: Write `TestQueueTiming`**: skips unless `YTPLAY_CORPUS` is set; loads every cached `<id>.f32` of the corpus (`loadPCM`), analyses them with `Analyze` at `n` songs at a time (`n` from env `YTPLAY_SONGS_AT_ONCE`, default 2, goroutine per song), and logs wall time for the whole set plus the mean seconds of audio per second of compute.
- [ ] **Step 2: Write `TestFetchTiming`**: skips unless `YTPLAY_FETCH_TIMING=1` (and mpv/yt-dlp found, as `fetchMissing` checks); `Fetch`es the first 5 corpus ids one after the other and logs each wall time and audio length.
- [ ] **Step 3: Run both** with the corpus cache from earlier (`YTPLAY_CORPUS=<dir> go test ./internal/analyze -run 'QueueTiming|FetchTiming' -v -count=1`); with the machine otherwise idle. Expected: numbers, no failure.
- [ ] **Step 4: Record** in the spec's "What exists" section: seconds to analyse the corpus at 2 at a time, mean fetch seconds per song, and the resulting estimate for a 20-song queue. Task 9's target is a quarter of that analysis time.

### Task 2: The overlap stays inside the playing song's last 30 s

**Files:**
- Modify: `internal/autodj/plan.go` (`For`), `internal/autodj/plan_test.go`

**Interfaces:**
- Produces: `const LastSeconds = 30.0` in package `autodj`.

- [ ] **Step 1: Write the failing tests** in `plan_test.go` (`songA` has `AudioEnd` 200.6, period 0.5, so the overlap starts `anchor - 8`):
  - `TestBeatMatchStaysInsideTheLast30Seconds`: `End.Anchor = 178.7` → `BeatMatch`; `End.Anchor = 178.5` → `Crossfade`, `Why` contains "last 30 s".
  - `TestAShortPlayingSongIsStillBeatMatched`: `Song{Length: 25, Edges: sureEdges(0.1, 24.5, 0.5, 0.4, 0.5, 24.1)}` as `a` with `songB()` → `BeatMatch` (the window cannot start before `AudioStart`).
  - Change `TestBeatMatchEndsWhereTheBeatEndsWhenTheOutroIsLong`: `AudioEnd = 215` (15 s outro) keeps the beat-match with overlap `16*0.5+0.5`; add `AudioEnd = 230` → `Crossfade` (the overlap would start 37.6 s before the end).
- [ ] **Step 2: Run** `go test ./internal/autodj -run 'Last30|ShortPlayingSong|OutroIsLong' -count=1`. Expected: FAIL (the 178.5 and the 230 cases beat-match).
- [ ] **Step 3: Implement** in `For`, right after `aStart` is computed: if `aStart < math.Max(a.audibleEnd()-LastSeconds, a.Edges.AudioStart)` return `crossfade(a, b, "crossfade: the playing song's steady beat ends more than 30 s before its end")`.
- [ ] **Step 4: Run** `go test ./internal/autodj -count=1` (skip nothing; the end-to-end tests take ~45 s). Expected: PASS.
- [ ] **Step 5: Mutation check**: replace `LastSeconds` by 60, then by 0; Step 1's tests must fail each time; restore.

### Task 3: Find a beat that starts up to 150 s in

**Files:**
- Modify: `internal/analyze/edges.go` (`edgesOf`, `edgeGrid`), `internal/analyze/edges_test.go`

**Interfaces:**
- Produces: unexported `startSearchSeconds = 150`, `scanWindowSeconds = 30`, `scanHopSeconds = 10`; `edgeGrid` keeps its signature for now (Task 6 adds the pool); the end (`firstPassSeconds` = 60) is unchanged.

- [ ] **Step 1: Write the failing tests** in `edges_test.go`, all with 128 BPM tracks that have `intro` seconds of quiet `tone` pad before the first beat and 120 s of beat after it (`drumTrack(128, intro, intro+120, drumOpts{})` mixed with a pad; the intro must be audible, silence would be trimmed by `audibleRange` and the intro would vanish):
  - `TestBeatStartIsFoundAfterALongIntro`: intros 16, 30, 45, 60, 70, 100, 120 → `Edges.Start.Sure`, and `Anchor` within 25 ms of a kick (`beatOffset`) with `Anchor-intro` in `[-0.025, 1 beat+0.025]`.
  - `TestNoBeatStartIsClaimedWhenTheBeatComesAfter150Seconds`: intro 160 → `Start.Sure` false.
  - `TestNoBeatStartInIrregularNoiseBursts`: 200 s of pad with noise bursts at irregular (seeded `lcg`) intervals, `edgesOf(x, 128)` → `Start.Sure` false.
  - Existing `TestEdgesWithABeatlessIntro` and `TestEdgesOfADrumTrack` must still pass unchanged.
- [ ] **Step 2: Run** `go test ./internal/analyze -run 'BeatStart|BeatlessIntro' -count=1`. Expected: FAIL for intros 45 and longer.
- [ ] **Step 3: Implement** for the start only. The cause of today's limit (measured: a 45 s intro gives sharpness 17 but no anchor) is that `edgeGrid` needs `last-first >= 2*minGridSeconds` (16 s) of steady beat inside the first-pass window, and only 15 s of it fall inside 60 s. So first widen the start's first pass from `firstPassSeconds` to `startSearchSeconds`, leaving the end at 60 s. If Step 1's tests still fail (a beat that fills only 20% of the window dulls the fit, or the noise-burst test finds a beat), scan instead: slide a `scanWindowSeconds` window by `scanHopSeconds` over the envelope (built once), take the first window whose `fitGrid` has `sharp >= sureSharp` as the grid for `steadyBeats` over the whole envelope, then refit on `edgeSeconds` from that first beat as today. Audio shorter than one window uses today's single fit. The tests decide; keep the simpler one that passes them all.
- [ ] **Step 4: Run** `go test ./internal/analyze -count=1`. Expected: PASS (the corpus test skips without `YTPLAY_CORPUS`).
- [ ] **Step 5: Mutation check**: set `startSearchSeconds` to 60, then make the window take the last sharp one instead of the first; Step 1's tests must fail; restore.

### Task 4: Real EDM and techno, listening, far seeks

**Files:**
- Modify: `internal/analyze/testdata/corpus.tsv` (at least 8 EDM/techno tracks, columns `id | BPM | title | note`), `internal/analyze/corpus_test.go`
- Create: `internal/analyze/testdata/beatstart.tsv` (`id | seconds where the steady kick starts | note`), `internal/player/farseek_test.go` (gated)

**Interfaces:**
- Consumes: `Edges.Start` from Task 3.
- Produces: corpus assertion that every id in `beatstart.tsv` has `Start.Sure` and `Anchor` in `[truth-0.05, truth+2 bars]`.

- [ ] **Step 1: Choose the tracks** (WebSearch for BPM from public databases, as the corpus header describes): four-on-the-floor techno, house, trance, EDM; at least 3 with a beatless or soft intro longer than 45 s, at least 2 with the beat from the start. Fetch them into the corpus cache (`TestCorpus` does that).
- [ ] **Step 2: Establish where each beat starts**: run `TestCorpus`, cross-check against librosa (venv in the session scratchpad), then render previews with the gated preview test in `internal/autodj/preview_test.go` (env var is in its header) for pairs including these tracks, and **ask the user to listen**. Fill `beatstart.tsv` only with what they confirm.
- [ ] **Step 3: Add the assertion** to `TestCorpus` (and `fmt.Printf` the start grid of those tracks, as for the others). Run `YTPLAY_CORPUS=<dir> go test ./internal/analyze -run TestCorpus -v -count=1`. Expected: PASS; any track the gate refuses is reported to the user with its numbers, thresholds are not loosened without their say.
- [ ] **Step 4: Write `TestFarSeekPreloadRealYouTube`** in `farseek_test.go`, skipped unless `YTPLAY_NETWORK=1`: with a real deck, time `loadfile` of a corpus song paused until `time-pos` has reached a seek to 0 s, and to 60, 100 and 140 s; log the four times.
- [ ] **Step 5: Run it, record** the times in the spec's Risks. If the 140 s seek needs more than about 20 s, raise `autoDJLead` in `internal/player/autodj.go` and say so in the report; if a late deck ever leaves silence in `TestBeatMatchRealMpv`-style tests, that is a bug to fix, not to document.

### Task 5: A shared worker `Pool`

**Files:**
- Create: `internal/analyze/pool.go`, `internal/analyze/pool_test.go`

**Interfaces:**
- Produces:
  - `type Pool struct{ ... }`
  - `func NewPool(workers int) *Pool` (`workers < 1` means 1)
  - `func (p *Pool) Run(n int, fn func(i int))`: calls `fn(0..n-1)` once each, at most `workers` calls at the same time across all concurrent `Run`s of that pool, returns when all are done; a nil `*Pool` runs them one after the other in the caller.

- [ ] **Step 1: Write the failing tests**: every index runs exactly once (sum check), nil pool works, `n == 0` returns, the highest number of concurrent calls observed (atomic counter) over 8 goroutines each calling `Run(20, ...)` on `NewPool(3)` never exceeds 3, and with `-race` all 8 callers finish within a 10 s deadline (no deadlock when callers outnumber workers).
- [ ] **Step 2: Run** `go test ./internal/analyze -run Pool -count=1`. Expected: FAIL to compile (`NewPool` undefined).
- [ ] **Step 3: Implement** with a token channel of size `workers`: `Run` starts `min(n, workers)` goroutines that each take a token, loop over an atomic next-index until `n`, release the token; the caller waits on a `sync.WaitGroup`.
- [ ] **Step 4: Run** `go test -race ./internal/analyze -run Pool -count=3`. Expected: PASS.
- [ ] **Step 5: Mutation check**: let `Run` ignore the token limit; the concurrency test must fail; restore.

### Task 6: Frames in parallel, same results

**Files:**
- Modify: `internal/analyze/analyze.go` (`onsetEnvelope`, `tempo`, `chroma`, `detectKey`, `eachFrame`), `internal/analyze/edges.go` (`gridEnvelope`, `gridOf`, `edgeGrid`, `edgesOf`), existing call sites in `edges_test.go` (pass `nil`)
- Create: `internal/analyze/parallel_test.go`

**Interfaces:**
- Consumes: `*Pool` from Task 5.
- Produces (all unexported): `bandFlux(x []float32, win, hop int, pool *Pool) [3][]float64` (the three bands' raw log-spectral flux, shared by `onsetEnvelope` and `gridEnvelope`), and the pool as the last parameter of `onsetEnvelope`, `gridEnvelope`, `tempo`, `chroma`, `detectKey`, `gridOf`, `edgeGrid`, `edgesOf`.

- [ ] **Step 1: Write the failing test** `TestParallelAnalysisGivesTheSameResults` in `parallel_test.go`: for three inputs (a `drumTrack` of 90 s, 90 s of seeded noise, a `drumTrack` with a 20 s `tone` intro), compare with `==` (slices with `reflect.DeepEqual`) the output of `onsetEnvelope`, `gridEnvelope`, `chroma`, `tempo`, `detectKey` and `edgesOf` for `pool == nil` and for `NewPool(8)`.
- [ ] **Step 2: Run** it. Expected: FAIL to compile (signatures).
- [ ] **Step 3: Implement** the signature change and `bandFlux`: split the frames into batches of 64; each batch recomputes the log-magnitudes of the frame before its first (for the flux against it), runs its frames in order and writes into its own slice of the result, so every value is computed by the same operations as before; for `chroma` each batch collects its peaks and the batches are concatenated in order. `Run` on the pool distributes the batches; a nil pool runs them in order.
- [ ] **Step 4: Run** `go test -race ./internal/analyze -count=1`. Expected: PASS, including every existing edge, grid and tempo test.
- [ ] **Step 5: Benchmark** (add `BenchmarkAnalyze` with 240 s of drum track at `pool` nil and `NewPool(8)`): `go test ./internal/analyze -bench Analyze -run xxx -benchtime 3x`. Expected: the pooled one at least 3 times faster on 8 or more cores; record both in the spec.
- [ ] **Step 6: Mutation check**: make a batch skip its seed frame; the equality test must fail; restore.

### Task 7: Levels, options, key beside tempo

**Files:**
- Modify: `internal/analyze/analyze.go`, `internal/analyze/decode.go`, `internal/analyze/analyze_test.go` (or the file that tests `Analyze`)

**Interfaces:**
- Consumes: `*Pool` and the pool parameters from Task 6.
- Produces:
  - `type Level int; const (Full Level = iota; Light)` (`Full` is the zero value)
  - `type Options struct{ Level Level; Pool *Pool }`
  - `func AnalyzeWith(pcm []float32, o Options) (Result, error)`; `func Analyze(pcm []float32) (Result, error)` becomes `AnalyzeWith(pcm, Options{})`
  - `func SongWith(ctx context.Context, mpvPath, ytdlpPath, id string, duration float64, o Options) (Result, error)`; `Song` stays as `SongWith(..., Options{})`
  - new field `Result.Level Level`

- [ ] **Step 1: Write the failing tests**: `TestLightKeepsTempoAndKeyAndSkipsTheGrids` (same `BPM`, `TempoScore`, `Key`, `Camelot`, `KeyScore` as `Full`; `Result.Level == Light`; `Edges` is the zero value), `TestFullIsTheDefault` (`Analyze(x)` equals `AnalyzeWith(x, Options{})` and has `Level == Full`), `TestAnalyzeWithAPoolEqualsWithout` (`reflect.DeepEqual` on a drum track, pool of 8, key running beside tempo).
- [ ] **Step 2: Run** them. Expected: FAIL to compile.
- [ ] **Step 3: Implement**: Light returns after tempo and key; with a pool and `Full`, run the key in its own goroutine while tempo then `edgesOf` run in the caller; without a pool everything runs in order. `Result.Level` is set from the options.
- [ ] **Step 4: Run** `go test -race ./internal/analyze -count=1`. Expected: PASS.

### Task 8: The UI picks the level; DJ mode analyses the queue in parallel

**Files:**
- Modify: `internal/ui/model.go` (`Deps.Analyze`, `fetchStats`, `startQueuedAnalyses`, `openDJ`, `analysisMsg` handling, `queueAnalysisWorkers`), `cmd/ytplay/main.go`, the existing test sites that build `Deps.Analyze` (`queue_bpm_test.go` twice, `stats_test.go`; find them with `grep -rn "Analyze:" internal/ui cmd`)
- Test: `internal/ui/dj_test.go`

**Interfaces:**
- Consumes: `analyze.Level`, `analyze.Options`, `analyze.SongWith`, `analyze.NewPool`, `Result.Level`.
- Produces: `Deps.Analyze func(ctx context.Context, id string, duration float64, level analyze.Level) (analyze.Result, error)`; `const queueAnalysisWorkers = 4`.

- [ ] **Step 1: Write the failing tests** in `dj_test.go` with a fake `Analyze` that records the calls (`id`, `level`) and can block:
  - `TestWithoutTheDJPopupOnlyThePlayingSongIsAnalysedLight`: play a queue of 4 → exactly one call, `Light`, for the playing song.
  - `TestOpeningTheDJPopupAnalysesTheQueueFullFourAtATime`: open the popup with 10 queued songs → four calls `Full` at once (the fake blocks), the fifth only after one returns.
  - `TestOpeningTheDJPopupUpgradesThePlayingSongOnce`: the playing song has a `Light` result → one `Full` call for it; opening the popup again, or a second result message, causes no more; the song's `Edges` reach `m.songs` (the autodj lookup).
  - `TestUpgradeWhileTheLightAnalysisIsStillRunning`: popup opened while the `Light` call is blocked → when it returns, exactly one `Full` call follows.
  - `TestAFailedOrBeatlessSongIsNotReanalysedInALoop`: a failing `Light` analysis, and a `Light` result with `BPM == 0`: opening and closing the popup three times causes at most one extra call for the beatless one and none for the failed one.
  - `TestClearingTheQueueDropsAnalysesInFlight`: with four blocked `Full` analyses, clear the queue (the existing `queueCtx` mechanism) and release them → their results are not stored and the next analyses start only for songs that are still queued.
  - Existing tests (queue sort, BPM display, stats) keep passing after the call sites are changed to the new signature.
- [ ] **Step 2: Run** `go test ./internal/ui -run 'DJPopup|Upgrade|Reanalysed' -count=1`. Expected: FAIL to compile.
- [ ] **Step 3: Implement**: the playing song's lookup passes `Light` unless `m.djActive`; `openDJ` starts a `Full` analysis for the playing song when its result has `Level == Light` (once, tracked in a `fullAsked` set; a failed song is never upgraded), and a `Light` result arriving after the popup was opened triggers it too; queued songs always `Full`; `queueAnalysisWorkers` = 4. Cancellation through the existing `queueCtx`/`statsCancel` is unchanged.
- [ ] **Step 4: Wire `main.go`**: one `pool := analyze.NewPool(max(1, runtime.NumCPU()/2))`; `Analyze` calls `analyze.SongWith(ctx, mpvPath, ytdlp, id, duration, analyze.Options{Level: level, Pool: pool})` with `Pool` only when `level == analyze.Full`.
- [ ] **Step 5: Run** `go test -race ./internal/ui -count=1`, then `make check`. Expected: PASS.
- [ ] **Step 6: Mutation check**: make `openDJ` skip the upgrade, then make the playing song always `Full`; Step 1's tests must fail each time; restore.

### Task 9: Measure against the target, docs

**Files:**
- Modify: `README.md` (DJ popup row: light and full analysis), `docs/superpowers/specs/2026-11-07-long-intros-and-fast-analysis-design.md`, `docs/superpowers/specs/2026-11-07-autodj-design.md` (analysis and sureness paragraphs that name 60 s and 40 s)

- [ ] **Step 1: Re-run Task 1's measurements** after extending `TestQueueTiming` with env `YTPLAY_POOL=<workers>` (use `AnalyzeWith(pcm, Options{Pool: NewPool(n)})` when set; this could not compile before Task 7), with `YTPLAY_SONGS_AT_ONCE=4` and `YTPLAY_POOL` = `NumCPU/2`; expected: the corpus analysed in at most a quarter of the baseline's time; light analysis of one song no slower than the baseline's. If not met, say so with the numbers instead of adjusting the target.
- [ ] **Step 2: Run the real app** under a pty (as for the autodj smoke tests: answer the OSC 11 and CPR queries, `Esc` before `Enter`): with 10 queued songs, `D` opens the popup and BPMs fill in; note the time until all show, and that keys (`j`, `k`) still redraw the screen at once while the analysis runs (the CPU burst must not make the UI sluggish); without the popup open only the playing song is analysed (check the process list or debug log).
- [ ] **Step 3: Update the docs** with the numbers and the new rules; run `gofmt -l .`, `go vet ./...`, `make check`, `go test -race ./internal/analyze ./internal/autodj ./internal/ui ./internal/player` (autodj's race run needs `-timeout 40m`).
- [ ] **Step 4: Report** to the user: what changed, the measured speed-up, any gate decisions on the EDM tracks, the files touched, and that nothing is committed.
