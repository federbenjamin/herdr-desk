// Package runner starts armed tasks in herdr panes, watches them, and stops them.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
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

	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/herdr"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
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
	StateNoHerdr  = "no-herdr"  // no usable herdr binary: DESK_HERDR names none, or, unset, none is on PATH
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
	Exe           string                           // the herdr-desk binary a pane runs; "" → os.Executable()
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

	openMu sync.Mutex
	open   map[string]openPane // pane id → a pane that did not close; the watch closes it again

	handMu  sync.Mutex
	handing map[int64]int // run id → hand-backs of it in flight, from before their claim until their writes end
}

// openPane is a pane the runner failed to close, and the task it was opened for.
type openPane struct {
	pane herdr.Pane
	task int
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
	r := &Runner{o: o, idle: map[int64]int{}, open: map[string]openPane{}, handing: map[int64]int{}}
	r.publish(context.Background())
	return r
}

// State returns the runner's state now.
func (r *Runner) State() string {
	s, _, _ := r.compute()
	return s
}

// Paused reports whether the pause file exists.
func (r *Runner) Paused() bool {
	paused, _ := r.paused()
	return paused
}

// paused reports whether the pause file exists. A file that cannot be looked at reads as paused, with the reason:
// the pause is a person's brake, so it fails closed.
func (r *Runner) paused() (bool, error) {
	_, err := os.Stat(r.o.Paths.RunnerPause())
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	}
	return true, fmt.Errorf("the pause file cannot be read, so the runner stays paused: %w", err)
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
	r.repairStarted(ctx)
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

// compute returns the state, the herdr to use (nil when none is found), and why the state is paused or no-herdr
// when something went wrong there: herdr.Find's error, or a pause file that cannot be read.
func (r *Runner) compute() (string, Herdr, error) {
	h, herdrErr := r.findHerdr()
	paused, pauseErr := r.paused()
	switch {
	case !r.o.Config.Runner.Enabled:
		return StateOff, h, nil
	case paused:
		return StatePaused, h, pauseErr
	case h == nil:
		return StateNoHerdr, h, herdrErr
	case !routerFound(r.o.Config.Agent.Router):
		return StateNoRouter, h, nil
	}
	return StateOn, h, nil
}

// findHerdr returns the herdr to use, or nil and herdr.Find's error when none is found.
func (r *Runner) findHerdr() (Herdr, error) {
	if r.o.Herdr != nil {
		return r.o.Herdr, nil
	}
	bin, err := herdr.Find()
	if err != nil {
		return nil, err
	}
	return &herdr.Client{Bin: bin}, nil
}

func routerFound(argv []string) bool {
	if len(argv) == 0 || argv[0] == "" {
		return false
	}
	_, err := exec.LookPath(argv[0])
	return err == nil
}

// publish computes the state, calls OnState when it changed (always on the first call), logs why when the new
// state has a reason, and notifies once each time the state becomes no-router.
func (r *Runner) publish(ctx context.Context) (string, Herdr) {
	r.stateMu.Lock()
	state, h, why := r.compute()
	changed := !r.published || state != r.last
	r.published, r.last = true, state
	if changed && r.o.OnState != nil {
		r.o.OnState(state)
	}
	if changed && why != nil {
		r.logErr("runner %s", state, why)
	}
	r.stateMu.Unlock()
	if changed && state == StateNoRouter {
		r.notify(ctx, "herdr-desk: runner stopped", "no router: [agent] router is empty or its first word is not an executable")
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

// repairStarted blocks each task left started by its newest run after that run ended: a hand-back that claimed the
// run but could not write the task's status. A task a person set started is left alone.
func (r *Runner) repairStarted(ctx context.Context) {
	tasks, err := r.o.Store.ListTasks(ctx, store.Filter{Statuses: []model.Status{model.StatusStarted}})
	if err != nil {
		r.logErr("read started tasks", err)
		return
	}
	for _, t := range tasks {
		run, ok, err := r.o.Store.CurrentRun(ctx, t.Number)
		if err != nil {
			r.logErr("T%d: read its run", t.Number, err)
			continue
		}
		// A hand-back marks its run before it claims it, so a run seen ended and unmarked here has no writes still
		// to come, and the task read after this check holds them.
		if !ok || model.RunLive(run.State) || r.handingBack(run.ID) {
			continue
		}
		d, err := r.o.Store.GetTask(ctx, t.Number)
		if err != nil {
			r.logErr("T%d: read the task", t.Number, err)
			continue
		}
		if startedBy(d.History, run.ID) {
			r.handBack(ctx, run, flip{from: run.State, status: model.StatusBlocked, tags: []string{model.TagRunner},
				note: fmt.Sprintf("run %d is %s, but its task was left started", run.ID, run.State)})
		}
	}
}

// startedBy reports whether the newest status write in history is the one that started the run.
func startedBy(history []model.Event, run int64) bool {
	for i := len(history) - 1; i >= 0; i-- {
		e := history[i]
		var p model.Patch
		if (e.Kind != model.KindSet && e.Kind != model.KindTask) || json.Unmarshal(e.Data, &p) != nil || p.Status == nil {
			continue
		}
		return e.Run == run && *p.Status == model.StatusStarted
	}
	return false
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
	r.handBack(ctx, run, flip{from: from, to: model.RunFailed, status: model.StatusBlocked, tags: tags, note: msg})
}

// flip is one hand-back: the run leaves from for to, and its task gets status and a runner note.
type flip struct {
	from, to string // to "" leaves the run in from: the claim only checks it is still there
	status   model.Status
	tags     []string
	note     string
	// cleanup runs once the run is claimed and returns the note, in place of note.
	cleanup func(ctx context.Context) string
}

// handBack claims the run by moving it from f.from to f.to, runs f.cleanup, then writes the note and the task's
// status as the runner, with the run's id. Once the run is claimed the writes go on even when ctx ends, so the
// task is not left started with no live run. It returns the task and whether it claimed the run.
func (r *Runner) handBack(ctx context.Context, run model.Run, f flip) (model.Task, bool, error) {
	r.markHandBack(run.ID, 1)
	defer r.markHandBack(run.ID, -1)
	ok, err := r.o.Store.UpdateRun(ctx, run.ID, f.from, store.RunUpdate{State: f.to})
	if err != nil {
		r.logErr("T%d run %d: set %s", run.Task, run.ID, f.to, err)
		return model.Task{}, false, err
	}
	if !ok {
		return model.Task{}, false, nil
	}
	ctx = context.WithoutCancel(ctx)
	msg := f.note
	if f.cleanup != nil {
		msg = f.cleanup(ctx)
	}
	actor := store.Actor{Run: run.ID}
	r.note(ctx, actor, run.Task, f.tags, msg)
	t, err := r.o.Store.SetTask(ctx, actor, run.Task, model.Patch{Status: &f.status})
	if err != nil {
		r.logErr("T%d: set %s", run.Task, f.status, err)
	}
	return t, true, err
}

// markHandBack adds n to the run's count of hand-backs in flight.
func (r *Runner) markHandBack(run int64, n int) {
	r.handMu.Lock()
	defer r.handMu.Unlock()
	if r.handing[run] += n; r.handing[run] <= 0 {
		delete(r.handing, run)
	}
}

// handingBack reports whether a hand-back of the run is in flight.
func (r *Runner) handingBack(run int64) bool {
	r.handMu.Lock()
	defer r.handMu.Unlock()
	return r.handing[run] > 0
}

func (r *Runner) note(ctx context.Context, a store.Actor, task int, tags []string, text string) {
	if _, err := r.o.Store.Note(ctx, a, store.NoteInput{NoteData: model.NoteData{Text: text}, Task: task, Tags: tags}); err != nil {
		r.logErr("T%d: write a note", task, err)
	}
}

// logErr logs format with args, then ": " and err, the last argument.
func (r *Runner) logErr(format string, args ...any) {
	r.o.Logf("herdr-desk runner: "+format+": %v", args...)
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
