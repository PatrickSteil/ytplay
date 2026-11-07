# Mixing into long intros, and analysing the queue faster

Status: approved by the user 2026-11-07 in conversation and implemented, tasks
1 to 9 of `docs/superpowers/plans/2026-11-07-long-intros-and-fast-analysis.md`
(measurements and what was found on the way: see Results). Two independent parts,
built in this order: 1 (where a transition happens), then 2 (how fast and how
heavily songs are analysed). Builds on `2026-11-07-autodj-design.md`.

## What the user wants

Their words:

- "some edm songs have a long intro, and dont start with the beat directly. can
  you also consider transitioning into the next songs at some mid point in the
  second song? like eg when the introduction is slow and quiet, and the 'beat'
  starts around 30 sec, maybe transition into the second song with the start of
  the beat at 30 sec?"
- "i will listen to techno / edm, so 4 on the floor is typically the ground
  thruth"
- The first song transitions "just inside the last 30 s (when the beat is
  playing, not some outro or so), 4 bars is fine".
- "make the bpm detection for the current queue faster? maybe in parallel? i want
  to have a lightweight variant (no dj), and one variant where i allow to invest
  some cpu power to compute bpm and then reorder the queue."

## What exists and was measured today (2026-11-07, 16 cores)

**The next song's entry point.** The beat-matched plan already starts the next
song at the first beat of its steady beat (`Edges.Start.Anchor`), not at its
first sound, so a beatless intro is skipped. How far into a song that beat is
found was measured on synthetic tracks (128 BPM, a quiet pad, then a kick on every
beat, `Analyze`):

| beatless intro | start anchor found | sure |
| --- | --- | --- |
| 0 s, 16 s, 30 s | exactly where the kick starts | yes |
| 45 s | 0.00 s (wrong) | no |
| 70 s, 100 s | 0.15 s (wrong) | no |

So a beat at 30 s works, a 32-bar intro (60 s at 128 BPM) does not: `edges.go`
looks for the start of the steady beat only in the first `firstPassSeconds` = 60 s
of audible audio, and a beat that fills a quarter of that is too diluted to be
fitted. Real EDM has not been tried; the corpus has no techno yet.

**The playing song's overlap.** It starts 16 beats before the last steady beat
(`End.Anchor`) and lasts until that beat is sounded out. With a beat that runs to
the end that is the last ~8 s. With a beatless outro of more than ~22 s it starts
before the outro, so possibly more than 30 s before the end.

**Analysis cost.** One 4-minute song, single core: tempo 1.1 s, beat grids 1.4 s,
key 1.7 s, 4.3 s in all (measured with another test running, so approximate).
Fetching the audio (yt-dlp, then mpv decoding the whole song) comes on top and was
not timed separately. The queue is analysed two songs at a time, only after the DJ
popup has been opened; the playing song is analysed always, completely. Every
analysis computes beat grids, which only the DJ features use.

**Baseline** (Task 1, before any change, this machine idle, 15 cached corpus
songs, `TestQueueTiming` and `TestFetchTiming`): 4068 s of music (271 s per song)
analysed 2 at a time in 35.8 s wall, 4.63 s of compute per song (58.6 s of music
per second of compute); fetching a song takes 6.5 s on average (5.6 to 7.4 s,
one at a time). So a song costs about 6.5 s of fetch plus 4.6 s of compute, and a
20-song queue about 20 x 11.1 s / 2 = 110 s today. Fetching is 60% of that and
more cores do not shrink it: with 4 songs in flight it is at least 20 x 6.5 s / 4
= 33 s however fast the analysis is. The quarter-of-today target therefore
applies to the analysis compute (35.8 s for these 15 songs, so 9 s or less); the
queue end to end is reported next to it, and is bounded by fetching.

## Decisions

### Part 1: where a transition happens

**The playing song.** A beat-matched overlap lies inside the last 30 s of the
playing song's audible music, and on its steady beat: it starts no earlier than
`LastSeconds` = 30 s before `AudioEnd` and ends at the last steady beat as now.
Four bars (16 beats) stay. If the steady beat ended so early that 16 beats before
its end is before that point, no beat-match is made and the plan is the 6 s
crossfade at the end, saying why. (The user's words leave this case open; this is
the safe reading: never mix on a beat that is not there. *My call.*)

**The next song.** It enters at the start of its steady beat wherever that is in
the first `StartSearchSeconds` of audible music, so a beat at 60 s or 120 s is as
good as one at 30 s. The search must not get the beat at 30 s wrong (it works
today) and must find a kick that starts well after the beginning of the stretch it
looks at. The number is my starting point, to be tuned on real tracks: 150 s
(64 bars at 128 BPM is 120 s). A song whose beat starts later than that has no
sure start: crossfade, as now. How the search is done is the builder's: the
cause is that one fit over a long, mostly beatless stretch is too dull, so
fitting windows that move through the start and taking the first with a sure
grid, then finding the run's first beat in it as now, is one way.

**What counts as the start of the beat.** As today: the first beat of a run of
at least 8 clear beats (each at least 40% as strong as the typical beat's), on a
grid that is sure. A very quiet kick in a slow intro does not count until it is
clearly there. For four-on-the-floor music the beat is the kick, which the grid
envelope already favours (low band counted double); no separate mode for it. The
real test is real techno and EDM (below).

**Real music.** Before the thresholds are trusted, at least 8 real EDM and techno
tracks are added to the test set, with their BPM from a trusted source and where
their beat starts (some with an intro over 45 s), judged by listening like the
earlier previews were. The synthetic table above becomes tests as well.

**Unchanged.** A crossfade still starts the next song at its first audible
moment, intro and all. The 6% tempo rule, half and double time, sureness, volumes
and the closed-loop phase correction are not touched.

### Part 2: two ways of analysing

Two levels of analysis, chosen by what the user is doing, not by a setting:

- **Light**: the song that is playing, tempo and key only (what the header shows),
  one core, one song at a time, no beat grids. This is all that happens until the
  DJ popup has been opened. *Cheaper than today, which also computes the grids.*
- **DJ**: from the first opening of the DJ popup, the playing song also gets its
  beat grids, and the queued songs are analysed completely, several at a time,
  with each song's analysis itself spread over several cores. Opening the popup
  is the user's permission to spend CPU; nothing else needs switching on.

How it is made fast (results for the same audio stay identical to today's; tests
compare sequential and parallel output exactly):

1. across stages: key and tempo are independent, the beat grids need the tempo;
2. inside a stage: the spectrogram frames (FFT per frame) are independent, split
   over goroutines;
3. across songs: in DJ mode the fetch of the next songs (waiting on yt-dlp and the
   network, not the CPU) overlaps with the analysis of others.

CPU budget in DJ mode: up to half the cores in total (`NumCPU/2`, at least 1,
my call), shared by everything above; light mode uses one. Memory: a song's audio
is up to 53 MB as floats (600 s), so only a few songs are in flight at once, with
a ceiling of about 500 MB for the analysis (my call).

Target to be checked by measuring first: a 20-song queue of 4-minute songs is
analysed in at most a quarter of the time it takes today (about 20 songs x 10 s /
2 at a time = 100 s, to be measured), on this machine; light mode costs no more
than today's analysis of one song and less.

When the popup is opened mid-song, the playing song's grids are missing: the song
is fetched and analysed again for them. (Keeping its 21 MB of audio from the
light pass would avoid that; the builder may choose, my call is to refetch, which
is rare and simple.)

## Results (Task 9, 2026-11-07, this machine idle)

**Compute** (`TestQueueTiming`, 25 cached corpus songs, 7945 s of music):

| | wall time |
| --- | --- |
| today: 2 songs at a time, no pool | 74.7 s |
| DJ mode: 4 at a time, a pool of 8 (half the cores) | 20.6 s, 3.6 times faster |
| the same with a pool of 16 | 17.2 s, 4.3 times faster |

The target was a quarter (18.7 s). It is not met with half the cores: the work is
147.8 s of compute, so 8 workers cannot do better than 18.5 s however well they are
used, and 20.6 s is 90% of that. It needs about 9 workers. (`BenchmarkStages`, one
4-minute song, tempo plus grids plus key: 4.44 s sequential, 1.08 s on 8 workers,
0.91 s on 16.) The budget stays at `NumCPU/2`, as agreed; more is one number in
`cmd/ytplay/main.go`.

**Light** costs 4.26 s of compute per song against 5.97 s for full: 29% less, not
the third guessed above.

**The real app** (pty, 10 songs queued): before the DJ popup is opened no analysis
runs at all (0 decoders in 45 s, the playing song's light analysis was over). After
`D` the playing song and the queue were all analysed in 23 s, at most 4 decoders at
once, against about 69 s the old way (11 songs x (6.5 s fetch + 6 s compute) / 2).
Keys redrew during the burst after a median of 22 ms, at most 110 ms. Fetching is
60% of a song's time and more cores do not shrink it; with 4 in flight it is at
least 6.5 s / 4 per song.

**Real EDM** (10 tracks added to the corpus): the beat start is found on
Faithless - Insomnia at 63.4 s, Don't You Worry Child at 24.4 s and Around the World
at 20.8 s, and within 2 s on Titanium, Summer, Levels, Losing It and Sky and Sand.
Not yet confirmed by ear (click previews are rendered, see the ledger).

**Found on the way.** A fetch that runs out of time (the app's 45 s budget; long
tracks arrive at about twice real time) was analysed as if it were the whole song,
so its edges were the excerpt's: the planner would have cut a 10-minute track off at
about 80 s. Edges of an excerpt are now dropped (Task 7). Its tempo can still be
wrong: deadmau5 - Strobe reads 88 BPM from the 86 s that arrive in 45 s and 128 from
461 s.

## Not in this change

Saving analyses between runs; changing how tempo or key are estimated; other time
signatures than what the grid already assumes; choosing the song order for the
best mix; bar and phrase alignment (still no reliable bar start); a setting for the
number of cores.

## Risks

- **Real EDM is not the synthetic track.** Hats, pads and sidechained basses can
  make a kick run look weak or the grid dull; thresholds may need adjusting, and
  every doubt must still end in a crossfade, never a wrong grid.
- **A longer search costs time.** Fitting the fine envelope over 150 s instead of
  60 s is more compute at the start of every song in DJ mode; it must be paid for
  by the parallelism in part 2.
- **Seeking far into a stream.** The next song's deck is preloaded and seeked to
  its entry point. Measured (Task 4, `TestFarSeekPreloadRealYouTube`, a real
  YouTube stream): a paused deck is loaded after about 3 s and has stopped seeking
  0.1 s after a seek to 60, 100 or 140 s (3.2, 3.0 and 3.3 s in all, the cache
  reaching the target), against 2.9 s with no seek. The preload starts 30 s early:
  no change needed.
- **Parallelism changes timing, not results.** Floating-point sums must keep the
  same order (or the tests must show the same output) or tempo estimates change by
  a hair and the corpus expectations move.
- **CPU at the moment of opening the popup.** A burst of analysis can make a
  sluggish terminal; the UI loop and audio are separate processes (mpv) or
  goroutines and must not starve.

## Left to the builder

The exact search window and how to scan it, the number of workers for fetch and for
compute, how frames are split, whether to keep the playing song's audio, and the
names of the new constants.
