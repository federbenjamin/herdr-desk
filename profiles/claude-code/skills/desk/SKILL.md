---
name: desk
description: Use the desk CLI to read, add, and update the user's tasks, and to record notes and decisions in the session journal. Use it when the user mentions a task (T12), asks what is on their desk, or when you finish, get blocked on, or want to propose work.
---

# desk

`desk` is the user's task board and session journal. One home machine owns the data; every command talks to it. You act on it through the `desk` command only.

## What you may do

- Set a task's status to `review` (work is done and waits for the user) or `blocked` (you cannot go on without the user).
- Never set `ready` or `done`. The user arms a task with `ready` and closes it with `done`. A refusal `not-allowed` means you tried; stop and report it.
- Propose new work with `--thread agent`. It lands in the user's inbox. It does not start anything.
- Never edit a task's title or notes to answer a question. Write a note instead.

## Commands

Task ids: `T12`, `t12`, and `12` name the same task.

| goal | command |
|---|---|
| list live tasks | `desk list --json` (also `--ready`, `--open`, `--done`, `--archived`, `--all`, `-p <project>`) |
| read one task and its history | `desk show T12 --json` |
| propose a task | `desk add -t "<title>" -n "<notes>" --thread agent` |
| hand a task back | `desk set T12 review` or `desk set T12 blocked` |
| hand back with the PR | `desk set T12 review --ref <pr-url>`; add `--merged` once the PR is merged |
| tick steps | `desk steps T12 add "<text>"`, `toggle s1`, `rename s1 "<text>"`, `remove s1` |
| record a fact | `desk note "<text>" --task T12 --ref <path-or-url>` |
| record a decision | `desk decide "<text>" --tag k:v` (`--replaces e<id>` when it replaces one) |
| read the session journal | `desk session <id> --md` (no id: the current session) |

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
| 1 | refused, with a stable code first on stderr: `desk <command>: <code>: <message>` | fix the cause; do not repeat the same call |
| 2 | usage error, or the refusal `bad-input` | fix the arguments |
| 3 | the store or the home could not be reached or read (`home-unreachable`, `scan-failed`) | do not retry in a loop; tell the user. `note` and `decide` are queued and sent later (stdout says `queued`) |

Refusal codes you may see: `unknown-task`, `unknown-project`, `unknown-step`, `unknown-event`, `empty-title`, `empty-text`, `secret-detected`, `not-allowed`, `bad-input`, `home-unreachable`, `scan-failed`.

- `secret-detected` names the pattern, never the text. Remove the secret and write the call again.
- `not-allowed` is final. Use `review` or `blocked`, or propose with `--thread agent`.
