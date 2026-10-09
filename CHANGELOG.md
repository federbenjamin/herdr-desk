# Changelog

## Unreleased

- `herdr-desk add --start` adds a task and starts its run in one call, taking `run start`'s route flags
  (`--root`, `--isolation`, `--model`, `--first-message`); `--json` prints `{"task": …, "run": …}`. A start
  that is refused leaves the task and names it in the refusal.
- History names who made each write: the runner's own writes (a hand-back from herdr's events or the ticker, a
  spawn's note) are `runner`, not `user`, and `run start` from an agent session records that session as `agent`.
- `herdr plugin install federbenjamin/herdr-desk` is the whole install: its build step places the binary,
  then runs `herdr-desk setup`, with the `claude-code` profile when `claude` is on PATH, and with `claude`
  on PATH installs the Claude Code plugin (`claude plugin marketplace add federbenjamin/herdr-desk`, then
  `claude plugin install herdr-desk@herdr-desk`). Without `claude` it says how to set `[agent]` and prints
  the two `/plugin` commands. A step that does not finish is reported, and the install still succeeds.
  herdr shows a build step's output only when it fails, so the report is also written to
  `<XDG_STATE_HOME or ~/.local/state>/herdr-desk/install.log`. `herdr-desk setup` re-runs setup; the two
  `claude plugin` commands re-run the Claude Code plugin step.
- `herdr-desk roots add` takes `--first-message <template>` and `--agents-may-start[=false]`, so neither key needs
  a hand edit of `config.toml`. An agent session that passes `--agents-may-start` is refused `not-allowed`: letting
  agents start runs is a person's choice.
- `herdr-desk setup` creates herdr's `config.toml` when herdr is found and has none, holding only
  herdr-desk's keys and sidebar row (0600, no backup). With no herdr found, or with `--no-herdr`, it
  still writes nothing there; with no herdr found it says why and to run `herdr-desk setup` once herdr is
  found.
- `herdr-desk setup` writes to `HERDR_CONFIG_PATH` when it is set, the file herdr itself reads then.
- Setting a task `done` marks the pane of each of its ended runs owed a close. The ticker closes it, then removes
  the task's worktree, so a worker's session no longer sits in a deleted folder after a `done` hand-back. A pane
  that will not close keeps the worktree until a later tick closes it.
- The runner no longer stops a run after `[runner] max_run_minutes`: a run waiting on another PR or on you was
  killed whatever its progress. `cap` and `max_runs_per_day` still bound the cost. A config that still sets
  `max_run_minutes` loads with the value ignored, and the next `herdr-desk setup` drops it.
- `herdr-desk set T<n> blocked --question <text>` blocks a task without ending its run, and notes the question
  naming the run's pane. A `/build` that asks in text and goes on working shows `blocked`, and an answer typed in
  the pane returns it to `started`. A pane that goes `idle` or `done` now sets `review` only from `started`, so a
  question's `blocked`, or a status a person set, stands. `/dispatch-build` watches for `blocked`, `idle`, and
  `done`, and takes a run's end from `herdr-desk runs`.
