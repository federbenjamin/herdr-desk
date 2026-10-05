// Package runner starts runs of tasks in herdr panes, hands them back, and does the ticker's run jobs. It keeps no
// state of its own between calls: every process that opens the store makes its own Runner.
package runner

import (
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
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/herdr"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/sidebar"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// Herdr is what the runner needs from herdr. *herdr.Client satisfies it.
type Herdr interface {
	CreateWorkspace(ctx context.Context, cwd, label string, env []string) (herdr.Created, error)
	Run(ctx context.Context, pane, command string) error
	Panes(ctx context.Context) ([]herdr.Pane, error)
	Pane(ctx context.Context, id string) (herdr.Pane, bool, error)
	Processes(ctx context.Context, pane string) (herdr.Processes, error)
	ClosePane(ctx context.Context, pane string) error
	FocusPane(ctx context.Context, workspace, pane string) error
	ReportToken(ctx context.Context, pane, source, name, value string) error
}

// The runner's states, as State returns them and the status method shows them.
const (
	StateOff     = "off"      // runner.enabled is false
	StatePaused  = "paused"   // the pause file exists
	StateNoHerdr = "no-herdr" // no usable herdr binary: DESK_HERDR names none, or, unset, none is on PATH
	StateOn      = "on"
)

const (
	defaultKillGrace = 2 * time.Second
	notifyTimeout    = 10 * time.Second
	gitTimeout       = 30 * time.Second
	maxQuoted        = 80
	// staleAfter is how long a run may stay starting before Jobs fails it, and how long after a run ended Jobs
	// waits before it blocks a task the run left started: a hand-back between its claim and its writes is not one.
	staleAfter = time.Minute
)

// Options configures a Runner.
type Options struct {
	Store     *store.Store
	Config    config.Config
	Paths     config.Paths
	Herdr     Herdr                            // nil → a *herdr.Client when herdr.Find succeeds, looked up at each call
	Exe       string                           // the herdr-desk binary a pane runs; "" → os.Executable()
	Now       func() time.Time                 // nil → time.Now
	Logf      func(format string, args ...any) // nil → log.Printf
	KillGrace time.Duration                    // 0 → 2 seconds between TERM and KILL
}

// Runner is one desk's runner.
type Runner struct {
	o     Options
	owned bool // Open opened the store, so Close closes it
}

// New returns a runner over o.Store.
func New(o Options) *Runner {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logf == nil {
		o.Logf = log.Printf
	}
	if o.KillGrace <= 0 {
		o.KillGrace = defaultKillGrace
	}
	return &Runner{o: o}
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
		return config.WriteFileAtomic(r.o.Paths.RunnerPause(), []byte("paused\n"))
	}
	if err := os.Remove(r.o.Paths.RunnerPause()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
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

// Jobs is the ticker's run jobs, once: reconcile, the deadline, start what waits, fail runs left starting, close
// the panes left open again, and block tasks left started. With no live run and no pane left open, herdr is not asked.
func (r *Runner) Jobs(ctx context.Context) {
	_ = r.Reconcile(ctx) // Reconcile logs its errors.
	r.deadline(ctx)
	r.StartWaiting(ctx)
	r.failStaleStarting(ctx)
	r.closeLeftOpen(ctx)
	r.repairStarted(ctx)
}

// deadline stops every running run past runner.max_run_minutes. It asks herdr for its panes only when one is.
func (r *Runner) deadline(ctx context.Context) {
	live, err := r.o.Store.LiveRuns(ctx)
	if err != nil {
		r.logErr("read live runs", err)
		return
	}
	limit := time.Duration(r.o.Config.Runner.MaxRunMinutes) * time.Minute
	var over []model.Run
	for _, run := range live {
		if run.State == model.RunRunning && r.o.Now().Sub(run.StartedTS) > limit {
			over = append(over, run)
		}
	}
	if len(over) == 0 {
		return
	}
	h, panes, ok := r.panes(ctx)
	if !ok {
		return
	}
	for _, run := range over {
		r.stop(ctx, h, run, panes, fmt.Sprintf("stopped after %d minutes (runner.max_run_minutes)", r.o.Config.Runner.MaxRunMinutes))
	}
}

// panes finds herdr and lists its panes; false, logged, when either fails.
func (r *Runner) panes(ctx context.Context) (Herdr, []herdr.Pane, bool) {
	h, err := r.findHerdr()
	if err != nil {
		r.logErr("find herdr", err)
		return nil, nil, false
	}
	panes, err := h.Panes(ctx)
	if err != nil {
		r.logErr("list herdr panes", err)
		return nil, nil, false
	}
	return h, panes, true
}

// failStaleStarting fails every run left starting for over a minute: a start spawns its run within one call, so
// such a run was left by a process that stopped.
func (r *Runner) failStaleStarting(ctx context.Context) {
	live, err := r.o.Store.LiveRuns(ctx)
	if err != nil {
		r.logErr("read live runs", err)
		return
	}
	for _, run := range live {
		if run.State == model.RunStarting && r.o.Now().Sub(run.StartedTS) > staleAfter {
			r.fail(ctx, run, model.RunStarting, fmt.Sprintf("run %d was left starting for over a minute; start the task again", run.ID))
		}
	}
}

// closeLeftOpen closes again each pane a kill or a spawn left open, and clears the mark once herdr no longer lists
// the pane.
func (r *Runner) closeLeftOpen(ctx context.Context) {
	runs, err := r.o.Store.LeftOpenRuns(ctx)
	if err != nil {
		r.logErr("read the runs whose pane was left open", err)
		return
	}
	if len(runs) == 0 {
		return
	}
	h, panes, ok := r.panes(ctx)
	if !ok {
		return
	}
	shut := false
	for _, run := range runs {
		pane := herdr.Pane{ID: run.Pane, Workspace: run.Workspace}
		if listed(panes, pane) {
			k := r.closePane(ctx, h, run, pane)
			r.o.Logf("herdr-desk runner: T%d: closing pane %s again: %s", run.Task, pane.ID, k)
			if k.done == "" {
				continue
			}
		}
		if _, err := r.o.Store.UpdateRun(ctx, run.ID, run.State, store.RunUpdate{LeftOpen: &shut}); err != nil {
			r.logErr("T%d run %d: clear its open pane", run.Task, run.ID, err)
		}
	}
}

// repairStarted blocks each task left started by its newest run more than a minute after that run ended: a
// hand-back that claimed the run but could not write the task's status. A task a person set started is left alone.
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
		if !ok || model.RunLive(run.State) || r.o.Now().Sub(run.EndedTS) <= staleAfter {
			continue
		}
		d, err := r.o.Store.GetTask(ctx, t.Number)
		if err != nil {
			r.logErr("T%d: read the task", t.Number, err)
			continue
		}
		if startedBy(d.History, run.ID) {
			r.handBack(ctx, run, store.HandBack{From: run.State, Status: model.StatusBlocked, Tags: []string{model.TagRunner},
				Note: fmt.Sprintf("run %d is %s, but its task was left started", run.ID, run.State)}, nil)
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

// fail sets the run failed while it is in state from, then notes msg on its task and sets the task blocked. A
// run that has left from (a kill took it) is left alone.
func (r *Runner) fail(ctx context.Context, run model.Run, from, msg string) {
	r.handBack(ctx, run, store.HandBack{From: from, To: model.RunFailed, Status: model.StatusBlocked,
		Tags: []string{model.TagRunner}, Note: msg}, nil)
}

// handBack is the one hand-back, over Store.HandBack. Without a cleanup it is one HandBack. With one, it claims the
// run (HandBack{From, To}), runs the cleanup, whose answer is the note, then writes the note and the status from
// To; once claimed, those writes go on even when ctx ends, so the task is not left started with no live run. A run
// that is not its task's newest (stale-run) is nothing to do. A run that ends frees a slot, so StartWaiting runs.
// A claimed hand-back is then reported: the run's row while the run stays live (a run that ends here had its pane
// closed or found gone), and the coordinator's row. It returns the task and whether it claimed the run.
func (r *Runner) handBack(ctx context.Context, run model.Run, hb store.HandBack, cleanup func(ctx context.Context) string) (model.Task, bool, error) {
	write := hb
	if cleanup != nil {
		_, claimed, err := r.storeHandBack(ctx, run, store.HandBack{From: hb.From, To: hb.To})
		if err != nil || !claimed {
			return model.Task{}, false, err
		}
		ctx = context.WithoutCancel(ctx)
		write.Note = cleanup(ctx)
		if hb.To != "" {
			write.From, write.To = hb.To, ""
		}
	}
	t, claimed, err := r.storeHandBack(ctx, run, write)
	if err != nil {
		return t, cleanup != nil, err
	}
	if !claimed && cleanup == nil {
		return t, false, nil
	}
	if runEnded(hb.To) {
		r.StartWaiting(ctx)
	}
	state := hb.To
	if state == "" {
		state = hb.From
	}
	r.report(ctx, run, model.RunLive(state))
	return t, true, nil
}

// report shows the sidebar rows a change of the run can move: the run's own when row is set, then the
// coordinator's. With no herdr there is no sidebar, and nothing is asked; a report herdr refuses is logged.
func (r *Runner) report(ctx context.Context, run model.Run, row bool) {
	h, err := r.findHerdr()
	if err != nil {
		return
	}
	if row {
		r.reportRun(ctx, h, run)
	}
	r.reportCoordinator(ctx, h)
}

// reportRun shows the run's row on its pane, from the run and its task as the store holds them now. A run with no
// pane yet, or killed or failed (its pane closed or closing), gets nothing; for a run that ended, herdr is asked
// first whether its pane is still open.
func (r *Runner) reportRun(ctx context.Context, h Herdr, run model.Run) {
	cur, ok, err := r.o.Store.CurrentRun(ctx, run.Task)
	if err != nil {
		r.logErr("T%d: read its run for the sidebar", run.Task, err)
		return
	}
	current := ok && cur.ID == run.ID
	if current {
		run = cur
	}
	if run.Pane == "" || run.State == model.RunKilled || run.State == model.RunFailed {
		return
	}
	if !model.RunLive(run.State) {
		p, found, err := h.Pane(ctx, run.Pane)
		if err != nil || !found || p.Workspace != run.Workspace {
			return
		}
	}
	d, err := r.o.Store.GetTask(ctx, run.Task)
	if err != nil {
		r.logErr("T%d: read the task for the sidebar", run.Task, err)
		return
	}
	r.reportToken(ctx, h, run.Pane, sidebar.RunText(d.Task, run, current, handBackRef(d.History, run.ID), r.o.Now().Location()))
}

// reportCoordinator shows the desk's counts on the recorded coordinator's pane, when there is one.
func (r *Runner) reportCoordinator(ctx context.Context, h Herdr) {
	c, ok, err := r.o.Store.Coordinator(ctx)
	if err != nil {
		r.logErr("read the coordinator for the sidebar", err)
		return
	}
	if !ok || c.Pane == "" {
		return
	}
	need, err := r.o.Store.ListTasks(ctx, store.Filter{Statuses: []model.Status{model.StatusBlocked, model.StatusReview}})
	if err != nil {
		r.logErr("count the tasks that need a person", err)
		return
	}
	live, err := r.o.Store.LiveRuns(ctx)
	if err != nil {
		r.logErr("read live runs", err)
		return
	}
	running, waiting := 0, 0
	for _, run := range live {
		switch run.State {
		case model.RunStarting, model.RunRunning:
			running++
		case model.RunWaiting:
			waiting++
		}
	}
	r.reportToken(ctx, h, c.Pane, sidebar.CoordinatorText(len(need), running, waiting))
}

func (r *Runner) reportToken(ctx context.Context, h Herdr, pane, text string) {
	if err := h.ReportToken(ctx, pane, sidebar.Source, sidebar.Token, text); err != nil {
		r.logErr("show the sidebar row of pane %s", pane, err)
	}
}

// handBackRef is the ref of the newest status write the run's worker made with one, "" when none did.
func handBackRef(history []model.Event, run int64) string {
	for i := len(history) - 1; i >= 0; i-- {
		e := history[i]
		var p model.Patch
		if e.Kind != model.KindSet || e.Run != run || json.Unmarshal(e.Data, &p) != nil {
			continue
		}
		if p.Ref != "" {
			return p.Ref
		}
	}
	return ""
}

// storeHandBack is Store.HandBack with a stale-run refusal read as no claim; any other error is logged.
func (r *Runner) storeHandBack(ctx context.Context, run model.Run, hb store.HandBack) (model.Task, bool, error) {
	t, claimed, err := r.o.Store.HandBack(ctx, run, hb)
	if ref, ok := model.AsRefusal(err); ok && ref.Code == model.CodeStaleRun {
		return model.Task{}, false, nil
	}
	if err != nil {
		r.logErr("T%d run %d: hand back (%s → %s, task %s)", run.Task, run.ID, hb.From, hb.To, hb.Status, err)
	}
	return t, claimed, err
}

// runEnded reports whether state is one a run never leaves.
func runEnded(state string) bool {
	return state == model.RunEnded || state == model.RunFailed || state == model.RunKilled
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
	if err := runChild(ctx, argv, notifyTimeout); err != nil {
		r.logErr("notify", err)
	}
}

// runChild runs argv within timeout, in its own process group so a timeout stops what it started too. An error
// quotes at most 80 characters of its stderr.
func runChild(ctx context.Context, argv []string, timeout time.Duration) error {
	if len(argv) == 0 || argv[0] == "" {
		return errors.New("the command is empty")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var errOut strings.Builder
	cmd.Stderr = &errOut
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("timed out after %s", timeout)
		}
		if msg := strings.TrimSpace(errOut.String()); msg != "" {
			return fmt.Errorf("%s: %w: %s", filepath.Base(argv[0]), err, clip(msg))
		}
		return fmt.Errorf("%s: %w", filepath.Base(argv[0]), err)
	}
	return nil
}

// clip cuts s to 80 characters, the most of a child's output a note may quote.
func clip(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= maxQuoted {
		return s
	}
	return string([]rune(s)[:maxQuoted]) + "…"
}
