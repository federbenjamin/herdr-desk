// Package board is herdr-desk's terminal board and capture popup. State and CaptureState hold every rule and do no
// I/O; Run and Capture put them in a bubbletea program and run the effects they ask for against the home.
package board

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// Home is what the board asks of the home. *api.Client satisfies it.
type Home interface {
	ListTasks(ctx context.Context, f store.Filter) (api.TaskList, error)
	GetTask(ctx context.Context, number int) (store.TaskDetail, error)
	AddTask(ctx context.Context, a store.Actor, in store.AddTaskInput) (model.Task, error)
	SetTask(ctx context.Context, a store.Actor, number int, p model.Patch) (model.Task, error)
	Step(ctx context.Context, a store.Actor, number int, op model.StepOp) (model.Task, bool, error)
	Append(ctx context.Context, r api.AppendRequest) (ev model.Event, queued bool, err error)
	ListRuns(ctx context.Context) ([]model.Run, error)
	Status(ctx context.Context) (api.Status, error)
	KillRun(ctx context.Context, a store.Actor, task int) (model.Task, error)
	PauseRunner(ctx context.Context, a store.Actor, paused bool) (api.Status, error)
	StartRun(ctx context.Context, a store.Actor, task int, route store.RunRoute) (model.Run, error)
	// Retry forgets an unreachable home, so a refresh after the home came back reaches it.
	Retry()
}

var _ Home = (*api.Client)(nil)

// Options is what Run and Capture need.
type Options struct {
	Home    Home
	IsHome  bool      // false on a client
	Herdr   string    // the herdr binary; "" → no herdr: the file viewer is off
	In      io.Reader // the terminal
	Out     io.Writer
	Refresh time.Duration // 0 → 3 seconds
	// Exec runs one argv and returns its combined output. nil → os/exec with a 10 second timeout.
	Exec func(ctx context.Context, argv []string) ([]byte, error)
}

// Run shows the board on the alternate screen until the user quits or ctx ends. Its writes are the user's.
func Run(ctx context.Context, o Options) error {
	every := o.Refresh
	if every <= 0 {
		every = 3 * time.Second
	}
	m := runModel{
		ctx:   ctx,
		s:     NewState(Config{IsHome: o.IsHome}),
		x:     newExecutor(o),
		every: every,
	}
	_, err := newProgram(ctx, m, o).Run()
	return ended(ctx, err)
}

// Capture shows the popup until one task lands or the user cancels. It returns the task, or nil on a cancel.
func Capture(ctx context.Context, o Options) (*model.Task, error) {
	m := captureModel{ctx: ctx, c: NewCapture("capture: "), x: newExecutor(o)}
	final, err := newProgram(ctx, m, o).Run()
	if err = ended(ctx, err); err != nil {
		return nil, err
	}
	if cm, ok := final.(captureModel); ok {
		return cm.task, nil
	}
	return nil, nil
}

func newProgram(ctx context.Context, m tea.Model, o Options) *tea.Program {
	// The size counts only when Out is not a terminal, which reports none: NewState's size, so a frame is drawn.
	opts := []tea.ProgramOption{tea.WithContext(ctx), tea.WithWindowSize(80, 24)}
	if o.In != nil {
		opts = append(opts, tea.WithInput(o.In))
	}
	if o.Out != nil {
		opts = append(opts, tea.WithOutput(o.Out))
	}
	return tea.NewProgram(m, opts...)
}

// ended maps the program's end to Run's: the caller's ctx ending is not an error.
func ended(ctx context.Context, err error) error {
	if err != nil && ctx.Err() != nil && errors.Is(err, tea.ErrProgramKilled) {
		return nil
	}
	return err
}

type timerMsg struct{}

type runModel struct {
	ctx   context.Context
	s     State
	x     *executor
	every time.Duration
}

func (m runModel) timer() tea.Cmd {
	return tea.Tick(m.every, func(time.Time) tea.Msg { return timerMsg{} })
}

func (m runModel) Init() tea.Cmd {
	return tea.Batch(func() tea.Msg { return Tick{} }, m.timer())
}

func (m runModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var next tea.Cmd
	if _, ok := msg.(timerMsg); ok {
		msg, next = Tick{}, m.timer()
	}
	var eff []Effect
	m.s, eff = feed(m.s, msg)
	return m, tea.Batch(m.x.cmds(m.ctx, eff), next)
}

// wrote is the executor's answer to a write that succeeded, with the write.
type wrote struct{ effect Effect }

// staleNotes is a notes save's stale refusal with the notes the home held when it came back, or, when they could
// not be read, why (readErr). Its text and its refusal are the refusal's.
type staleNotes struct {
	err     error
	notes   string
	readErr error
}

func (e staleNotes) Error() string { return e.err.Error() }
func (e staleNotes) Unwrap() error { return e.err }

// feed applies one message of Run's program to s. A write that succeeded reaches s as a Tick, once s has dropped
// the notes save it carried, which can no longer fail.
func feed(s State, msg tea.Msg) (State, []Effect) {
	if w, ok := msg.(wrote); ok {
		s, msg = s.wrote(w.effect), Tick{}
	}
	return s.Update(msg)
}

func (m runModel) View() tea.View {
	v := tea.NewView(m.s.Render())
	v.AltScreen = true
	return v
}

type captureModel struct {
	ctx  context.Context
	c    CaptureState
	x    *executor
	task *model.Task
}

func (m captureModel) Init() tea.Cmd { return nil }

func (m captureModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if a, ok := msg.(Added); ok {
		t := a.Task
		m.task = &t
	}
	var eff []Effect
	m.c, eff = m.c.Update(msg)
	return m, m.x.cmds(m.ctx, eff)
}

func (m captureModel) View() tea.View { return tea.NewView(m.c.Render()) }

type executor struct {
	home  Home
	herdr string
	exec  func(ctx context.Context, argv []string) ([]byte, error)

	viewerMu    sync.Mutex
	viewerKnown bool // herdr gave a clean answer, which viewer holds
	viewer      bool
}

func newExecutor(o Options) *executor {
	x := &executor{home: o.Home, herdr: o.Herdr, exec: o.Exec}
	if x.exec == nil {
		x.exec = runArgv
	}
	return x
}

func runArgv(ctx context.Context, argv []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
}

func (x *executor) cmds(ctx context.Context, eff []Effect) tea.Cmd {
	var cmds []tea.Cmd
	for _, e := range eff {
		if _, quit := e.(Quit); quit {
			cmds = append(cmds, tea.Quit)
			continue
		}
		cmds = append(cmds, func() tea.Msg { return x.answer(ctx, e) })
	}
	return tea.Batch(cmds...)
}

// answer runs one effect. A failure's error names the effect, so State can tell which outstanding write or
// refresh it answers, and a write that succeeded is a wrote that names it.
func (x *executor) answer(ctx context.Context, e Effect) tea.Msg {
	msg := x.run(ctx, e)
	switch m := msg.(type) {
	case Failed:
		var named effectErr
		if !errors.As(m.Err, &named) {
			m.Err = effectErr{effect: e, err: m.Err}
		}
		return m
	case Tick:
		return wrote{effect: e}
	}
	return msg
}

var user = store.Actor{}

func afterWrite(err error) tea.Msg {
	if err != nil {
		return Failed{Err: err}
	}
	return Tick{}
}

func (x *executor) run(ctx context.Context, e Effect) tea.Msg {
	switch e := e.(type) {
	case Refresh:
		return x.refresh(ctx, e.Done)
	case LoadTask:
		d, err := x.home.GetTask(ctx, e.Task)
		if err != nil {
			return Failed{Err: err}
		}
		return TaskLoaded{Detail: d}
	case AddTask:
		t, err := x.home.AddTask(ctx, user, store.AddTaskInput{TaskData: e.Data})
		if err != nil {
			return Failed{Err: err}
		}
		return Added{Task: t}
	case SetTask:
		_, err := x.home.SetTask(ctx, user, e.Task, e.Patch)
		if r, ok := model.AsRefusal(err); ok && r.Code == model.CodeStale && e.Patch.NotesWere != nil {
			// The editor reopens on the home's notes, read now, so the next ctrl+s replaces them with no refresh
			// between; a read that fails goes with the refusal, so the editor does not promise that.
			d, gerr := x.home.GetTask(ctx, e.Task)
			err = staleNotes{err: err, notes: d.Task.Notes, readErr: gerr}
		}
		return afterWrite(err)
	case StepTask:
		_, _, err := x.home.Step(ctx, user, e.Task, e.Op)
		return afterWrite(err)
	case KillRun:
		_, err := x.home.KillRun(ctx, user, e.Task)
		return afterWrite(err)
	case StartRun:
		_, err := x.home.StartRun(ctx, user, e.Task, store.RunRoute{})
		return afterWrite(err)
	case PauseRunner:
		_, err := x.home.PauseRunner(ctx, user, e.Paused)
		return afterWrite(err)
	case Rearm:
		return x.rearm(ctx, e)
	case OpenRef:
		if err := x.open(ctx, e); err != nil {
			return Failed{Err: err}
		}
		return nil
	}
	return nil
}

func (x *executor) do(ctx context.Context, argv []string) error {
	out, err := x.exec(ctx, argv)
	if err != nil {
		return failure(argv, err, out)
	}
	return nil
}

// failure names the command that failed by its first three words, with its error and output.
func failure(argv []string, err error, out []byte) error {
	return fmt.Errorf("%s: %v: %s", strings.Join(argv[:min(len(argv), 3)], " "), err, oneLine(string(out)))
}

func (x *executor) rearm(ctx context.Context, e Rearm) tea.Msg {
	if e.Answer != "" {
		_, queued, err := x.home.Append(ctx, api.AppendRequest{
			Actor: user,
			Kind:  model.KindNote,
			Note:  &store.NoteInput{NoteData: model.NoteData{Text: e.Answer}, Task: e.Task},
		})
		if err != nil {
			return Failed{Err: err}
		}
		if queued {
			return Failed{Err: effectErr{effect: Rearm{Task: e.Task}, err: fmt.Errorf("the home did not answer: the answer is queued as a note, and %s is not set ready", taskID(e.Task))}}
		}
	}
	// From here the answer is stored, so a failure names a Rearm with no answer to give back.
	ready := model.StatusReady
	if _, err := x.home.SetTask(ctx, user, e.Task, model.Patch{Status: &ready}); err != nil {
		return Failed{Err: effectErr{effect: Rearm{Task: e.Task}, err: err}}
	}
	return Tick{}
}

func (x *executor) refresh(ctx context.Context, done bool) tea.Msg {
	// Each refresh tries the home afresh: a board left open while the home was away recovers once it answers.
	x.home.Retry()
	tl, err := x.home.ListTasks(ctx, store.Filter{})
	if err != nil {
		return Failed{Err: err}
	}
	d := Data{Tasks: tl.Tasks}
	if tl.Offline {
		d.Offline, d.SnapshotTS = true, tl.SnapshotTS
		return Loaded{Data: d}
	}
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	fail := func(err error) {
		mu.Lock()
		errs = append(errs, err)
		mu.Unlock()
	}
	wg.Go(func() {
		st, err := x.home.Status(ctx)
		if err != nil {
			fail(err)
		}
		d.Status = st
	})
	wg.Go(func() {
		runs, err := x.home.ListRuns(ctx)
		if err != nil {
			fail(err)
		}
		d.Runs = runs
	})
	if done {
		wg.Go(func() {
			dl, err := x.home.ListTasks(ctx, store.Filter{Statuses: []model.Status{model.StatusDone}})
			if err != nil {
				fail(err)
			}
			d.Done = dl.Tasks
		})
	}
	for _, t := range tl.Tasks {
		if t.Status != model.StatusStarted && t.Status != model.StatusBlocked {
			continue
		}
		wg.Go(func() {
			detail, err := x.home.GetTask(ctx, t.Number)
			if err != nil {
				fail(err)
				return
			}
			if note, ok := lastNote(detail.History); ok {
				mu.Lock()
				if d.Notes == nil {
					d.Notes = map[int]string{}
				}
				d.Notes[t.Number] = note
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if len(errs) > 0 {
		return Failed{Err: errs[0]}
	}
	return Loaded{Data: d}
}

// unreadableNote stands in for a last note whose data this client cannot read.
const unreadableNote = "<the last note is unreadable>"

// lastNote is the text of the newest note in the history. When that note cannot be read it says so, and never
// shows an older note as the newest.
func lastNote(history []model.Event) (string, bool) {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Kind == model.KindNote {
			var n model.NoteData
			if json.Unmarshal(history[i].Data, &n) != nil {
				return unreadableNote, true
			}
			return n.Text, true
		}
	}
	return "", false
}

func (x *executor) open(ctx context.Context, e OpenRef) error {
	if u, ok := refURL(e.Ref); ok {
		return x.do(ctx, openerArgv(u))
	}
	path, err := refPath(e.Ref, e.Dir)
	if err != nil {
		return err
	}
	has, err := x.hasViewer(ctx)
	if err != nil {
		return err
	}
	if !has {
		return fmt.Errorf("%s is a file, and no file viewer is installed: herdr with the %s plugin opens it", e.Ref, viewerPlugin)
	}
	return x.do(ctx, viewerArgv(x.herdr, path))
}

// hasViewer asks herdr whether the file viewer is installed. Only a clean answer is kept for the program's life;
// a failed or unreadable one is returned as an error and asked again on the next ref.
func (x *executor) hasViewer(ctx context.Context) (bool, error) {
	if x.herdr == "" {
		return false, nil
	}
	x.viewerMu.Lock()
	defer x.viewerMu.Unlock()
	if x.viewerKnown {
		return x.viewer, nil
	}
	argv := viewerListArgv(x.herdr)
	out, err := x.exec(ctx, argv)
	if err != nil {
		return false, failure(argv, err, out)
	}
	if x.viewer, err = listsPlugin(out); err != nil {
		return false, fmt.Errorf("%s: %v", strings.Join(argv[:3], " "), err)
	}
	x.viewerKnown = true
	return x.viewer, nil
}
