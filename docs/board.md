# The board

Bare `herdr-desk` on a terminal is the board, in a herdr pane, a herdr popup, or any terminal. The
split pane (the action `open-board`, `prefix+t`) and the popup (the action `open-popup`) show the same
board, each laid out to the size herdr gives it; under 10 rows it draws one line saying how many it
needs. It refreshes every 3 seconds and after each write, writes as you (never as an agent), and uses
only the terminal's 16 ANSI colours. `ctrl+d` is bound to nothing. `ctrl+c` and `q` quit.

The header is `herdr-desk  <project> ▾  thread: <thread> ▾` on the left and the runner on the right:
`runner ● on · 2/3 · home` (live runs and the cap), `runner ◐ paused`, `runner ○ off · home`, and
`client` in place of `home` on a client machine. With the home unreachable it reads
`offline (snapshot 12m)`: the board shows the last snapshot, and every key that writes refuses with
`offline: <key> needs the home`. A refusal or error from the home shows on the status line until the next key.

Below the header are the three sections, each with its title even when empty. `NEEDS YOU` lists
blocked and review tasks, `IN MOTION` started ones (and the last note under the row), `ON DECK` the ready
ones, then `inbox` and the open ones. `d` adds a `DONE` section. A row whose task has a live run, in any
section, shows the run: its state, root, isolation, model, and elapsed time
(`running · alpha · worktree · gpt · 1h`); a row without one shows its project and how long ago it changed.

Width decides the layout: under 78 columns one surface at a time and rows without their right-hand
detail; from 78 to 109 one surface with full rows; from 110 the board on the left and the selected
task's page on the right.

Board page keys:

| key | does |
|---|---|
| `↓` `j` · `↑` · `g` · `G` | next row · previous row · first · last (`k` is kill, not up) |
| `enter` | open the task's page |
| `+` | add a task: one line, `#thread` and `@project` as in `herdr-desk capture`; a refused line stays in the box with its error. Until the home answers an `enter`, the line takes no keys, and `esc` closes the box once it answers |
| `n` | set `ready`; on a blocked task it asks `answer:`, appends your answer as a note, then sets `ready` (an empty answer sets `ready` alone); when the note cannot be written the prompt opens again with your answer |
| `S` | start a run of the task, as `herdr-desk run start` with no flags does; a refusal shows on the status line |
| `s` · `b` · `r` | set `started` · `blocked` · `review` |
| `x` | set `done`; asks `y/n` unless the task is in `review` |
| `a` | toggle the thread `agent` |
| `k` | kill the task's live run after `y/n`; the home sets the task `blocked` |
| `P` | pause or resume the runner |
| `/` | search titles and ids as you type; `enter` keeps the filter, `esc` clears it |
| `p` · `t` | next project filter · next thread filter, then back to all |
| `d` | open or close the done drawer: the 20 most recently updated done tasks |
| `?` | the keys overlay |
| `esc` | close the overlay, else the drawer, else clear the search |
| `q` · `ctrl+c` | quit |

`P` with no runner state reported follows the header: `on` pauses, anything else says the runner is off.
A paste goes to the open text input (a prompt, the add box, the notes editor).

Task page keys (the board's `n S s b r x a f k P ? q ctrl+c` work here too, for this task):

| key | does |
|---|---|
| `↓` `j` · `↑` | scroll |
| `esc` | back to the board |
| `e` | edit the notes in an editor; `ctrl+s` saves, `esc` discards. A save that fails opens the editor again with your text. When someone else changed the notes while you edited, the save is refused `stale`: your text stays, the status line says the notes changed, and a second `ctrl+s` replaces them. When the board cannot read the home's notes at that moment, the status line says so and a second `ctrl+s` tries the save again |
| `t` | steps mode: `↓` `j` `↑` move, `space` or `enter` toggles, `a` adds, `r` renames, `x` removes, `esc` leaves |
| `R` · `M` | edit the root · the model (an empty line clears it) |
| `I` | next isolation: none, `self`, `worktree`, `in-place` |
| `o` | open a ref: with several, a pick list; with none, the status line says so |

The page shows the head line, `root · isolation · model`, `NOTES`, `STEPS`, `HISTORY across <n> sessions`
(run starts, notes with their refs, decisions, hand-backs, `archived` and `unarchived`), and
`FILES`, the refs of the history. `o` opens an `http` or `https` URL with the OS opener, and a file
(an absolute path, or one relative to the task's project) in the herdr plugin `herdr-file-viewer`
when herdr lists it; with no viewer the status line says so. A ref reaches a command only as one
argument, never through a shell.
