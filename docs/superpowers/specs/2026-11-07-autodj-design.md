# Autodj: start the next song before the current one ends

Status: approved 2026-11-07 by the user in conversation (overlap, the sureness
rule, the checked spike) and implemented, tasks 1 to 8 of
`docs/superpowers/plans/2026-11-07-autodj.md`. Builds on
`2026-11-06-two-decks-design.md`, phase 4.

## What the user wants

When one song is about to end, the next one starts under it and the volumes
cross over. The user's words and decisions:

- They overlap "maybe 4 bars at the end".
- Beats are lined up and the tempo is adjusted so the two songs run together,
  **but only when ytplay is very sure** ("techno is pretty easy, hip hop not").
- Otherwise: "just smoothly start the next song a few seconds before the other
  ends (volume up and down)". A plain crossfade is always acceptable, so every
  doubt resolves to it.
- All of this belongs to DJ use: it is switched on inside the DJ popup, not
  forced on anyone.

## What the spike showed (2026-11-07, ten songs, one listened to by the user)

- The beat grid at the two ends of a song is accurate for kick-driven music:
  after removing librosa's own +19 ms bias, grids agree with its beats within
  about 15 ms on Daft Punk, Billie Jean, Another One Bites the Dust, Uptown
  Funk and Levels' start. By ear the user judged the clicks right on the
  songs checked, and Nirvana (which the gate refuses) completely off.
- One grid for a whole song is not good enough: Billie Jean drifts 129 ms,
  Another One Bites the Dust 136 ms, Teen Spirit swings 100 ms. A grid fitted
  to the last and the first 40 s is: wobble inside that stretch was 0 to 36 ms.
- The envelope used to find onsets lags the audio by about 41 ms; it is
  calibrated on synthetic clicks and subtracted.
- Hi-hats or guitars on the off-beat can look as strong as the beat; weighting
  the low band (the kick) twice as much picks the beat.
- Which beat of four starts a bar cannot be told reliably yet (confidence
  below 0.31 on every song). This design does not rely on it.

## Decisions

**Analysis** (`internal/analyze`): per song, `Edges`: the first and the last
audible moment, and a beat grid fitted to the first and to the last 40 s of
audible audio: the beat period, an anchor beat and a verdict `Sure`. The anchor
is where the steady beat starts (the first beat of a run of 8 clear beats, so
after a beatless intro) and, at the end, where it stops. The grid is fitted in
two passes: first to up to 60 s to find that point, then to the 40 s of steady
beat next to it, so that a beatless intro or outro does not dull it. If the
first 60 s of a song give no beat that could be sure (a long intro: a pad, a
build-up), its start is searched again over the first 150 s, window by window,
from where the first window with a sharp beat begins (see
`2026-11-07-long-intros-and-fast-analysis-design.md`). A grid is
sure only if all hold: the folded onset profile is sharp (peak at least 5
times the mean), steady (the beat's offset in each quarter that has a beat of
its own stays within 20 ms), clearly on the beat and not the off-beat (its
peak at least 1.5 times the opposite phase), its tempo is within 0.5% of the
song's overall BPM, and the tempo at the other end of the song is the same
within 1% (a drummer whose tempo wanders, or a tempo change, is never sure).
Songs with no BPM have no grids.

**Planning** (`internal/autodj`, pure functions): `For(a, b)` returns a `Plan`:
- Beat-matched, only if the end of `a` and the start of `b` are both sure and
  the tempi are within 6%: `b` starts at its anchor beat when `a`'s anchor beat
  16 beats (4 bars) before its own anchor is reached, played at the speed that
  makes its beats as far apart as `a`'s (pitch kept). A song at twice or half
  the tempo (70 and 140 BPM) is compared at double or half time: of the three
  ways to line up (one beat to one, two to one, one to two) the one that needs
  the smallest change of speed is taken, and must itself be within the 6%. 70
  into 140 therefore plays at about speed 1, not 2 or 0.5, and every beat of the
  slower song meets a beat of the faster one. Other ratios (3:2) are
  crossfaded. The plan says "(double time)" or "(half time)" in its reason.
  The overlap lasts until `a` ends. After that `b`'s speed eases back to 1
  over 8 s. Volumes cross with an equal-power curve.
- Otherwise a crossfade: `b` starts 6 s before the audible end of `a` (the
  whole of `a` if shorter than 15 s), at normal speed, same volume curve.
- Every plan says in words why it is what it is.
- Beat-matching is also refused if the plan would run past the end of either
  song.

**Hearing it**: `autodj.Render` mixes a plan offline so it can be tested and
written to a WAV for listening (a gated test does that for pairs from the test
songs). Time-stretching is injected: tests use a resampler, previews ffmpeg.

**Doing it live** (`internal/player`): with autodj on, the next song is
preloaded 30 s before the end (15 s was too late: a stream takes seconds to
resolve and the beat-matched start is some 8 s before the end), seeked to
`BFrom`, silenced and set to its speed. The playing song's own position is the
master clock; a crossfade starts the next song when it reaches `AStart`. A
beat-matched start is timed from many readings of that position (mpv reports it
in steps, so the freshest reading is the truth) and sent a calibrated 22 ms
early. Even so a start by order over a socket lands on the deck's audio chunk
grid, 17 or 40 ms off in measurements, so while both songs sound the next
song's position is compared with where the plan says it should be and its speed
is steered by the error over 1 s (errors under 10 ms are left alone, changes
come at most every 250 ms, and the speed never strays more than 3% from the
plan). Volumes follow an equal-power curve through mpv's cubic volume scale
(gain = (volume/100)^3, measured). When the overlap is over the next song takes
over and its speed eases back to 1 over 8 s. A beat-match that is ready too
late (more than a second after its start) becomes a crossfade of the time that
is left. Skipping, pausing, seeking back, changing the queue or switching autodj
off during a transition are all handled. If a song's analysis is not there in
time, the transition is a crossfade by duration.

Measured on real decks with two synthetic drum tracks: worst miss between the
incoming and the outgoing kicks 7 to 12 ms over repeated runs. At 70 into 140
and 138 into 72 BPM the kicks land on the right beat but less precisely (worst
22 to 33 ms): the next song starts 18 to 24 ms early and the loop takes about
2 s to bring that in (a song with a kick every 0.83 s shows it on the first
kick), and at exactly speed 1 the dead band leaves a steady 12 to 17 ms. With two real
songs (Uptown Funk into Another One Bites the Dust) the loop saw the next song
10 to 14 ms ahead of the plan and the speed stayed within 0.4% of it.

**Switch**: `a` in the DJ popup turns autodj on and off; off by default, for
the session. The popup says how the next transition will be (the plan's
reason). Queued songs are analysed once the popup has been opened.

## Not in this change

Bar and phrase alignment (needs a reliable bar start; in double and half time
which of the faster song's two beats meets the slower song's beat is therefore
not chosen); choosing the song order for the best mix; EQ or filter tricks; saving
analysis between runs.

## Risks

- **Timing of the real deck start.** Measured about 50 ms from unpause to
  audio; the engine compensates and logs the error it can see. Beats off by
  more than about 30 ms sound like a flam. Handled by the gate (when unsure
  it crossfades) and by measuring on real decks before shipping the live part.
- **The gate is calibrated on ten songs and no hip hop.** It errs towards
  refusing. More songs, including hip hop and techno, are added to the test set
  before the thresholds are trusted.
- **Songs with a silent or beatless intro or outro.** The 40 s stretch may then
  be less sharp and the gate refuses: a crossfade, never a wrong grid.
