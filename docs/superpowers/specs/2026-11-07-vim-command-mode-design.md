# Vim-style command mode

Status: approved by the user 2026-11-07 ("start implementing directly") and
implemented. Verified: unit tests (`commands_test.go`, with the old key tests
converted), mutation checks of the command logic, and the real binary under a pty
(`:find`, `:add`, Tab and `x`, `:skip`, `:auto` with the green theme, an unknown
command, `:help`, `:q`; the old letters do nothing).

## What the user wants

Their words: "change the keyinput to be like vim? you need to type ':' and then
a keyword (or only a single character). eg ':find NAME' (':f NAME') -> search
function, ':skip' ':s' skips the current song, 'tab' switches between the search
window and queue, 'j/k' up down navigation, ':auto' ':a' starts the auto mix dj,
':delete' ':x' (or just x) deletes the current song in the queue that is
highlighted, ':download' or ':d' downloads a song (i want to be able to highlight
a song in the search window or queue window and then hit download). start stop
can remain space bar, 0-9 is still fine. ':help' or ':h' is help."

Their answers when asked: the old single-letter keys **stop working outside the
colon commands**; **`:q` quits, `:add` queues**.

## The key map

Direct keys (no colon), after this change:

| Key | Does |
| --- | --- |
| `:` | opens the command line |
| `/` | opens the search bar, as `:find` with no name does (*my call: a vim habit and not a letter*) |
| `j` `k` | move in the results, the queue, the DJ popup |
| `Tab` | switches between the results and the queue |
| `x` | removes the highlighted queue entry (like `:delete`) |
| `↑` `↓` | move the highlighted queue entry up or down |
| `Enter` | plays the highlighted result now |
| `Esc` | backs out: command line, queue focus, results focus, popups |
| `Space` | pause and resume |
| `0`-`9`, `=` | seek to 0% to 90%, and 100% |
| `Ctrl+C` | quits |

Every other single key stops doing what it did: `s d q r h D X Q b e a y n`
outside their dialogs. (`y`, `n`, `Enter`, `Esc` still answer a yes/no popup,
and typing still types in the search bar and the path field of the download
popup: those are text input and answers, not commands.)

Commands, a name or its short form, `:name arguments`:

| Command | Short | Does |
| --- | --- | --- |
| `:find NAME` | `:f` | runs a search for NAME; without NAME opens the search bar |
| `:add` | `:ad` | appends the highlighted result to the queue |
| `:skip` | `:s` | skips to the next song |
| `:delete` | `:x` | removes the highlighted queue entry |
| `:download` | `:d` | saves the highlighted result or queue entry (the playing song if nothing is highlighted) to a file; the popup asking for the path stays |
| `:auto` | `:a` | switches autodj on or off (the next song starts under the end of the playing one) |
| `:dj` | | opens the DJ popup (queue with BPM); `Esc` closes it |
| `:sort` | `:b` | in the DJ popup: sort the queue by BPM, again: reverse |
| `:bpm N` | `:e` | in the DJ popup: set the highlighted song's BPM to N; `:bpm` alone goes back to the detected one |
| `:radio` | `:r` | adds songs similar to the queue |
| `:clear` | `:X` | clears the queue (asks first) |
| `:help` | `:h` | the help popup |
| `:quit` | `:q` | quits |

*My calls in the table:* `:ad`, `:clear`, `:radio`, `:dj`, `:sort`, `:bpm`, `:quit`
exist because every function behind an old key needs a place to go; `:X` and `:b`
and `:e` and `:r` keep the old letter as the short form.

## Behaviour

**The command line.** `:` opens a one-line input at the bottom, where the status
line is, with the prompt `:`. `Enter` runs it, `Esc` (or `Backspace` on an empty
line) cancels, and afterwards focus is where it was. It opens from the normal,
results, queue and DJ states; not while a text field has the keyboard (typing `:`
in the search bar types a colon) or in the yes/no, help and download popups.
*My calls:* a command is matched by exact name or short form first, then by a unique
prefix (`:fin`, `:down`, `:ad`); an ambiguous prefix says which names it could be.
An unknown command shows `Unknown command: foo (:help lists them)` as an error in
the status line. `Up` and `Down` step through the commands of this session;
`Tab` completes a name.

**What "highlighted" means.** The results list's selection when the results have
focus; the queue's cursor when the queue has focus; the DJ popup's cursor inside the
popup. `:download` with the normal state (nothing highlighted) saves the playing
song, as `d` did. `:delete` and `x` need the queue to have focus: from the results
they say "Press Tab to focus the queue first". `:add` needs a highlighted result.

**Inside the DJ popup** the command line takes the popup's commands (`:sort`,
`:bpm`), `:auto`, `:help` and `:quit`; any other command says "Close the popup first
(Esc)" (*my call*: a command that moves focus or opens another popup from under an
open one is more surprising than refused). `:dj` there closes it. The help popup is
closed by `Esc` or `Enter` (it was `h`, `q`, `Q` too); the yes/no popup keeps `y`,
`n`, `Enter`, `Esc`.

**Autodj anywhere.** `:auto` works from any state, not only inside the DJ popup, so
it also switches on the DJ analyses (a full analysis of the playing song and the
queue) the way opening the popup does, without opening it. The BPM commands
(`:sort`, `:bpm`) work only while the DJ popup is open (otherwise: "Open the DJ
popup first (:dj)"), as `b` and `e` did.

**The DJ popup loses its edit field.** `e` used to open an input for the BPM;
`:bpm 128` takes the number as an argument instead, so the edit state, its key
handler and its prompt go. (*My call.* Invalid numbers are rejected with the same
messages as today.)

**Help and README** are rewritten for the new keys and commands. The help popup is
generated from the same table the command line runs, so no command can be left
out, and it scrolls with `j` and `k` on a terminal too small for all of it (*my
call*: it has about 30 rows).

**Autodj's green theme, the status messages and everything else** stay as they are.

## How it is built

A command is a name, its short forms, and a function from the model and the argument
text to the model and a command, calling the same methods the keys call today
(`startSearch`, `m.deps.Player.Skip`, `openDownload`, `toggleAutoDJ`, `openDJ`, ...).
The old key handlers lose the letters that moved; the movement keys stay. One new
mode (`modeCommand`) holds the line, its history and the mode to return to.

## Tests

- A table test for the parser: names, short forms, unique and ambiguous prefixes,
  arguments with spaces (`:find daft punk`), unknown commands, empty line.
- Each command's effect, through the real command line (typing `:` + the text +
  `Enter`), for the same behaviours the key tests covered.
- The old letters do nothing: for every old key in every state, the model is
  unchanged (except where it stays: `x`, `/`, Enter, Tab, j, k, Space, digits).
- The existing key tests (about 150 key presses in 16 files) are converted to
  commands, not deleted: each keeps the behaviour it checks.

## Not in this change

Offered and not decided by the user: `:seek 1:30`, a volume control (`:vol`, `+`
`-`; there is none today), `:prev`, undo (`u`) for queue edits, counts (`5j`) and
`gg`/`G`, moving queue entries with `J`/`K`, `:h COMMAND`, a mode indicator.
Remappable keys; a config file for aliases.

## Risks

- **Muscle memory.** The letters that did something now do nothing, silently. The
  user chose this; a hint in the status line ("Use :skip") would be a small
  addition if it annoys them.
- **Test conversion** is the bulk of the work: about 150 key presses across 16
  test files. The risk is converting a test so that it no longer checks what it
  did; the old behaviour is the spec for each.
- **`:` on other keyboard layouts** is a modified key on some (Shift on a German
  one); it is a normal key press for Bubble Tea.

## Left to the builder

How the command line is drawn (the width, a cursor), the data structure of the
command table, how history is stored, the exact wording of the help popup and the
error messages, and the order in which the old keys are removed.
