---
name: dispatch-build
description: >-
  Start a /build run in its own place: a herdr-desk task whose run opens a fresh Claude session on
  opus at high effort, in a new worktree of the target repo, on the prompt `/build <task file>`.
  Watches the run, answering what blocks it; the build goes on there. Use when the user says
  "build X", "quick build X", "have big boy / little man do X", or hands over a bug fix, refactor,
  or feature to build. Not for edits the session can already dictate.
argument-hint: "<what to build, naming its plan, brief, or ticket file>"
---

# /dispatch-build — open the place a /build runs

/build assumes its session already sits in its own worktree of the target repo. This skill makes
that true through herdr-desk, the one launcher: it adds a task, routes it, starts its run, and
watches the run. It never briefs, builds, or answers for the build what the build can decide.

1. **The repo.** Take it from the file the request names (a plan, a brief), else the repo this
   session is in. `<repo>` is that repo's main checkout. When neither names one, ask which repo:
   the one question this skill asks.
2. **The request file.** The run starts with no history, so the request must sit in a file: a
   plan, a brief, or a ticket id. When the request lives only in this conversation, write it to
   `~/Programming/Scratch/agent-docs/<worktree>-<branch>/dispatch-<slug>.md` (this session's
   checkout and branch) with every decision and constraint the conversation settled. `<slug>` =
   the request in 2–4 kebab-case words plus `-` and 4 random hex characters
   (`openssl rand -hex 2`).
3. **Add the task.**
   `herdr-desk add -p <repo> -t "<the request in a few words>" -n "<the /build request: the file's absolute path, plus big boy / little man when the user said it>"`.
   It prints `T<n>`. The notes are what /build reads: the run's task file holds the title and
   the notes.
4. **Route it.**
   `herdr-desk set T<n> --isolation worktree --model opus --first-message '/build {task_file}'`.
   A `--from-branch <name>` request puts the flag in the template:
   `--first-message '/build --from-branch <name> {task_file}'`.
5. **Start the run.** `herdr-desk run start T<n> --json` prints the run; note its `id`,
   `state` and `pane`.
   - `running`: go on to step 6 with that pane.
   - `waiting`: the desk starts it when a slot frees. Poll `herdr-desk runs --json` with Monitor
     until this run has a `pane`, then go on.
   - A refusal (`not-allowed`, `cap-reached`, a failed run): end with one `needs you` line holding
     the refusal and the command `herdr-desk run start T<n>` for the user to run.
6. **Watch the run.** Run
   `herdr agent wait <pane> --until blocked --until idle --until done` via Bash
   `run_in_background`: /build asks its questions in text and ends its turn, so a question shows
   as `idle` or `done` as often as `blocked`. When it returns, ask the desk first:
   `herdr-desk runs --json`.
   - **The run is not listed:** it ended; the desk holds the result (`herdr-desk show T<n>`).
     Stop watching. A pane that stays open, or one that is gone, says nothing about the run.
   - **The run is listed:** read the screen (`herdr agent read <pane> --lines 30`) and
     `herdr-desk show T<n>`. A `blocked` task with a note naming the pane is a question; a turn
     that ended with no question needs nothing, so watch again.
     - **Claude's folder-trust question** ("Is this a project you created or one you trust?", the
       pane `blocked` before /build starts): Claude Code keys trust on the main checkout's root, so
       it asks only when `claude` has never been trusted in `<repo>`. Trusting a folder is the
       user's choice, never yours: escalate it as below, with the fix in the line (answer it in
       the pane, or run `claude` once in `<repo>`). Either trusts every later worktree of the repo.
     - **Answer** what the request file or the rules already settle (contract rule 5), and tell
       the session the answer is yours, not the user's: `herdr agent send-keys <pane> <keys>`
       for a picker (`down`, `enter`; read the screen again, since a picker can ask for a second
       `enter`), `herdr agent prompt <pane> "<text>"` for text.
     - **Escalate** the rest (a refused permission, a product decision): one `needs you` line
       naming `T<n>` and the pane, and what it waits on. The desk also marks the task blocked
       and shows it on the pane's sidebar row. When the user answers you, pass it on as your own
       decision, never as theirs: `herdr agent prompt <pane> "From the launching session (<your
       session id>), my decision, not the user's word: <answer>"`. Never quote it as a user
       ruling, and never write in the run's session log.

     Then watch again, from the session moving on:
     `herdr agent wait <pane> --until working && herdr agent wait <pane> --until blocked --until idle --until done`,
     via Bash `run_in_background`, and handle its return the same way.

   When the desk's coordinator is the launcher, it never answers a worker: it escalates every
   block.
7. **Report** `T<n>`, the run id, and the pane, then keep watching. A multi-unit plan is one
   dispatch: /build runs every unit of a plan from its one session, and the desk task shows each
   unit as a step.
