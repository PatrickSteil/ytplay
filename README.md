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
| Esc | back out of whatever is open |
| Ctrl+C | quit |

The other letters do nothing on their own. Press `:`, type a command and press
Enter; Esc (or Backspace on an empty line) cancels, `↑` `↓` step through the
commands of this session and Tab completes a name. A command is its name or its
short form, or the start of its name if that is unambiguous (`:down`, `:fin`).
"Highlighted" is the selection of the list that has the focus: the results, or
the queue after Tab.

| Command | Short | Action |
| --- | --- | --- |
| `:find NAME` | `:f` | search for NAME (without a name: open the search bar) |
| `:add` | | add the highlighted result to the queue |
| `:play` | | play the highlighted result or queue entry now (Enter does the same) |
| `:skip` | `:s` | skip |
| `:prev` | `:p` | the song before; if this one has played for more than 3 s, its start |
| `:seek TIME` | | jump to `1:30`, `1:02:03` or `90` (seconds), to `50%`, or by `+10` / `-30` seconds |
| `:vol N` | `:v` | volume 0 to 100, or `+N` / `-N`; alone it says the volume. A master volume: the crossfades of autodj are scaled by it. Not kept between runs |
| `:mute` | `:m` | mute or unmute |
| `:delete` | `:x` | remove the highlighted queue entry (the key `x` does the same) |
| `:undo` | `:u` | undo the last queue change (the key `u` does the same) |
| `:shuffle` | | shuffle the queue (the playing song stays) |
| `:download` | `:d` | save the highlighted result or queue entry (the playing song if nothing is highlighted) to a file; a popup asks for the path, Tab completes it (needs ffmpeg) |
| `:auto` | `:a` | switch autodj on or off (off at start): the next song then starts under the last 4 bars of the playing one (inside its last 30 s, where its beat is playing) and comes in where its own beat starts, even a minute or more into the song, beat-matched (its tempo adjusted by up to 6%; a song at twice or half the tempo, like 70 into 140 BPM, is matched in double or half time, every beat of the slower one meeting a beat of the faster one, with no big speed change) when both songs have a beat the analysis is sure of, otherwise a plain 6 s crossfade; the DJ popup says which it will be. Until you use `:auto` or `:dj` only the playing song is analysed, lightly (tempo and key, one core); after that it is analysed completely and the whole queue in parallel (up to four songs at once, on up to half the cores) |
| `:dj` | | DJ popup: the playing song and the queue with their BPM; `j` `k` select, Esc or `:dj` closes. The command line opens over it, and these run inside it: `:sort`, `:bpm`, `:auto`, `:shuffle`, `:undo`, `:vol`, `:mute`, `:help` and `:quit` |
| `:sort` | `:b` | in the DJ popup: sort the queue by BPM (slowest first, again: fastest first) |
| `:bpm N` | `:e` | in the DJ popup: set the highlighted song's BPM by hand (`*`; kept until ytplay quits); `:bpm` alone goes back to the detected one |
| `:radio` | `:r` | add 20 songs similar to the queue |
| `:clear` | `:X` | clear the queue (asks first: `y` or `n`; the playing song keeps going) |
| `:help` | `:h` | help popup (`j` `k` scroll on a small terminal) |
| `:quit` | `:q` | quit |

Terminals 108+ columns wide show three equal columns (now playing, queue, search);
narrower ones get a stacked layout. Under the current song ytplay shows description, views/likes, top comments and an
estimated BPM and key (worked out from the whole song; no BPM is shown for audio
without a steady beat, such as speech or ambient music; expect the occasional
half/double tempo or neighbouring key). While running, ytplay shows up in the
desktop media controls (MPRIS).

MIT licensed.
