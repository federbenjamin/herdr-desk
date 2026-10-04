#!/usr/bin/env python3
"""A stand-in for herdr, for tests. Standard library only.

State lives under $FAKE_HERDR_DIR: state.json, one <pane>.log per pane, notifications.log, and calls.log, one
line per call: the time it began and its arguments.
FAKE_HERDR_FAIL=<subcommand words joined by ->, for example pane-run, makes that subcommand exit 1.
DESK_HERDR names it, by its absolute path.
"""
import argparse
import fcntl
import json
import os
import signal
import subprocess
import sys
import time

def fail(msg):
    print("fake-herdr: " + msg, file=sys.stderr)
    sys.exit(1)


class Parser(argparse.ArgumentParser):
    def error(self, message):
        fail(message)


def state_dir():
    d = os.environ.get("FAKE_HERDR_DIR")
    if not d:
        fail("FAKE_HERDR_DIR is not set")
    os.makedirs(d, exist_ok=True)
    return d


def load(d):
    try:
        with open(os.path.join(d, "state.json")) as f:
            return json.load(f)
    except FileNotFoundError:
        return {"next_ws": 0, "next_pane": 0, "workspaces": {}, "panes": {}}


def save(d, st):
    tmp = os.path.join(d, "state.json.tmp")
    with open(tmp, "w") as f:
        json.dump(st, f)
    os.replace(tmp, os.path.join(d, "state.json"))


def group_procs(pgid):
    """The live (not zombie) processes of a process group, as (pid, name)."""
    if not pgid:
        return []
    out = subprocess.run(
        ["ps", "-ax", "-o", "pid=,pgid=,stat=,comm="], capture_output=True, text=True
    ).stdout
    procs = []
    for line in out.splitlines():
        parts = line.split(None, 3)
        if len(parts) < 3:
            continue
        pid, pg, stat = parts[0], parts[1], parts[2]
        if not (pid.isdigit() and pg.isdigit()):
            continue
        if int(pg) == pgid and not stat.startswith("Z"):
            name = os.path.basename(parts[3]) if len(parts) > 3 else ""
            procs.append((int(pid), name))
    return procs


def reap(st):
    """A pane whose command has run and exited is gone."""
    for pid in list(st["panes"]):
        p = st["panes"][pid]
        if p.get("pgid") and not group_procs(p["pgid"]):
            del st["panes"][pid]


def pane_or_fail(st, pane):
    if pane not in st["panes"]:
        fail("pane not found: " + pane)
    return st["panes"][pane]


def emit(result):
    print(json.dumps({"id": "fake", "result": result}))


def cmd_workspace_create(d, st, args):
    ap = Parser(prog="herdr workspace create")
    ap.add_argument("--cwd", default=None)
    ap.add_argument("--label", default="")
    ap.add_argument("--env", action="append", default=[])
    ap.add_argument("--focus", action="store_true")
    ap.add_argument("--no-focus", action="store_true")
    a = ap.parse_args(args)
    for e in a.env:
        if "=" not in e:
            fail("--env wants KEY=VALUE: " + e)
    st["next_ws"] += 1
    st["next_pane"] += 1
    ws = "w%d" % st["next_ws"]
    pane = "p%d" % st["next_pane"]
    cwd = a.cwd or os.getcwd()
    st["workspaces"][ws] = {"cwd": cwd, "label": a.label, "env": a.env}
    st["panes"][pane] = {
        "workspace": ws,
        "status": "unknown",
        "session": None,
        "pgid": 0,
    }
    emit(
        {
            "type": "workspace_created",
            "workspace": {"workspace_id": ws, "label": a.label},
            "root_pane": {"pane_id": pane, "workspace_id": ws},
        }
    )


def cmd_pane_run(d, st, args):
    if len(args) < 2:
        fail("usage: herdr pane run <PANE_ID> <COMMAND>...")
    pane, command = args[0], " ".join(args[1:])
    p = pane_or_fail(st, pane)
    ws = st["workspaces"][p["workspace"]]
    env = dict(os.environ)
    env.pop("FAKE_HERDR_FAIL", None)
    for e in ws["env"]:
        k, _, v = e.partition("=")
        env[k] = v
    env["HERDR_PANE_ID"] = pane
    log = open(os.path.join(d, pane + ".log"), "ab")
    proc = subprocess.Popen(
        ["sh", "-c", command],
        cwd=ws["cwd"],
        env=env,
        stdin=subprocess.DEVNULL,
        stdout=log,
        stderr=log,
        start_new_session=True,
    )
    log.close()
    p["pgid"] = proc.pid
    emit({"type": "pane_run", "pane_id": pane})


def cmd_pane_list(d, st, args):
    ap = Parser(prog="herdr pane list")
    ap.add_argument("--workspace", default=None)
    a = ap.parse_args(args)
    panes = []
    for pid, p in st["panes"].items():
        if a.workspace and p["workspace"] != a.workspace:
            continue
        panes.append(
            {
                "pane_id": pid,
                "workspace_id": p["workspace"],
                "agent_status": p["status"],
                "agent_session": {"value": p["session"]} if p["session"] else None,
            }
        )
    emit({"type": "pane_list", "panes": panes})


def cmd_pane_process_info(d, st, args):
    ap = Parser(prog="herdr pane process-info")
    ap.add_argument("--pane", default=None)
    ap.add_argument("--current", action="store_true")
    a = ap.parse_args(args)
    pane = a.pane or os.environ.get("HERDR_PANE_ID", "")
    p = pane_or_fail(st, pane)
    pgid = p.get("pgid", 0)
    procs = group_procs(pgid)
    shell = pgid if any(pid == pgid for pid, _ in procs) else 0
    emit(
        {
            "type": "pane_process_info",
            "process_info": {
                "foreground_process_group_id": pgid if procs else 0,
                "foreground_processes": [{"pid": pid, "name": n} for pid, n in procs],
                "shell_pid": shell,
            },
        }
    )


def cmd_pane_close(d, st, args):
    if len(args) != 1:
        fail("usage: herdr pane close <pane_id>")
    pane = args[0]
    p = pane_or_fail(st, pane)
    if p.get("pgid"):
        try:
            os.killpg(p["pgid"], signal.SIGKILL)
        except ProcessLookupError:
            pass
    del st["panes"][pane]
    emit({"type": "ok"})


def cmd_report_agent(d, st, args):
    ap = Parser(prog="herdr pane report-agent")
    ap.add_argument("pane")
    ap.add_argument("--source", required=True)
    ap.add_argument("--agent", required=True)
    ap.add_argument("--state", required=True)
    ap.add_argument("--message", default=None)
    ap.add_argument("--seq", default=None)
    ap.add_argument("--agent-session-id", default=None)
    ap.add_argument("--agent-session-path", default=None)
    a = ap.parse_args(args)
    p = pane_or_fail(st, a.pane)
    p["status"] = a.state
    if a.agent_session_id:
        p["session"] = a.agent_session_id
    emit({"type": "ok"})


def cmd_report_agent_session(d, st, args):
    ap = Parser(prog="herdr pane report-agent-session")
    ap.add_argument("pane")
    ap.add_argument("--source", required=True)
    ap.add_argument("--agent", required=True)
    ap.add_argument("--seq", default=None)
    ap.add_argument("--agent-session-id", default=None)
    ap.add_argument("--agent-session-path", default=None)
    ap.add_argument("--session-start-source", default=None)
    a = ap.parse_args(args)
    p = pane_or_fail(st, a.pane)
    if a.agent_session_id:
        p["session"] = a.agent_session_id
    emit({"type": "ok"})


def cmd_notification_show(d, st, args):
    ap = Parser(prog="herdr notification show")
    ap.add_argument("title")
    ap.add_argument("--body", default="")
    ap.add_argument("--position", default=None)
    ap.add_argument("--sound", default=None)
    a = ap.parse_args(args)
    clean = lambda s: s.replace("\t", " ").replace("\n", " ")
    with open(os.path.join(d, "notifications.log"), "a") as f:
        f.write(clean(a.title) + "\t" + clean(a.body) + "\n")
    emit({"type": "ok"})


HANDLERS = {
    ("workspace", "create"): cmd_workspace_create,
    ("pane", "run"): cmd_pane_run,
    ("pane", "list"): cmd_pane_list,
    ("pane", "process-info"): cmd_pane_process_info,
    ("pane", "close"): cmd_pane_close,
    ("pane", "report-agent"): cmd_report_agent,
    ("pane", "report-agent-session"): cmd_report_agent_session,
    ("notification", "show"): cmd_notification_show,
}


def main(argv):
    if len(argv) < 2 or tuple(argv[:2]) not in HANDLERS:
        fail("unsupported command: " + " ".join(argv[:2]))
    key = tuple(argv[:2])
    if os.environ.get("FAKE_HERDR_FAIL") == "-".join(key):
        fail("%s failed on request" % " ".join(key))
    d = state_dir()
    with open(os.path.join(d, "lock"), "w") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        # Each call, in the order the lock gives them, with the time it began: a script counts polls with it.
        with open(os.path.join(d, "calls.log"), "a") as calls:
            calls.write("%.6f\t%s\n" % (time.time(), " ".join(argv)))
        st = load(d)
        reap(st)
        HANDLERS[key](d, st, argv[2:])
        save(d, st)


if __name__ == "__main__":
    main(sys.argv[1:])
