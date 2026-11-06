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

## Keys

| Key | Action |
| --- | --- |
| `/` | search (Enter runs it, Esc closes the bar) |
| `j` `k` | move in results |
| Enter | play the selected result now |
| `q` | append the selected result to the queue |
| `d` | save the selected result (or the playing song) to a file; a popup asks for the path, Tab completes it (needs ffmpeg) |
| `r` | add 20 songs similar to the queue |
| Tab | edit the queue: `j` `k` select, `↑` `↓` move, `x` remove, Tab/Esc back |
| `X` | clear the queue (asks first; the playing song keeps going) |
| `h` | help popup |
| `s` | skip |
| `0`–`9`, `=` | seek to 0%–90%, 100% |
| Space | pause / resume |
| `Q`, Ctrl+C | quit |

Terminals 108+ columns wide show three equal columns (now playing, queue, search);
narrower ones get a stacked layout. Under the current song ytplay shows description, views/likes, top comments and an
estimated BPM and key (worked out from a minute of audio; expect the occasional
half/double tempo or neighbouring key). While running, ytplay shows up in the
desktop media controls (MPRIS).

MIT licensed.
