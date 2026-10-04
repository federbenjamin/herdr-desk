package board

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/federbenjamin/desk/internal/api"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
)

// Config is what a State is made from.
type Config struct {
	IsHome   bool             // this machine is the home; false on a client
	CanFocus bool             // f can reach a worker's pane: the home, with herdr
	Now      func() time.Time // nil → time.Now
}

// Data is one refresh of what the home holds.
type Data struct {
	Tasks      []model.Task // the live tasks
	Done       []model.Task // the done tasks; set only when the Refresh asked for them
	Runs       []model.Run
	Status     api.Status
	Offline    bool // Tasks came from the snapshot; nothing else is set but SnapshotTS
	SnapshotTS *time.Time
	Notes      map[int]string // task number → the text of its last note; started and blocked tasks only
}

// Loaded carries a Refresh's answer.
type Loaded struct{ Data Data }

// TaskLoaded carries a LoadTask's answer. A State drops one whose task is not the task it shows.
type TaskLoaded struct{ Detail store.TaskDetail }

// Added says an AddTask landed.
type Added struct{ Task model.Task }

// Failed carries the error of an effect that failed. The status line shows Err.Error() until the next key.
type Failed struct{ Err error }

// Tick asks the State to refresh: the timer firing, or a write that succeeded.
type Tick struct{}

// Effect is one piece of I/O the State asks for.
type Effect interface{ effect() }

type (
	// Refresh loads Data. Done asks for the done tasks too.
	Refresh struct{ Done bool }
	// LoadTask loads one task with its history.
	LoadTask struct{ Task int }
	// SetTask patches a task.
	SetTask struct {
		Task  int
		Patch model.Patch
	}
	// AddTask creates a task.
	AddTask struct{ Data model.TaskData }
	// StepTask changes a task's steps.
	StepTask struct {
		Task int
		Op   model.StepOp
	}
	// Rearm appends Answer as a note on the task when it is not empty, then sets the task ready.
	Rearm struct {
		Task   int
		Answer string
	}
	// KillRun kills the task's live run.
	KillRun struct{ Task int }
	// PauseRunner pauses or resumes the runner.
	PauseRunner struct{ Paused bool }
	// FocusRun focuses the run's pane.
	FocusRun struct{ Run model.Run }
	// OpenRef opens a ref of a task whose project is Dir.
	OpenRef struct {
		Ref string
		Dir string
	}
	// Quit ends the program.
	Quit struct{}
)

func (Refresh) effect()     {}
func (LoadTask) effect()    {}
func (SetTask) effect()     {}
func (AddTask) effect()     {}
func (StepTask) effect()    {}
func (Rearm) effect()       {}
func (KillRun) effect()     {}
func (PauseRunner) effect() {}
func (FocusRun) effect()    {}
func (OpenRef) effect()     {}
func (Quit) effect()        {}

type page int

const (
	pageBoard page = iota
	pageTask
)

type projKind int

const (
	projAll projKind = iota
	projOne
	projNone
)

type projFilter struct {
	kind projKind
	name string // the base name, for projOne
}

// State is the board: both pages, the open prompt, the filters, and the last data. The zero State is not usable.
type State struct {
	cfg           Config
	width, height int
	data          Data

	page   page // the page that has the keys; in the wide layout both are drawn
	sel    int  // index into rows()
	selNum int  // the selected task, which the selection follows across refreshes
	top    int  // the board's first drawn line

	taskNum   int // the task the task page shows once enter opened it
	detail    store.TaskDetail
	hasDetail bool
	taskTop   int
	steps     bool
	stepSel   int

	proj   projFilter
	thread string
	search string
	drawer bool
	keys   bool
	status string

	waiting bool // a Refresh is unanswered
	again   bool // a Tick came while waiting

	prompt    prompt
	adding    bool
	add       CaptureState
	editing   bool
	notes     textarea.Model
	notesFrom string // the task's notes when the editor opened, or when ctrl+s last warned that they changed
	notesTop  int    // the editor's first drawn line
	picking   bool
	pickSel   int

	// The typed text of the last notes save and the last answer, so a failure of its write opens it again.
	notesOut, answerOut unsaved
}

// unsaved is text the user typed that a write carries, for a task.
type unsaved struct {
	task int
	text string
}

// NewState returns the board page with no data, 80 columns by 24 rows.
func NewState(c Config) State {
	return State{cfg: c, width: 80, height: 24}
}

func (s State) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now()
	}
	return time.Now()
}

func errText(err error) string {
	if err == nil {
		return "an effect failed"
	}
	return err.Error()
}

// Update applies one message and returns the next State and the I/O it asks for. It does no I/O.
// It takes tea.KeyPressMsg, tea.PasteMsg, tea.WindowSizeMsg, Loaded, TaskLoaded, Added, Failed, and Tick; any
// other message changes nothing.
func (s State) Update(msg tea.Msg) (State, []Effect) {
	var eff []Effect
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		if m.String() == "ctrl+d" {
			return s, nil
		}
		s.status = ""
		s, eff = s.key(m)
	case tea.PasteMsg:
		s, eff = s.paste(m)
	case tea.WindowSizeMsg:
		s.width, s.height = max(m.Width, 1), max(m.Height, 1)
		s.add.width = s.width
	case Loaded:
		s.data = m.Data
		if s.data.Offline {
			// A snapshot holds no history: the task page shows only what the snapshot holds.
			s.detail, s.hasDetail = store.TaskDetail{}, false
		}
		s.follow()
		s, eff = s.answered()
	case TaskLoaded:
		if m.Detail.Task.Number == s.shown() && s.shown() != 0 && !s.data.Offline {
			s.detail, s.hasDetail = m.Detail, true
		}
	case Added:
		s.adding = false
		s, eff = s.tick()
	case Failed:
		s, eff = s.failed(m)
	case Tick:
		s, eff = s.tick()
	default:
		return s, nil
	}
	s.scroll()
	return s, eff
}

// effectErr is the error of an effect that failed, with the effect. Run's executor wraps every Failed's error
// in one, so State can tell which outstanding write or refresh a failure answers. Its text is the error's.
type effectErr struct {
	effect Effect
	err    error
}

func (e effectErr) Error() string { return e.err.Error() }
func (e effectErr) Unwrap() error { return e.err }

// answers reports whether a Failed answers an outstanding effect that match accepts. A Failed whose error names
// no effect (one not made by Run's executor) answers every outstanding effect, as one unanswered request would.
func answers(m Failed, match func(Effect) bool) bool {
	var e effectErr
	return !errors.As(m.Err, &e) || match(e.effect)
}

func (s State) failed(m Failed) (State, []Effect) {
	text := errText(m.Err)
	shown := false
	switch {
	case s.adding && s.add.busy && answers(m, func(e Effect) bool { _, ok := e.(AddTask); return ok }):
		s, _ = s.toAdd(m)
		shown = s.adding
	case s.notesOut.task != 0 && answers(m, func(e Effect) bool {
		set, ok := e.(SetTask)
		return ok && set.Task == s.notesOut.task && set.Patch.Notes != nil
	}):
		u := s.notesOut
		s.notesOut = unsaved{}
		if s.inputOpen() {
			break
		}
		if s.shown() != u.task {
			s.page, s.taskNum, s.hasDetail, s.detail, s.taskTop = pageTask, u.task, false, store.TaskDetail{}, 0
		}
		s.editing, s.notes, s.notesTop = true, newNotes(u.text), 0
		s.notesFrom = s.task().Notes
	case s.answerOut.task != 0 && answers(m, func(e Effect) bool {
		r, ok := e.(Rearm)
		return ok && r.Task == s.answerOut.task && r.Answer != ""
	}):
		u := s.answerOut
		s.answerOut = unsaved{}
		if !s.inputOpen() {
			s.prompt = newPrompt(promptAnswer, "answer: ", u.task, u.text)
		}
	}
	if !shown {
		s.status = text
	}
	if s.waiting && answers(m, func(e Effect) bool { _, ok := e.(Refresh); return ok }) {
		return s.answered()
	}
	return s, nil
}

// inputOpen reports whether a text input, the notes editor, a prompt, or the pick list has the keys.
func (s State) inputOpen() bool {
	return s.adding || s.editing || s.prompt.kind != promptNone || s.picking
}

// answered closes the outstanding Refresh, and asks for the Tick that came while it was out.
func (s State) answered() (State, []Effect) {
	s.waiting = false
	if !s.again {
		return s, nil
	}
	s.again = false
	return s.tick()
}

// refresh asks for a Refresh as the drawer stands. While one is out it asks for nothing and holds a Tick, which
// the answer asks for, so one Refresh is out at a time.
func (s State) refresh() (State, []Effect) {
	if s.waiting {
		s.again = true
		return s, nil
	}
	s.waiting = true
	return s, []Effect{Refresh{Done: s.drawer}}
}

func (s State) tick() (State, []Effect) {
	s, eff := s.refresh()
	if n := s.shown(); eff != nil && n != 0 && !s.data.Offline {
		eff = append(eff, LoadTask{Task: n})
	}
	return s, eff
}

// paste sends pasted text to the open text input as typing it would; with none open it changes nothing.
func (s State) paste(m tea.PasteMsg) (State, []Effect) {
	switch {
	case s.adding:
		return s.toAdd(m)
	case s.editing:
		s.notes = typeNotes(s.notes, m)
	case s.prompt.kind != promptNone && !s.prompt.confirm():
		s = s.typePrompt(m)
	}
	return s, nil
}

// shown is the task a page shows: the open task page's, or in the wide layout the selection's; 0 for none.
func (s State) shown() int {
	switch {
	case s.page == pageTask:
		return s.taskNum
	case s.wide():
		return s.selNum
	}
	return 0
}

// follow keeps the selection on its task, or at the same index when the task is gone.
func (s *State) follow() {
	rows := s.rows()
	if i := slices.IndexFunc(rows, func(t model.Task) bool { return t.Number == s.selNum }); i >= 0 {
		s.sel = i
	}
	s.sel = min(s.sel, len(rows)-1)
	s.sel = max(s.sel, 0)
	s.selNum = 0
	if len(rows) > 0 {
		s.selNum = rows[s.sel].Number
	}
}

func (s *State) scroll() {
	body := s.bodyHeight(len(s.bottom(plain, s.width)))
	w := s.width
	if s.wide() {
		w, _ = s.split()
	}
	lines, selLine, span := s.boardLines(plain, w)
	s.top = keepInView(s.top, selLine, span, body, len(lines))
	if s.page == pageTask || s.wide() {
		tw := s.width
		if s.wide() {
			_, tw = s.split()
		}
		task, stepLine := s.taskBody(plain, tw)
		if stepLine >= 0 {
			s.taskTop = keepInView(s.taskTop, stepLine, 1, body, len(task))
		}
		s.taskTop = max(min(s.taskTop, len(task)-body), 0)
		if s.editing {
			s.notesTop = keepInView(s.notesTop, s.notesCursor(tw), 1, body, len(s.notesLines(colour, tw)))
		}
	}
}

// keepInView returns the first drawn line of a list of n lines, body of them drawn, moved from top as little as
// keeps lines at to at+span-1 drawn.
func keepInView(top, at, span, body, n int) int {
	if at < top {
		top = at
	}
	if at+span > top+body {
		top = at + span - body
	}
	return max(min(top, n-body), 0)
}

func (s State) selected() (model.Task, bool) {
	rows := s.rows()
	if s.sel < 0 || s.sel >= len(rows) {
		return model.Task{}, false
	}
	return rows[s.sel], true
}

func (s *State) refuse(key string) bool {
	if !s.data.Offline {
		return false
	}
	s.status = "offline: " + key + " needs the home"
	return true
}

func (s State) key(m tea.KeyPressMsg) (State, []Effect) {
	k := m.String()
	switch {
	case s.adding:
		return s.toAdd(m)
	case s.editing:
		return s.notesKey(m)
	case s.prompt.kind != promptNone:
		return s.promptKey(m)
	case s.picking:
		return s.pickKey(k)
	case s.keys:
		switch k {
		case "?", "esc":
			s.keys = false
		case "q", "ctrl+c":
			return s, []Effect{Quit{}}
		}
		return s, nil
	case s.page == pageTask:
		return s.taskKey(k)
	}
	return s.boardKey(k)
}

func (s State) boardKey(k string) (State, []Effect) {
	switch k {
	case "down", "j":
		return s.move(s.sel + 1)
	case "up":
		return s.move(s.sel - 1)
	case "g":
		return s.move(0)
	case "G":
		return s.move(len(s.rows()) - 1)
	case "enter":
		t, ok := s.selected()
		if !ok {
			return s, nil
		}
		return s.open(t.Number)
	case "+":
		if s.refuse(k) {
			return s, nil
		}
		s.adding = true
		s.add = NewCapture("add: ")
		s.add.width = s.width
		return s, nil
	case "/":
		s.prompt = newPrompt(promptSearch, "search: ", 0, s.search)
		return s, nil
	case "p":
		s.proj = s.nextProject()
		s.follow()
		return s, nil
	case "t":
		s.thread = s.nextThread()
		s.follow()
		return s, nil
	case "d":
		if s.data.Offline {
			s.status = "offline: the done drawer needs the home"
			return s, nil
		}
		s.drawer = !s.drawer
		s.follow()
		if s.drawer {
			return s.refresh()
		}
		return s, nil
	case "?":
		s.keys = true
		return s, nil
	case "esc":
		switch {
		case s.drawer:
			s.drawer = false
			s.follow()
		default:
			s.search = ""
			s.follow()
		}
		return s, nil
	case "q", "ctrl+c":
		return s, []Effect{Quit{}}
	case "P":
		return s.act(k, model.Task{})
	case "n", "s", "b", "r", "x", "a", "f", "k":
		t, ok := s.selected()
		if !ok {
			if k != "f" {
				s.refuse(k)
			}
			return s, nil
		}
		return s.act(k, t)
	}
	return s, nil
}

// move selects row i, clamped; in the wide layout a new selection asks for its task.
func (s State) move(i int) (State, []Effect) {
	rows := s.rows()
	if len(rows) == 0 {
		return s, nil
	}
	i = max(min(i, len(rows)-1), 0)
	before := s.selNum
	s.sel, s.selNum = i, rows[i].Number
	if s.wide() && s.selNum != before {
		s.taskTop = 0
		if !s.data.Offline {
			return s, []Effect{LoadTask{Task: s.selNum}}
		}
	}
	return s, nil
}

func (s State) open(n int) (State, []Effect) {
	if n != s.taskNum {
		s.hasDetail = false
		s.detail = store.TaskDetail{}
		s.taskTop = 0
	}
	s.taskNum = n
	s.page = pageTask
	s.steps = false
	if s.data.Offline {
		return s, nil
	}
	return s, []Effect{LoadTask{Task: n}}
}

func (s State) act(k string, t model.Task) (State, []Effect) {
	set := func(p model.Patch) (State, []Effect) {
		return s, []Effect{SetTask{Task: t.Number, Patch: p}}
	}
	status := func(st model.Status) model.Patch { return model.Patch{Status: &st} }
	switch k {
	case "n", "s", "b", "r", "x", "a", "k", "P":
		if s.refuse(k) {
			return s, nil
		}
	}
	switch k {
	case "n":
		if t.Status == model.StatusBlocked {
			s.prompt = newPrompt(promptAnswer, "answer: ", t.Number, "")
			return s, nil
		}
		return set(status(model.StatusReady))
	case "s":
		return set(status(model.StatusStarted))
	case "b":
		return set(status(model.StatusBlocked))
	case "r":
		return set(status(model.StatusReview))
	case "x":
		if t.Status == model.StatusReview {
			return set(status(model.StatusDone))
		}
		s.prompt = newConfirm(promptDone, fmt.Sprintf("mark %s done? y/n", taskID(t.Number)), t.Number)
		return s, nil
	case "a":
		thread := "agent"
		if t.Thread == "agent" {
			thread = ""
		}
		return set(model.Patch{Thread: &thread})
	case "f":
		r, ok := s.liveRun(t.Number)
		switch {
		case !s.cfg.CanFocus:
			s.status = "f works only on the home, with herdr"
		case !ok:
			s.status = taskID(t.Number) + " has no live run"
		case r.Pane == "":
			s.status = taskID(t.Number) + "'s run has no pane yet"
		default:
			return s, []Effect{FocusRun{Run: r}}
		}
		return s, nil
	case "k":
		if _, ok := s.liveRun(t.Number); !ok {
			s.status = taskID(t.Number) + " has no live run"
			return s, nil
		}
		s.prompt = newConfirm(promptKill, fmt.Sprintf("kill %s's run? y/n", taskID(t.Number)), t.Number)
		return s, nil
	case "P":
		switch st := s.runnerWord(); st {
		case api.RunnerStateOn:
			return s, []Effect{PauseRunner{Paused: true}}
		case api.RunnerStatePaused:
			return s, []Effect{PauseRunner{Paused: false}}
		default:
			s.status = "the runner is " + st
		}
		return s, nil
	}
	return s, nil
}

// runnerWord is the runner's state as the header names it: Status.RunnerState, else on or off from RunnerOn for
// a home that has no runner.
func (s State) runnerWord() string {
	switch {
	case s.data.Status.RunnerState != "":
		return s.data.Status.RunnerState
	case s.data.Status.RunnerOn:
		return api.RunnerStateOn
	}
	return api.RunnerStateOff
}

func (s State) nextProject() projFilter {
	var opts []projFilter
	none := false
	for _, t := range s.data.Tasks {
		if t.Project == "" {
			none = true
			continue
		}
		f := projFilter{kind: projOne, name: base(t.Project)}
		if !slices.Contains(opts, f) {
			opts = append(opts, f)
		}
	}
	if none {
		opts = append(opts, projFilter{kind: projNone})
	}
	opts = append(opts, projFilter{})
	if i := slices.Index(opts, s.proj); i >= 0 {
		return opts[(i+1)%len(opts)]
	}
	return projFilter{}
}

func (s State) nextThread() string {
	opts := []string{""}
	for _, t := range s.data.Tasks {
		if t.Thread != "" && !slices.Contains(opts, t.Thread) {
			opts = append(opts, t.Thread)
		}
	}
	if i := slices.Index(opts, s.thread); i >= 0 {
		return opts[(i+1)%len(opts)]
	}
	return ""
}

func (s State) matches(t model.Task) bool {
	switch s.proj.kind {
	case projOne:
		if t.Project == "" || base(t.Project) != s.proj.name {
			return false
		}
	case projNone:
		if t.Project != "" {
			return false
		}
	}
	if s.thread != "" && t.Thread != s.thread {
		return false
	}
	q := strings.ToLower(strings.TrimSpace(s.search))
	if q == "" {
		return true
	}
	return strings.Contains(strings.ToLower(t.Title), q) || strings.Contains(strings.ToLower(taskID(t.Number)), q)
}

// toAdd sends a message to the add box. Its Quit closes the box and does not end the program.
func (s State) toAdd(msg tea.Msg) (State, []Effect) {
	var eff []Effect
	s.add, eff = s.add.Update(msg)
	var out []Effect
	for _, e := range eff {
		if _, quit := e.(Quit); quit {
			s.adding = false
			continue
		}
		out = append(out, e)
	}
	return s, out
}

type promptKind int

const (
	promptNone promptKind = iota
	promptAnswer
	promptDone
	promptKill
	promptSearch
	promptRoot
	promptModel
	promptStepAdd
	promptStepRename
)

type prompt struct {
	kind  promptKind
	label string
	task  int
	step  string // the step a rename changes
	in    textinput.Model
}

func newPrompt(kind promptKind, label string, task int, value string) prompt {
	return prompt{kind: kind, label: label, task: task, in: newInput(value)}
}

func newConfirm(kind promptKind, question string, task int) prompt {
	return prompt{kind: kind, label: question, task: task}
}

func (p prompt) confirm() bool { return p.kind == promptDone || p.kind == promptKill }

func (p prompt) line(pl palette) string {
	if p.confirm() {
		return p.label
	}
	return p.label + inputLine(pl, p.in)
}

func newInput(value string) textinput.Model {
	in := textinput.New()
	in.Prompt = ""
	in.SetValue(value)
	in.CursorEnd()
	_ = in.Focus()
	return in
}

// typeInto sends a key or a paste to a text input. The input's value is copied first: a State is a value, and the
// input's buffer would otherwise be shared with every earlier copy of it.
func typeInto(in textinput.Model, m tea.Msg) textinput.Model {
	pos := in.Position()
	in.SetValue(in.Value())
	in.SetCursor(pos)
	in, _ = in.Update(m)
	return in
}

func inputLine(p palette, in textinput.Model) string {
	return withCursor(p, in.Value(), in.Position())
}

// withCursor draws a line with the cursor on its rune at col, or on a space after its end. The plain palette
// draws no cursor.
func withCursor(p palette, line string, col int) string {
	if !p.colour {
		return line
	}
	r := []rune(line)
	c := max(min(col, len(r)), 0)
	at, after := " ", ""
	if c < len(r) {
		at, after = string(r[c]), string(r[c+1:])
	}
	return string(r[:c]) + p.cursor(at) + after
}

// typePrompt sends a key or a paste to the open prompt's line; the search prompt filters the rows as it changes.
func (s State) typePrompt(m tea.Msg) State {
	s.prompt.in = typeInto(s.prompt.in, m)
	if s.prompt.kind == promptSearch {
		s.search = s.prompt.in.Value()
		s.follow()
	}
	return s
}

func (s State) promptKey(m tea.KeyPressMsg) (State, []Effect) {
	k := m.String()
	p := s.prompt
	if p.confirm() {
		s.prompt = prompt{}
		if k != "y" {
			return s, nil
		}
		if p.kind == promptKill {
			return s, []Effect{KillRun{Task: p.task}}
		}
		done := model.StatusDone
		return s, []Effect{SetTask{Task: p.task, Patch: model.Patch{Status: &done}}}
	}
	switch k {
	case "esc", "ctrl+c":
		if p.kind == promptSearch {
			s.search = ""
			s.follow()
		}
		s.prompt = prompt{}
		return s, nil
	case "enter":
		s.prompt = prompt{}
		v := strings.TrimSpace(p.in.Value())
		switch p.kind {
		case promptAnswer:
			s.answerOut = unsaved{}
			if v != "" {
				s.answerOut = unsaved{task: p.task, text: v}
			}
			return s, []Effect{Rearm{Task: p.task, Answer: v}}
		case promptRoot:
			return s, []Effect{SetTask{Task: p.task, Patch: model.Patch{Root: &v}}}
		case promptModel:
			return s, []Effect{SetTask{Task: p.task, Patch: model.Patch{Model: &v}}}
		case promptStepAdd:
			if v != "" {
				return s, []Effect{StepTask{Task: p.task, Op: model.StepOp{Op: "add", Text: v}}}
			}
		case promptStepRename:
			if v != "" {
				return s, []Effect{StepTask{Task: p.task, Op: model.StepOp{Op: "rename", ShortID: p.step, Text: v}}}
			}
		}
		return s, nil
	}
	return s.typePrompt(m), nil
}
