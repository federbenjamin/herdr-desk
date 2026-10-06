---
name: herdr-desk
description: Use the herdr-desk CLI to read, add, and update the user's tasks, and to record notes and decisions in the session journal. Use it when the user mentions a task (T12), asks what is on their desk, or when you finish, get blocked on, or want to propose work.
---

# herdr-desk

`herdr-desk` is the user's task board and session journal. One home machine owns the data; every command reaches it. You act on it through the `herdr-desk` command only.

## What you may do

- Set a task's status to `review` (work is done and waits for the user), `blocked` (you cannot go on without the user), or `done`.
- Set `ready` only when the desk's `[coordinator] start_runs` is `auto`. Otherwise `ready` is the user's approval, and a refusal `not-allowed` means you tried; stop and report it.
- Propose new work with `--thread agent`. It lands in the user's inbox. It does not start anything.
- Never run `herdr-desk runs kill`, `herdr-desk runner pause`, or `herdr-desk runner resume`: killing a run and pausing the runner are the user's acts. Run `herdr-desk run start` only in a root whose `agents_may_start` is set; the user and the desk's coordinator start runs anywhere. A session that owns a live run (a worker) never may start one. `not-allowed` is final.
- Never edit a task's title or notes to answer a question. Write a note instead. To add a line to a task's notes, use `herdr-desk edit T12 --append-notes "<text>"`; `--notes` replaces them.
- Never run bare `herdr-desk` or `herdr-desk capture` on a terminal. The board and the capture popup are the user's: they take the terminal until a key ends them. Read tasks with `herdr-desk list --json`.

## When a run started you

A task a run started has `DESK_TASK`, `DESK_RUN`, and `DESK_SESSION` set in your pane, and your first message says what to do. Hand the task back when you stop:

- Finished: `herdr-desk set T12 review --ref <a file or PR that shows the work>`; add `--merged` when that PR is merged.
- You need an answer: `herdr-desk note --task T12 "<what you need>"`, then `herdr-desk set T12 blocked`. The user answers in a note and the task runs again; the next run's first message holds the history.
- Every write you make carries your run id. A refusal `stale-run` means a newer run owns the task: stop and do nothing more to it.

## Commands

Task ids: `T12`, `t12`, and `12` name the same task.

| goal | command |
|---|---|
| list live tasks | `herdr-desk list --json` (also `--ready`, `--open`, `--done`, `--archived`, `--all`, `-p <project>`) |
| read one task and its history | `herdr-desk show T12 --json` |
| propose a task | `herdr-desk add -t "<title>" -n "<notes>" --thread agent` |
| hand a task back | `herdr-desk set T12 review` or `herdr-desk set T12 blocked` |
| hand back with the PR | `herdr-desk set T12 review --ref <pr-url>`; add `--merged` once the PR is merged |
| close a task | `herdr-desk set T12 done` |
| add to a task's notes | `herdr-desk edit T12 --append-notes "<text>"` |
| tick steps | `herdr-desk steps T12 add "<text>"`, `add --id <id> "<text>"` (you pick the id; never `s<n>`; a second `add --id` with the same id changes nothing), `done <id>` (prints `changed` or `unchanged`; never flips back), `toggle s1`, `rename s1 "<text>"`, `remove s1` |
| record a fact | `herdr-desk note "<text>" --task T12 --ref <path-or-url>` |
| record a decision | `herdr-desk decide "<text>" --tag k:v` (`--replaces e<id>` when it replaces one) |
| read the session journal | `herdr-desk session <id> --md` (no id: the current session); the file the session-start hook named is rewritten after each write of yours, so reading that path is current too |

Rules:

- Read with `--json` and parse it. Do not scrape the text output.
- Use `note --ref` for every file or PR a note is about, so the journal links it.
- Use `decide --tag k:v` for choices worth keeping, for example `--tag area:auth`.
- Task text, notes, and refs are scanned for secrets. Never put a key, token, or password in one.
- A project is the main checkout of the repo you are in. A worktree resolves to its main checkout. `--desk` means no project.

## Exit codes

Never retry blind. Read the code first.

| exit | meaning | what to do |
|---|---|---|
| 0 | done, or already true | go on |
| 1 | refused, with a stable code first on stderr: `herdr-desk <command>: <code>: <message>` | fix the cause; do not repeat the same call |
| 2 | usage error, or the refusal `bad-input` | fix the arguments |
| 3 | the store or the home could not be reached or read (`home-unreachable`, `scan-failed`) | do not retry in a loop; tell the user. `note` and `decide` are queued and sent later (stdout says `queued`) |

Refusal codes you may see: `unknown-task`, `unknown-project`, `unknown-step`, `unknown-event`, `empty-title`, `empty-text`, `secret-detected`, `not-allowed`, `stale-run`, `stale`, `no-run`, `runner-off`, `runner-paused`, `cap-reached`, `no-herdr`, `run-failed`, `backup-off`, `bad-input`, `home-unreachable`, `scan-failed`.

- `secret-detected` names the pattern, never the text. Remove the secret and write the call again.
- `stale-run` is final: a newer run owns the task, so do not write to it again.
- `stale` means the notes changed after you read them and nothing was written. Read the task again, then add your line with `edit --append-notes` or write a note.
- `not-allowed` is final. Use `review`, `blocked`, or `done`, or propose with `--thread agent`.
