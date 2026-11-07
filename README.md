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
| Enter | play the highlighted result now |
| Tab | switch between the results and the queue |
| `x` | remove the highlighted queue entry (`↑` `↓` move it) |
| Space | pause / resume |
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
| `:skip` | `:s` | skip |
| `:delete` | `:x` | remove the highlighted queue entry (the key `x` does the same) |
| `:download` | `:d` | save the highlighted result or queue entry (the playing song if nothing is highlighted) to a file; a popup asks for the path, Tab completes it (needs ffmpeg) |
| `:auto` | `:a` | switch autodj on or off (off at start): the next song then starts under the last 4 bars of the playing one (inside its last 30 s, where its beat is playing) and comes in where its own beat starts, even a minute or more into the song, beat-matched (its tempo adjusted by up to 6%; a song at twice or half the tempo, like 70 into 140 BPM, is matched in double or half time, every beat of the slower one meeting a beat of the faster one, with no big speed change) when both songs have a beat the analysis is sure of, otherwise a plain 6 s crossfade; the DJ popup says which it will be. Until you use `:auto` or `:dj` only the playing song is analysed, lightly (tempo and key, one core); after that it is analysed completely and the whole queue in parallel (up to four songs at once, on up to half the cores) |
| `:dj` | | DJ popup: the playing song and the queue with their BPM; `j` `k` select, Esc or `:dj` closes. Only `:sort`, `:bpm`, `:auto`, `:help` and `:quit` run while it is open |
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
