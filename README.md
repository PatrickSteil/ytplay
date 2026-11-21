# ytplay

A terminal music player for YouTube. [mpv](https://mpv.io) plays the audio and
[yt-dlp](https://github.com/yt-dlp/yt-dlp) searches; ytplay only controls them.

## Requirements

Linux or macOS, a true-colour terminal, Go 1.26+ to build, `mpv` (0.35+) and
`yt-dlp`. If search or playback breaks, update yt-dlp first.

```sh
go install github.com/PatrickSteil/ytplay/cmd/ytplay@latest
ytplay
```

Or download a build for Linux or macOS (amd64 or arm64) from the
[releases](https://github.com/PatrickSteil/ytplay/releases), check it against
`SHA256SUMS`, and put `ytplay` somewhere in your `PATH`. A release is made by pushing a
tag (`git tag v0.1.0 && git push --tags`); `scripts/build-release.sh` builds the same archives locally. `ytplay -version` prints the
version (the tag, or `dev-<commit>` for a build from a checkout).

From a checkout: `make` builds, `make test` tests, `make install` installs to
`~/.local/bin` (`PREFIX=/usr/local` for system-wide; `make help` lists all).

## Keys and commands

ytplay takes keys like vim: a few keys for moving about, and everything else is a
command typed after `:`.

Keys that need no colon:

| Key | Action |
| --- | --- |
| `:` | open the command line (below) |
| `/` | search bar (Enter runs it, Esc closes it); the same as `:find` without a name |
| `j` `k` | move in the results, the queue and the DJ popup |
| Enter | play the highlighted result or queue entry now |
| Tab | switch between the results and the queue |
| `x` | remove the highlighted queue entry (`↑` `↓` or `J` `K` move it) |
| Space | pause / resume |
| `←` `→` | seek 5 seconds back / forward |
| `+` `-` | volume up / down by 5% |
| `u` | undo the last queue change (removing, moving, sorting, shuffling, clearing, adding, radio; up to 20, forgotten when the song changes) |
| `0`–`9`, `=` | seek to 0%–90%, 100% |
| `?` | open or close help |
| Esc | back out of whatever is open |
| Ctrl+C | quit |

Press a shortcut from the table to run its command without arguments.
`l` opens `:load ` ready for a link. Shortcuts in the DJ popup follow the same
command availability as the command line. Press `:`, type a command and press
Enter; Esc (or Backspace on an empty line) cancels, `↑` `↓` step through the
commands of this session and Tab completes a name. A command is its name or its
short form, or the start of its name if that is unambiguous (`:down`, `:fin`).
"Highlighted" is the selection of the list that has the focus: the results, or
the queue after Tab.

| Command | Shortcut | Action |
| --- | --- | --- |
| `:find NAME` | `f` | search for NAME (without a name: open the search bar) |
| `:add` | | add the highlighted result to the queue |
| `:load LINK` | `l` | add a YouTube or SoundCloud playlist (or set) link to the queue, every song in it (the first 500; private and deleted ones are skipped); a song link queues that one song (a YouTube watch link that carries `list=` counts as the playlist; short `on.soundcloud.com` links are not accepted). A link pasted into the `/` search bar or after `:find` does the same instead of searching. It starts playing if nothing is, and `u` undoes it |
| `:yank` | `y` | copy the link of the highlighted result, queue entry or DJ popup row (the playing song if nothing is highlighted) to the clipboard: with `wl-copy`, `xclip`, `xsel` or `pbcopy` if installed, else through the terminal (OSC 52) |
| `:cast [NAME\|off]` | `C` | play on a speaker in the same network (Chromecast, Google Home, Nest): without a name it opens the speaker picker. Use `↑`/`↓` or `j`/`k` to select, Enter to confirm, and Esc to close. Choose **Play here** or type `:cast off` to return to local playback. Give `:cast` a speaker name or part of it to connect directly. Playback continues from the current position. The speaker fetches the audio itself, so this needs no extra software; volume, pause and seek go to the speaker |
| `:play` | | play the highlighted result or queue entry now (Enter does the same) |
| `:skip` | `s` | skip |
| `:prev` | `p` | the song before; if this one has played for more than 3 s, its start |
| `:seek TIME` | | jump to `1:30`, `1:02:03` or `90` (seconds), to `50%`, or by `+10` / `-30` seconds |
| `:vol N` | `v` | volume 0 to 100, or `+N` / `-N`; alone it says the volume. A master volume: the crossfades of autodj are scaled by it. Not kept between runs |
| `:mute` | `m` | mute or unmute |
| `:delete` | `x` | remove the highlighted queue entry (the key `x` does the same) |
| `:undo` | `u` | undo the last queue change (the key `u` does the same) |
| `:shuffle` | | shuffle the queue (the playing song stays) |
| `:download` | `d` | save the highlighted result or queue entry (the playing song if nothing is highlighted) to a file; a popup asks for the path, Tab completes it (needs ffmpeg) |
| `:auto` | `a` | switch autodj on or off (off at start): the next song then starts under the last 4 bars of the playing one (inside its last 30 s, where its beat is playing) and comes in where its own beat starts, even a minute or more into the song, beat-matched (its tempo adjusted by up to 6%; a song at twice or half the tempo, like 70 into 140 BPM, is matched in double or half time, every beat of the slower one meeting a beat of the faster one, with no big speed change) when both songs have a beat the analysis is sure of, otherwise a plain 6 s crossfade; the DJ popup says which it will be. Until you use `:auto` or `:dj` only the playing song is analysed, lightly (tempo and key, one core); after that it is analysed completely and the whole queue in parallel (up to four songs at once, on up to half the cores) |
| `:dj` | | DJ popup: the playing song and the queue with their BPM and key (coloured by position on the Camelot wheel, so mixable keys look alike); `j` `k` select, Esc or `:dj` closes. The command line opens over it, and these run inside it: `:sort`, `:bpm`, `:auto`, `:shuffle`, `:undo`, `:vol`, `:mute`, `:cast`, `:help` and `:quit` |
| `:sort [build\|down]` | `b` | in the DJ popup: arrange the queue so each song follows the one before it smoothly, by tempo (double and half time count as equal), key and energy (loudness and how much of the song is sustained): the playing song stays, the rest drift from it instead of starting at the slowest. `:sort build` also pulls the more energetic songs to the end, `:sort down` to the start. If the playing song has only just started (under 3 s) it is arranged too, and the best opener plays. Songs without a BPM go last |
| `:bpm N` | `e` | in the DJ popup: set the highlighted song's BPM by hand (`*`; kept until ytplay quits); `:bpm` alone goes back to the detected one |
| `:radio N` | `r` | add up to N songs similar to the queue (1 to 50, default 10) |
| `:comments` | `c` | the comments of the playing song, as many below each other as fit in the wide layout; when all fetched ones are in view it loads 20 more from YouTube (up to 100) and adds them below (scrolling to them if they are out of view), otherwise it pages on; after the last one it starts again at the first |
| `:clear` | `X` | clear the queue (asks first: `y` or `n`; the playing song keeps going) |
| `:help` | `h`, `?` | help popup (`j` `k` scroll on a small terminal) |
| `:quit` | `q` | quit |

Casting (`:cast`) plays one song at a time on the speaker, which loads the stream from YouTube (AAC) or SoundCloud (HLS)
by itself: autodj mixes and beat-matching only happen on this computer and are switched off while casting (silence
trimming keeps working). A song needs a few seconds to start (its stream address is looked up first). If the speaker goes
away, playback comes back to this computer from where it was. Songs that cannot be streamed (DRM protected, or blocked
for the speaker) are skipped with "Could not play".

Silence at the ends of songs is skipped: when a song starts, ytplay jumps past leading silence (more than a second
of it), and when the sound ends it goes on to the next song, if more than a second and a half of silence would follow. "Silence" is
audio below 1% of the song's loudest moment (-40 dB). This uses the audible range the analysis measures, so it works for a
song once it has been analysed: the next song in the queue is analysed (lightly) while the playing one plays, so its start
is skipped from the first moment, while the very first song only gets its end trimmed. Autodj plans its own hand-overs;
songs of which only part was analysed (very long tracks) are trimmed where the analysis knows.

The colours of the screen (highlights, frames, the progress bar, popups) are taken from the cover of the
playing song: its most prominent colour, brightened to stay readable. A song without a cover, or with a gray one,
gets blue. While autodj is on, the line with the time says `⇄ AUTODJ`.

The playing song's key (in the line under its title) and its row in the queue are drawn in the colour of its Camelot key.
While `:auto` is on and the queue was sorted with `:sort`, songs added by `:radio` are sorted in as soon as they have been analysed (arranged again), so `:sort` does not need to be pressed twice.

A search looks at YouTube and SoundCloud at the same time (10 results each: YouTube's first, SoundCloud's below), and a
coloured dot in front of every song says where it is from: red for YouTube, orange for SoundCloud (also in the queue and
the DJ popup). If one of them fails the other's results are shown and the status says which failed. Cover, description,
statistics and comments come from the song's own site; SoundCloud comments are not ranked ("Comments", not "Top comments")
and say where in the track they were left (`@1:23`). Some SoundCloud tracks are DRM protected and cannot be played (the
status says so); `:radio` works from the YouTube songs of the queue only.

Terminals 108+ columns wide show three equal columns (now playing, queue, search);
narrower ones get a stacked layout. In the wide layout the search column is split once
there are results: the list above, and below it the cover, numbers and description of the
selected result, so `j` `k` browse through them (looked up once the selection rests for
a moment). Under the cover is the waveform of the playing
song: its loudness over time, mirrored around a middle line, the part already played in
the accent colour and the playhead in bold (beside the cover in the stacked layout; two
rows, four on a tall screen, none on a screen too short to leave room for the text
below it; while `:auto` is on, the stretch where the next song comes in is lit up in bold). It is drawn from the audio the analysis fetches anyway, so it appears
a few seconds into a song, as a flat line until then; of a track longer than 10
minutes only the middle is seen, and the rest stays a flat line. Below that ytplay shows description, views/likes, top comments and an
estimated BPM and key (worked out from the whole song; no BPM is shown for audio
without a steady beat, such as speech or ambient music; expect the occasional
half/double tempo or neighbouring key). While running, ytplay shows up in the
desktop media controls (MPRIS).

MIT licensed.
