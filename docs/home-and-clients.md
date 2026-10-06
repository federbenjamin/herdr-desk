# Home and clients

The home owns the store. By default it is the machine you ran `herdr-desk setup` on. Every
`herdr-desk` command on it opens the SQLite file, does its work, and closes it. Write transactions begin
`IMMEDIATE`, so two commands at once wait on the busy timeout instead of failing. A config edit takes
effect on the next command, with no restart.

To use the home from a second machine, make sure `ssh <home>` works without a prompt and finds
`herdr-desk` on the home's PATH in a non-interactive shell. Then, on the client:

```sh
herdr-desk client add <ssh target>      # for example you@home-host, or a Host from ~/.ssh/config
herdr-desk list
```

`client add` sends a `status` request to the home and saves `[client] home` only when the home answers.
A client sends each request as one JSON object on the stdin of its `[client] command`, which runs
`herdr-desk rpc` on the home and carries one JSON response back, refusal codes included. The default is

```toml
[client]
home = "you@home-host"
command = ["ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "ControlMaster=auto",
           "-o", "ControlPath={control}", "-o", "ControlPersist=60", "{home}", "herdr-desk", "rpc"]
```

`{home}` and `{control}` are filled in; each element is one argv word, never run through a shell.
`{control}` is the ssh control socket `<state folder>/ssh-<8 hex>`, the 8 hex the start of the home's
SHA-256, so each home gets its own. ssh adds 17 bytes to it while it binds the socket, and macOS allows a
socket path of 103 bytes, so the state folder (`$XDG_STATE_HOME/herdr-desk`) may be up to 73 bytes. With a
longer one, every request fails before ssh runs, naming the length; set `XDG_STATE_HOME` to a shorter folder,
or set a `command` without `{control}`. ssh keeps one connection open for 60 seconds, so a second call
within a minute does not pay for a new one. Set `command` to use `tailscale ssh`, a jump host, or any
program that carries stdin to `herdr-desk rpc` on the home and its stdout back.

Each request carries the wire version of the binary that sent it. A home of another version refuses the
request with an error naming both versions, and a client keeps what it queued until it reaches a home of its
own version: install the same herdr-desk on both machines. A request with a field the home does not know is
refused whole.

A client never opens a store, runs a ticker, or tracks runs: `herdr-desk ticker` and `herdr-desk coordinator`
there print where to run them and exit 0, `herdr-desk rpc` exits 2, and `herdr-desk hook herdr-event`
exits 0 before any transport.

The transport failing (ssh exits 255, or the 5-second connect timeout passes) is `home-unreachable`
(exit 3). With the home unreachable, a client:

- answers bare `herdr-desk` and `herdr-desk list` (with no flag, `--ready`, `--open`, `-p`, or `--desk`) from the
  snapshot of its last read, marked offline;
- refuses every other read and every task write with `home-unreachable` (exit 3);
- accepts `note`, `decide`, and the hook's records into a local outbox and forwards them on the next
  successful call (`queued` on stdout, and stderr says why the home did not take them). The entries keep
  their original time. Delivery is at least once. A queued entry the home refuses is dropped and named
  on stderr; one it cannot take yet (`scan-failed`, a server error) stays queued, and the command that
  tried to forward it fails naming the outbox file.

The offline notice on stderr gives the snapshot's time in UTC, like every time herdr-desk prints:
`herdr-desk list: the home did not answer; showing the snapshot from 2026-10-04 19:29Z`.

The board on a client runs on the client and sends each refresh through the same `[client] command`;
with the home unreachable it shows the snapshot.
