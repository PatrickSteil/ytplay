# Two decks: get the next song ready early, and later blend it in

Status: approved 2026-11-06. Phases 1 to 3 are implemented
(`docs/superpowers/plans/2026-11-06-two-decks.md`); phase 4 (blending) still
needs its own design. Measured on real YouTube songs: the silence between two
songs went from 4.9 s (cold load) to about 50 ms (preloaded handoff).

## Why

Between two songs ytplay is silent for about 4 seconds. mpv has to ask yt-dlp
for the stream (about 3 s) and open it (about 1 s) before the next song can
start. mpv's own `--prefetch-playlist` cannot help: its manual says it does not
work with URLs resolved by the youtube-dl wrapper, and never will.

The user wants three things from this, in this order:

1. No gap between songs: a few seconds before the current song ends, the next
   one is resolved and buffered in a **second mpv**, ready to start.
2. Later, **blend** the two songs (tempo-matched, overlapping beats, volume
   crossfade). That needs two songs sounding at the same time, which one mpv
   cannot do. This document only lays the ground for it.
3. A key that **sorts the queue by BPM**, lowest (chillest) to fastest.

## What was measured (2026-11-06, mpv 0.41, yt-dlp, this machine)

| | |
| --- | --- |
| yt-dlp resolves a stream URL | about 3.0 s |
| mpv opens an already resolved URL | about 1.2 s |
| mpv, page URL (today) | about 3.8 s |
| a second mpv loaded **paused**: ready after | about 3.2 s |
| ... and by then its cache holds | the whole song (270 s of a 271 s song) |
| unpause to audio advancing | about 51 ms |

So a deck that was loaded paused starts almost instantly, and preloading costs
one song's download (a few MB) ahead of time.

## Decision to approve: the queue moves out of mpv into Go

Today "mpv's internal playlist is the queue; nothing is kept in Go". That cannot
survive two mpv processes: a playlist lives in one process, and the next song
has to be loaded in the *other* one.

New rule: **ytplay owns the queue** (a list of entries and a position). Each of
the two decks is an ordinary mpv that plays **one file at a time** and has no
playlist of its own. Costs: `internal/player` is rewritten, the invariant above
changes, and queue bugs become ours instead of mpv's. Gains: queue operations
(move, remove, sort, clear) become plain, unit-testable Go; sorting by BPM is
just a new order; the handoff logic is ours to control, which blending needs.

The UI does not change its contract. It keeps receiving events with the same
names and shapes mpv sends today (`property-change` for `playlist`,
`playlist-pos`, `pause`, `idle-active`; `end-file` with reason `error`), now
produced by the new player, and it keeps calling the same `Actions`. The 300+
existing UI tests keep describing the behaviour.

## Design

### Pieces

- `internal/mpv`: `Start` takes a deck name so two instances get distinct
  sockets (`ytplay-<pid>-a.sock`, `-b.sock`). Otherwise unchanged.
- `internal/player`: `Player` becomes the engine. It holds the queue
  (`[]Entry`, a position, a played-before flag per entry as in mpv), two decks,
  which deck is active, and what is preloaded in the idle one. It implements
  the existing `ui.Actions`, plus `Events()`, `Done()`, `StderrTail()`,
  `Position()` and `Close()`, which `cmd/ytplay/main.go` takes from the mpv
  client today.
- A deck is one `mpv.Client` plus "what is loaded" (URL, ready, paused).

### Behaviour

- **Play a song**: load it on the active deck. Same as today.
- **Preload**: when the active song has at most `preloadLead` (default 15 s)
  left and there is a next entry, load that entry on the idle deck with
  `pause=yes`. If the next entry changes while a preload exists (reorder,
  remove, play-now), drop it and preload the new next one.
- **Handoff**: when the active deck reports `end-file` (reason `eof`) and the
  preload is ready, unpause the idle deck, make it active, advance the
  position, emit the events, stop the old deck. If the preload is not ready
  yet, wait for it (still faster than loading from scratch).
- **Skip**: the same handoff, immediately, if the preload is ready; otherwise
  load on the idle deck and play when ready.
- **Pause, seek, position**: act on the active deck. Pause state is kept in the
  player so a preloaded deck never starts while the user has paused.
- **Failures**: a deck that cannot load a song makes the player emit `end-file`
  with reason `error` (the UI already shows "Could not play") and move on to the
  next entry. If the second deck cannot be started or cannot open the audio
  device, the player runs with one deck and no preload (today's behaviour).
- **End of queue**: after the last song, `idle-active` becomes true and
  `playlist-pos` null, as mpv does now; appending starts playing, as now.

### Phases (each one shippable, in this order)

1. **The player owns the queue**, one active deck, no preload. No user-visible
   change. Done when: the UI tests pass unchanged, new player tests (fake
   decks) cover every `Actions` method, and the app behaves as before when run.
2. **Preload and handoff**, instant skip when ready. Done when: the silence
   between two songs is under about 0.3 s, measured with a short-song queue;
   reordering the next song never plays the wrong one.
3. **Sort by BPM.** The upcoming queue's BPM is analysed in the background
   (two songs at a time, results cached by video ID, like the current song's
   is today), shown next to each queued song, and a key in the queue panel
   (`b`) sorts the upcoming songs from slowest to fastest; songs without a BPM
   go last in their old order; a second press reverses. Songs added later (by
   hand or by `r`) are analysed as they arrive.
4. **Blending** (own design later): find where the beats fall in the last and
   first seconds of two songs, start the next deck on a beat of the current one,
   match the speed of the incoming one to the current one by a few percent
   (`speed`, pitch-corrected), and cross the volumes. It builds on the decks
   from phase 2 and on the tempo from the analyser; it needs a beat grid
   (positions of beats, not only the tempo), which the analyser does not give
   yet.

## Not in this change

Blending itself. Changes to how tempo or key are estimated (see the BPM work in
`internal/analyze`). Windows. Gapless handling of silence that is *inside* a
YouTube video's own audio (intros, outros, fade-outs): the handoff happens when
the file ends, not when the music does.

## Risks and how they are handled

- **Two mpv processes on one sound device.** PipeWire and PulseAudio mix them;
  plain ALSA may refuse the second open. Handled by the one-deck fallback above.
- **A stale preload** after the queue changes. Handled by dropping it whenever
  the entry after the current one changes; covered by tests that reorder,
  remove, play-now and clear while a preload exists.
- **UI and player disagree about the queue.** The UI keeps updating its own copy
  optimistically and is overwritten by the player's next `playlist` event, as
  it is overwritten by mpv's today.
- **Handoff timing.** Waiting for `end-file` and then unpausing leaves a gap of
  a few tens of milliseconds at most (event, IPC command, 51 ms measured start).
  Good enough for gapless; blending will start the next deck earlier and does
  not depend on this number.
- **Bandwidth.** One extra song is downloaded a little early. If the user skips
  or reorders, that download is wasted (a few MB).

## Left to the builder

The exact `preloadLead` (15 s is a starting point), how events are re-emitted
(synchronously from the player's goroutine is fine), and the structure of the
fake deck used in tests.
