# SoundCloud as a second source — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (inline) to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax. **No commit steps**: the user commits when they say so (see memory `commit-only-when-asked-in-ytplay`); no worktrees.

**Goal:** One search shows YouTube and SoundCloud results together, each marked with a coloured icon; both play, queue, download, load from links, and show cover, meta info and comments.

**Architecture:** Songs are identified everywhere by a string *key*. For YouTube the key stays the bare video id (so nothing existing changes); for SoundCloud it is `sc:<user>/<track>`. Two functions, `yt.KeyOf(url)` and `yt.URLOf(key)`, replace every place that parses or builds a `watch?v=` URL, and URLs are always rebuilt from a validated key, never from pasted text. Search runs `ytsearchN:` and `scsearchN:` in parallel and interleaves the results. Cover, info and links get the source from the key.

**Tech Stack:** Go, yt-dlp (`scsearchN:`, `--write-comments`, flat JSON), mpv through the yt-dlp hook (plays SoundCloud as is), Bubble Tea.

**Spec:** the user's request, 2026-11-08: "search a name and see YT and SC results (maybe draw some colored icon next to it). comments, meta info and cover can be recovered from soundcloud". No design document; the calls below are mine and listed under *Rulings to confirm*.

## Facts checked against the real yt-dlp (2026.10.21)

- `yt-dlp --flat-playlist --dump-json scsearch3:NAME` returns, per entry: `id` (number), `title`, `uploader`, `duration`, `webpage_url` (`https://soundcloud.com/user/slug`), `view_count`, `like_count`, `comment_count`, `release_year`, `description`, `thumbnails` (ids `mini`…`t300x300`, `t500x500`, `original`).
- `--write-comments --dump-single-json --no-playlist URL` returns comments with `author`, `text`, `timestamp`, `start_time` (the track position it was left at), **no likes**, and ignores a maximum: all of them come back (574 on one mix).
- Some tracks are **DRM protected** (`ERROR: This video is DRM protected`, e.g. the Daft Punk official upload): they cannot be played, analysed or downloaded.
- The numeric `id` cannot be turned back into a URL without a client id, so the key uses the `user/slug` path.

## Global Constraints

- YouTube behaviour and all existing keys stay as they are: key of a YouTube song = the 11-character id; every existing test keeps passing unchanged unless it names `VideoID` or a watch URL.
- A URL handed to yt-dlp or mpv is built by `yt.URLOf(key)` from a key that matched `^sc:[A-Za-z0-9_-]+/[A-Za-z0-9_-]+$` (SoundCloud) or the YouTube id pattern; anything else yields `""` and is refused.
- Colours of a source icon live in one new file (`internal/ui/source.go`), not in `view.go` (a test forbids colour numbers there except 245 and 196). Icon is one cell wide.
- A failing source never hides the other: results of the working one are shown with a status naming the failure.
- Radio (`:radio`) stays YouTube-only.
- No new dependency.

## Review Focus

- A SoundCloud track whose path has dots, `%` escapes or uppercase (`user.name/Track_1`): the key and the rebuilt URL must match what yt-dlp accepts, and a hostile string must never reach yt-dlp.
- One source times out or errors: the other's results appear, status says which failed; both fail: one clear error.
- A DRM-protected SoundCloud track: searching shows it (marked), playing it fails with a readable message, the queue moves on and the analysis fails quietly (no repeat attempts).
- SoundCloud comments have no likes and come all at once (hundreds): show no `(0)` prefix, cap to what `:comments` asks for, label "Comments" (they are not "top").
- Mixed queue: autodj, undo, `:download`, `:load` of a set link, cover and key colours all work for a queue that holds both sources.
- Narrow and stacked layouts: the icon must not push the title or duration out of the row.

---

### Task 1: Source-aware keys (pure refactor)

**Files:**
- Create: `internal/yt/key.go`, `internal/yt/key_test.go`
- Modify: `internal/yt/search.go` (`Result.URL`, `VideoID`), `internal/yt/info.go:54`, `internal/yt/download.go:39`, `internal/analyze/decode.go:86`, and the `yt.VideoID(...)` callers in `internal/ui/{model,view,songs,commands,wave}.go`

**Interfaces:**
- Produces:
  ```go
  type Source int            // YouTube = 0 (zero value), SoundCloud
  func KeyOf(rawURL string) string   // watch URL -> id; soundcloud URL -> "sc:user/slug"; "" otherwise
  func URLOf(key string) string      // inverse; "" for an invalid key
  func SourceOf(key string) Source   // "sc:" prefix -> SoundCloud
  ```
  `VideoID` becomes an alias of `KeyOf` for one step, then every caller is renamed and it is deleted.
- Consumes: nothing.

- [ ] **Step 1: Write the failing test** `TestKeyAndURLRoundTrip`: table of (url → key → url) for YouTube watch URLs with extra params, `https://soundcloud.com/user.name/Track_1?si=x` → `sc:user.name/Track_1` (**ruling: dots allowed in the user part**) → canonical URL; rejected: `sc:a/b/c`, `sc:../x`, `sc:a/`, `javascript:…`, empty, `https://soundcloud.com.evil.com/a/b`, `https://soundcloud.com/a` (a profile, not a track), `https://soundcloud.com/a/sets/b` (a set, not a track: Task 5).
- [ ] **Step 2: Run it** — Expected: FAIL, `undefined: KeyOf`.
- [ ] **Step 3: Implement `key.go`** (regexes anchored; SoundCloud hosts `soundcloud.com`, `www.`, `m.`; query and fragment dropped) and make `Result.URL()`, `FetchInfo`, `Download`, `analyze.Fetch` call `URLOf` (Fetch and FetchInfo return an error for `""`).
- [ ] **Step 4: Rename the callers** `yt.VideoID` → `yt.KeyOf` (about 20 call sites), delete `VideoID`.
- [ ] **Step 5: Run** `make check` — Expected: all green with no test changed except renames.

### Task 2: SoundCloud search and the merged result list

**Files:**
- Create: `internal/yt/soundcloud.go`, `internal/yt/soundcloud_test.go`
- Modify: `internal/yt/search.go` (`Result` gets `Source`, `Thumb`; `parseEntry` fills them), `cmd/ytplay/main.go`, `internal/ui/model.go` (`Deps.Search`)

**Interfaces:**
- Produces:
  ```go
  // Result gains: Source Source; Thumb string (best thumbnail URL, "" if none); Protected bool
  func SearchAll(ctx context.Context, ytdlpPath, query string, n int) (results []Result, failed map[Source]error, err error)
  ```
  `n` per source (default 10 each); results interleaved YT1, SC1, YT2, SC2…; `err` only when both failed; `failed` names the one that did.
- `parseEntry` for SoundCloud: `ID` = `KeyOf(webpage_url)`; `Thumb` = the `t500x500` thumbnail (fall back to `large`). *Correction after checking:* the flat JSON has `formats: null`, so DRM cannot be known at search time; a play failure is the only signal (Task 6), and there is no `Protected` field or `⊘` icon.

- [ ] **Step 1: Failing tests** with a fake yt-dlp script (the pattern of `recordingYtdlp` in `link_test.go`): both sources answer → interleaved, correct keys and thumbs; `scsearch` fails → YouTube results and `failed[SoundCloud]`; both fail → `err`; context cancelled; entries without `webpage_url` skipped; the fake records that both `ytsearch10:` and `scsearch10:` were asked, in parallel (both started before either returned).
- [ ] **Step 2: Run** — Expected: FAIL (undefined).
- [ ] **Step 3: Implement** with two goroutines and a shared context; reuse `runListFor`.
- [ ] **Step 4: Run** the package — Expected: PASS; mutation-check the interleave and the failure handling.

### Task 3: Cover from a thumbnail URL

**Files:** Modify `internal/cover/cover.go`, `cover_test.go`, the cover fetch in `internal/ui/model.go`.

**Interfaces:**
- Consumes: `Result.Thumb` / `Result.ThumbURL()`.
- Produces: `cover.FetchURL(ctx, url string) (image.Image, error)` (`Fetch(ctx, id)` becomes `FetchURL(ctx, yt.Result{ID:id}.ThumbURL())`); SoundCloud artwork is square (500×500): crop to the cover's 32×9-cell aspect from the centre instead of stretching. `ThumbURL()` returns `Thumb` for SoundCloud and the `ytimg` URL for YouTube.

- [ ] **Step 1: Failing tests** with the existing local test server: a square image is centre-cropped to the 16:9 target (check which rows of a striped image survive); a URL on another host than `i1.sndcdn.com`/`i.ytimg.com` is refused (**ruling: host allow-list**, the URL comes from yt-dlp output, not the user).
- [ ] **Step 2–4:** run (FAIL), implement, run (PASS). The model looks the thumbnail up in `m.tracks[key]`; a song with no registered result and a SoundCloud key shows the placeholder.

### Task 4: Meta info and comments for SoundCloud

**Files:** Modify `internal/yt/info.go`, `info_test.go`, `internal/ui/comments.go`, `internal/ui/view.go` (header and like prefix).

**Interfaces:**
- Produces: `FetchInfo(ctx, ytdlp, key, maxComments)` works for SoundCloud keys: `Description`, `Views`, `Likes`, `Year` from `release_year`, `Comments` capped to `maxComments` after the fetch (yt-dlp ignores the maximum); `Info.Source` so the UI can label. For SoundCloud, `Comment.Likes` is 0 and a new `Comment.At float64` holds `start_time`.
- UI: comment header says "Comments" (not "Top comments") for SoundCloud; no `(0) ` prefix when likes are 0 (already so); optional `@1:23 ` prefix from `At` (**ruling: show it, dim**).

- [ ] **Step 1: Failing tests** with a fake yt-dlp emitting the real JSON shape above (574 comments): capped to 10 and to 30 on the second fetch; likes 0; `At` kept; description, views, likes, year parsed; a DRM error (`stderr` text, exit 1) gives an error naming it.
- [ ] **Step 2–4:** run (FAIL), implement (pass `--write-comments` and the comment cap as for YouTube; slice afterwards), run (PASS). UI test: header text per source; the `:comments` paging tests still pass for both.

### Task 5: SoundCloud links (`:load`, search bar, `:find`)

**Files:** Modify `internal/yt/link.go`, `link_test.go`, `internal/ui/load.go` (`linkHelp`).

**Interfaces:**
- `ParseLink` accepts `soundcloud.com`, `www.`, `m.`, `on.soundcloud.com` is **not** accepted (short links redirect; ruling: refuse with the help text, a redirect would need a network call to resolve safely). Track `user/slug` → `Link{VideoLink, key}`; set `user/sets/slug` → `Link{PlaylistLink, "sc:user/sets/slug"}`; a profile (`/user`) and `/user/likes` etc. → `NotALink`.
- `Link.URL()` rebuilds from the key; `LoadLink` for a set uses `--flat-playlist` as for YouTube, entries become SoundCloud results (Task 2 parsing), same 500 cap.

- [ ] **Step 1: Failing tests** extending the accepted/rejected tables and `TestLinkURLIsRebuiltFromTheID`; a fake-yt-dlp set load of 3 tracks, one private.
- [ ] **Step 2–4:** run (FAIL), implement, run (PASS); mutation-check the host check and the profile/set distinction. Help text now names both sites.

### Task 6: Icons, merged results in the UI, playing and DRM

**Files:**
- Create: `internal/ui/source.go`, `internal/ui/source_test.go`
- Modify: `internal/ui/model.go` (search message, status on partial failure, play failures), `internal/ui/view.go` (`resultRow`, queue rows, DJ popup row), `cmd/ytplay/main.go`

**Interfaces:**
- Produces: `func (s yt.Source) icon(th theme) string` — one cell: `●` in YouTube red and SoundCloud orange (colours in `source.go`; the icon of a *protected* SoundCloud track is dim with `⊘`? **ruling: yes, `⊘` dim**).
- `Deps.Search` becomes `func(ctx, query) ([]yt.Result, map[yt.Source]error, error)`; the search message carries `failed`; status: `SoundCloud failed: <msg> (showing YouTube only)`.
- Rows: `icon + " " + title … channel … duration`; the title column shrinks by 2 in every layout.

- [ ] **Step 1: Failing tests:** result rows of both sources show their icons in their colours (`withColours`), width of every row is exact at widths 40…200 (**the icon never pushes the duration out**); queue rows and the DJ popup show the icon too; partial failure keeps results and sets an error status naming the source; both failing shows the error; icons are width 1 (`lipgloss.Width`).
- [ ] **Step 2: Run** — Expected: FAIL.
- [ ] **Step 3: Implement.** Play failure of a SoundCloud key (mpv reports a load error for the entry): status "Cannot play <title>: protected or removed", mpv continues with the next song; analysis failure for it is recorded once (`analysisFailed`), never retried.
- [ ] **Step 4: Run** `make check` — Expected: PASS; mutation-check icon choice, row width and the failure statuses.

### Task 7: Radio, docs and a real-binary check

**Files:** Modify `internal/yt/search.go` (`Similar` skips non-YouTube seeds), `README.md`, `docs/superpowers/specs/2026-11-07-vim-command-mode-design.md` ("Added after that").

- [ ] **Step 1: Failing test:** `Similar` with SoundCloud seeds only returns an error "radio needs a YouTube song"; mixed seeds use only the YouTube ones (the fake records the seeds asked for); the UI says so in the status.
- [ ] **Step 2–3:** run (FAIL), implement, run (PASS).
- [ ] **Step 4: Real binary under a pty** (scratchpad script as for `:load`): search `daft punk`, expect both icons in the result list; play a SoundCloud result (not protected), wait for the waveform and BPM, `:comments` loads comments, `:download` offers a file name, a pasted set link loads; then `make check`, `go test -race ./internal/ui ./internal/yt`.
- [ ] **Step 5: README** (sources, icons, `:load` links, the limits: no radio for SoundCloud, DRM tracks, comments not ranked), design doc bullet. Report to the user and offer the commit.

---

## Rulings to confirm (my calls, cheap to change)

1. **Key format** `sc:user/slug`, so YouTube keys and all existing code paths are untouched. Cost if wrong: a rename.
2. **Interleaved results**, 10 per source (YT1, SC1, YT2, …), not two sections. A section split (or a `:find sc NAME` filter) is an easy follow-up.
3. **Icons:** `●` red for YouTube, orange for SoundCloud; protected tracks `⊘` dim.
4. **No `on.soundcloud.com` short links** (they need a redirect to resolve).
5. **SoundCloud comments** are labelled "Comments", unranked, with the track position they were left at.
6. **Radio stays YouTube-only.**

## Risks

- DRM tracks look playable in search; the flat JSON may not say they are protected, so some can only be recognised when playing fails (handled in Task 6, but the user will see a few failures).
- yt-dlp's SoundCloud extractor depends on a client id it scrapes; if SoundCloud changes it, only SoundCloud breaks, and the partial-failure path (Task 2/6) keeps YouTube working.
- Fetching all SoundCloud comments of a popular track is slow and large (hundreds); `:comments` pays that once per song, then slices.
