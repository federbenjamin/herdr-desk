// Package runner starts armed tasks in herdr panes, watches them, and stops them.
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/herdr"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
)

// Herdr is what the runner needs from herdr. *herdr.Client satisfies it.
type Herdr interface {
	CreateWorkspace(ctx context.Context, cwd, label string, env []string) (herdr.Created, error)
	Run(ctx context.Context, pane, command string) error
	Panes(ctx context.Context) ([]herdr.Pane, error)
	Processes(ctx context.Context, pane string) (herdr.Processes, error)
	ClosePane(ctx context.Context, pane string) error
}

// The runner's states, as State returns them and daemon.json and the status method show them.
const (
	StateOff      = "off"       // runner.enabled is false
	StatePaused   = "paused"    // the pause file exists
	StateNoHerdr  = "no-herdr"  // no herdr binary on PATH
	StateNoRouter = "no-router" // [agent] router is empty or its first word is not an executable
	StateOn       = "on"
)

const (
	defaultRouterTimeout = 2 * time.Minute
	defaultKillGrace     = 2 * time.Second
	notifyTimeout        = 10 * time.Second
	gitTimeout           = 30 * time.Second
	maxQuoted            = 80
)

// Options configures a Runner.
type Options struct {
	Store         *store.Store
	Config        config.Config
	Paths         config.Paths
	Herdr         Herdr                            // nil → a *herdr.Client when herdr.Find succeeds, checked at each tick
	Exe           string                           // the desk binary a pane runs; "" → os.Executable()
	Now           func() time.Time                 // nil → time.Now
	OnState       func(state string)               // called from New, and from Tick or Pause when the state changed; calls never overlap and arrive in order
	Logf          func(format string, args ...any) // nil → log.Printf
	RouterTimeout time.Duration                    // 0 → 2 minutes
	KillGrace     time.Duration                    // 0 → 2 seconds between TERM and KILL
}

// Runner is one desk's runner.
type Runner struct {
	o Options

	stateMu   sync.Mutex // held while the state is computed and published, so OnState calls arrive in order
	published bool
	last      string

	idle map[int64]int // run id → ticks in a row its pane was done or idle; read and written by Tick only
}

// New returns a runner and publishes its state through OnState.
func New(o Options) *Runner {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logf == nil {
		o.Logf = log.Printf
	}
	if o.RouterTimeout <= 0 {
		o.RouterTimeout = defaultRouterTimeout
	}
	if o.KillGrace <= 0 {
		o.KillGrace = defaultKillGrace
	}
	r := &Runner{o: o, idle: map[int64]int{}}
	r.publish(context.Background())
	return r
}

// State returns the runner's state now.
func (r *Runner) State() string {
	s, _ := r.compute()
	return s
}

// Paused reports whether the pause file exists.
func (r *Runner) Paused() bool {
	_, err := os.Stat(r.o.Paths.RunnerPause())
	return err == nil
}

// Pause writes or removes the pause file. An actor with a session gets not-allowed.
func (r *Runner) Pause(ctx context.Context, a store.Actor, paused bool) error {
	if a.Session != "" {
		return &model.Refusal{Code: model.CodeNotAllowed, Msg: "an agent may not pause or resume the runner; a person does"}
	}
	if paused {
		if err := config.WriteFileAtomic(r.o.Paths.RunnerPause(), []byte("paused\n")); err != nil {
			return err
		}
	} else if err := os.Remove(r.o.Paths.RunnerPause()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	r.publish(ctx)
	return nil
}

// Tick is one poll: watch every live run, then start runs for armed tasks while the state and the caps allow.
func (r *Runner) Tick(ctx context.Context) {
	state, h := r.publish(ctx)
	r.failStaleRouting(ctx)
	if h != nil {
		r.watch(ctx, h)
	}
	if state == StateOn {
		r.start(ctx, h)
	}
}

// Loop calls Tick, then again every runner.poll_seconds, until ctx ends.
func (r *Runner) Loop(ctx context.Context) {
	poll := time.Duration(max(r.o.Config.Runner.PollSeconds, 1)) * time.Second
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		r.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// compute returns the state and the herdr to use, nil when none is found.
func (r *Runner) compute() (string, Herdr) {
	h := r.o.Herdr
	if h == nil {
		if bin, err := herdr.Find(); err == nil {
			h = &herdr.Client{Bin: bin}
		}
	}
	switch {
	case !r.o.Config.Runner.Enabled:
		return StateOff, h
	case r.Paused():
		return StatePaused, h
	case h == nil:
		return StateNoHerdr, h
	case !routerFound(r.o.Config.Agent.Router):
		return StateNoRouter, h
	}
	return StateOn, h
}

func routerFound(argv []string) bool {
	if len(argv) == 0 || argv[0] == "" {
		return false
	}
	_, err := exec.LookPath(argv[0])
	return err == nil
}

// publish computes the state, calls OnState when it changed (always on the first call), and notifies once each
// time the state becomes no-router.
func (r *Runner) publish(ctx context.Context) (string, Herdr) {
	r.stateMu.Lock()
	state, h := r.compute()
	changed := !r.published || state != r.last
	r.published, r.last = true, state
	if changed && r.o.OnState != nil {
		r.o.OnState(state)
	}
	r.stateMu.Unlock()
	if changed && state == StateNoRouter {
		r.notify(ctx, "desk: runner stopped", "no router: [agent] router is empty or its first word is not an executable")
	}
	return state, h
}

// failStaleRouting fails every run left in routing: Tick routes a run to the end within one call, so a routing
// run at the start of a tick was left by a daemon that stopped.
func (r *Runner) failStaleRouting(ctx context.Context) {
	live, err := r.o.Store.LiveRuns(ctx)
	if err != nil {
		r.logErr("read live runs", err)
		return
	}
	for _, run := range live {
		if run.State == model.RunRouting {
			r.fail(ctx, run, model.RunRouting, []string{model.TagRunner},
				"the daemon restarted while this run was being routed; arm the task again")
		}
	}
}

// start starts waiting runs whose root is free, then runs for armed tasks while the caps allow.
func (r *Runner) start(ctx context.Context, h Herdr) {
	live, err := r.o.Store.LiveRuns(ctx)
	if err != nil {
		r.logErr("read live runs", err)
		return
	}
	busy := map[string]bool{} // roots with a running in-place run
	for _, run := range live {
		if run.State == model.RunRunning && run.Isolation == "in-place" {
			busy[filepath.Clean(run.Root)] = true
		}
	}
	for _, run := range live {
		if run.State != model.RunWaiting || busy[filepath.Clean(run.Root)] {
			continue
		}
		d, err := r.o.Store.GetTask(ctx, run.Task)
		if err != nil {
			r.logErr("T%d: read the task", run.Task, err)
			continue
		}
		if r.spawn(ctx, h, d.Task, run, model.RunWaiting) {
			busy[filepath.Clean(run.Root)] = true
		}
	}

	now := r.o.Now()
	y, m, d := now.Date()
	today, err := r.o.Store.RunsSince(ctx, time.Date(y, m, d, 0, 0, 0, 0, now.Location()))
	if err != nil {
		r.logErr("count today's runs", err)
		return
	}
	armed, err := r.o.Store.Armed(ctx)
	if err != nil {
		r.logErr("read armed tasks", err)
		return
	}
	nlive := len(live)
	for _, t := range armed {
		if nlive >= r.o.Config.Runner.Cap || today >= r.o.Config.Runner.MaxRunsPerDay {
			return
		}
		run, err := r.o.Store.StartRun(ctx, t.Number)
		if errors.Is(err, store.ErrNotArmed) {
			continue
		}
		if err != nil {
			r.logErr("T%d: start a run", t.Number, err)
			continue
		}
		nlive++
		today++
		if !r.route(ctx, t, &run) {
			continue
		}
		if run.Isolation == "in-place" && busy[filepath.Clean(run.Root)] {
			if _, err := r.o.Store.UpdateRun(ctx, run.ID, model.RunRouting, store.RunUpdate{State: model.RunWaiting}); err != nil {
				r.logErr("T%d run %d: set waiting", t.Number, run.ID, err)
			}
			continue
		}
		if r.spawn(ctx, h, t, run, model.RunRouting) && run.Isolation == "in-place" {
			busy[filepath.Clean(run.Root)] = true
		}
	}
}

// fail sets the run failed while it is in state from, then notes msg on its task and sets the task blocked. A
// run that has left from (a kill took it) is left alone.
func (r *Runner) fail(ctx context.Context, run model.Run, from string, tags []string, msg string) {
	r.handBack(ctx, run, from, model.RunFailed, model.StatusBlocked, tags, msg)
}

// handBack claims the run by moving it from one state to the next, then writes the note and the task's status as
// the runner, with the run's id. It reports whether it claimed the run.
func (r *Runner) handBack(ctx context.Context, run model.Run, from, to string, status model.Status, tags []string, msg string) bool {
	ok, err := r.o.Store.UpdateRun(ctx, run.ID, from, store.RunUpdate{State: to})
	if err != nil || !ok {
		if err != nil {
			r.logErr("T%d run %d: set %s", run.Task, run.ID, to, err)
		}
		return false
	}
	actor := store.Actor{Run: run.ID}
	r.note(ctx, actor, run.Task, tags, msg)
	if _, err := r.o.Store.SetTask(ctx, actor, run.Task, model.Patch{Status: &status}); err != nil {
		r.logErr("T%d: set %s", run.Task, status, err)
	}
	return true
}

func (r *Runner) note(ctx context.Context, a store.Actor, task int, tags []string, text string) {
	if _, err := r.o.Store.Note(ctx, a, store.NoteInput{NoteData: model.NoteData{Text: text}, Task: task, Tags: tags}); err != nil {
		r.logErr("T%d: write a note", task, err)
	}
}

// logErr logs format with args, then ": " and err, the last argument.
func (r *Runner) logErr(format string, args ...any) {
	r.o.Logf("desk runner: "+format+": %v", args...)
}

// notify runs [notify] command with title and body. A failure is logged and nothing else.
func (r *Runner) notify(ctx context.Context, title, body string) {
	if len(r.o.Config.Notify.Command) == 0 {
		return
	}
	argv := config.Expand(r.o.Config.Notify.Command, map[string]string{"title": title, "body": body})
	if _, err := runChild(ctx, argv, nil, notifyTimeout, nil); err != nil {
		r.logErr("notify", err)
	}
}

// runChild runs argv with stdin and the extra environment within timeout, in its own process group so a timeout
// stops what it started too, and returns its stdout. An error quotes at most 80 characters of its stderr.
func runChild(ctx context.Context, argv []string, stdin []byte, timeout time.Duration, env []string) ([]byte, error) {
	if len(argv) == 0 || argv[0] == "" {
		return nil, errors.New("the command is empty")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.Stdin = bytes.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("timed out after %s", timeout)
		}
		if msg := strings.TrimSpace(errOut.String()); msg != "" {
			return nil, fmt.Errorf("%s: %w: %s", filepath.Base(argv[0]), err, clip(msg))
		}
		return nil, fmt.Errorf("%s: %w", filepath.Base(argv[0]), err)
	}
	return out.Bytes(), nil
}

// clip cuts s to 80 characters, the most of a child's output a note may quote.
func clip(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= maxQuoted {
		return s
	}
	return string([]rune(s)[:maxQuoted]) + "…"
}
