package board_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/federbenjamin/desk/internal/api"
	"github.com/federbenjamin/desk/internal/board"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
)

var _ board.Home = (*api.Client)(nil)

type w5Home struct {
	mu sync.Mutex

	tasks      []model.Task
	runs       []model.Run
	detail     store.TaskDetail
	status     api.Status
	offline    bool
	snapshotTS *time.Time
	list       error
	listErrors []error
	get        error
	add        error
	set        error
	step       error
	append     error
	run        error
	state      error
	kill       error
	pause      error
	queued     bool

	listCalls    int
	listFilters  []store.Filter
	getTaskCalls int
	statusCalls  int
	runsCalls    int
	setActors    []store.Actor
	setPatches   []model.Patch
	addActors    []store.Actor
	addInputs    []store.AddTaskInput
	stepActors   []store.Actor
	stepOps      []model.StepOp
	appendReqs   []api.AppendRequest
	killActors   []store.Actor
	killTasks    []int
	pauseActors  []store.Actor
	pauseValues  []bool
}

func (h *w5Home) ListTasks(_ context.Context, f store.Filter) (api.TaskList, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.listCalls++
	h.listFilters = append(h.listFilters, f)
	if h.list != nil {
		return api.TaskList{}, h.list
	}
	if len(h.listErrors) > 0 {
		err := h.listErrors[0]
		h.listErrors = h.listErrors[1:]
		if err != nil {
			return api.TaskList{}, err
		}
	}
	return api.TaskList{Tasks: append([]model.Task(nil), h.tasks...), Offline: h.offline, SnapshotTS: h.snapshotTS}, nil
}

func (h *w5Home) GetTask(_ context.Context, _ int) (store.TaskDetail, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.getTaskCalls++
	if h.get != nil {
		return store.TaskDetail{}, h.get
	}
	return h.detail, nil
}

func (h *w5Home) AddTask(_ context.Context, a store.Actor, in store.AddTaskInput) (model.Task, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.addActors = append(h.addActors, a)
	h.addInputs = append(h.addInputs, in)
	if h.add != nil {
		return model.Task{}, h.add
	}
	return model.Task{Number: 1}, nil
}

func (h *w5Home) SetTask(_ context.Context, a store.Actor, number int, p model.Patch) (model.Task, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.setActors = append(h.setActors, a)
	h.setPatches = append(h.setPatches, p)
	if h.set != nil {
		return model.Task{}, h.set
	}
	for _, task := range h.tasks {
		if task.Number == number {
			return task, nil
		}
	}
	return model.Task{Number: number}, nil
}

func (h *w5Home) Step(_ context.Context, a store.Actor, number int, op model.StepOp) (model.Task, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stepActors = append(h.stepActors, a)
	h.stepOps = append(h.stepOps, op)
	if h.step != nil {
		return model.Task{}, h.step
	}
	return model.Task{Number: number}, nil
}

func (h *w5Home) Append(_ context.Context, r api.AppendRequest) (model.Event, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.appendReqs = append(h.appendReqs, r)
	if h.append != nil {
		return model.Event{}, false, h.append
	}
	return model.Event{}, h.queued, nil
}

func (h *w5Home) ListRuns(context.Context) ([]model.Run, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.runsCalls++
	if h.run != nil {
		return nil, h.run
	}
	return append([]model.Run(nil), h.runs...), nil
}

func (h *w5Home) Status(context.Context) (api.Status, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.statusCalls++
	if h.state != nil {
		return api.Status{}, h.state
	}
	return h.status, nil
}

func (h *w5Home) KillRun(_ context.Context, a store.Actor, task int) (model.Task, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.killActors = append(h.killActors, a)
	h.killTasks = append(h.killTasks, task)
	if h.kill != nil {
		return model.Task{}, h.kill
	}
	return model.Task{Number: task}, nil
}

func (h *w5Home) PauseRunner(_ context.Context, a store.Actor, paused bool) (api.Status, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pauseActors = append(h.pauseActors, a)
	h.pauseValues = append(h.pauseValues, paused)
	if h.pause != nil {
		return api.Status{}, h.pause
	}
	return api.Status{}, nil
}

func (h *w5Home) w5ListCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.listCalls
}

func (h *w5Home) w5GetTaskCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.getTaskCalls
}

func (h *w5Home) w5OnlineCalls() (status, runs int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.statusCalls, h.runsCalls
}

func (h *w5Home) w5SetActors() []store.Actor {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]store.Actor(nil), h.setActors...)
}

func (h *w5Home) w5SetPatches() []model.Patch {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]model.Patch(nil), h.setPatches...)
}

func (h *w5Home) w5ListFilters() []store.Filter {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]store.Filter(nil), h.listFilters...)
}

func (h *w5Home) w5Add() ([]store.Actor, []store.AddTaskInput) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]store.Actor(nil), h.addActors...), append([]store.AddTaskInput(nil), h.addInputs...)
}

func (h *w5Home) w5Step() ([]store.Actor, []model.StepOp) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]store.Actor(nil), h.stepActors...), append([]model.StepOp(nil), h.stepOps...)
}

func (h *w5Home) w5Append() []api.AppendRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]api.AppendRequest(nil), h.appendReqs...)
}

func (h *w5Home) w5KillPause() ([]store.Actor, []int, []store.Actor, []bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]store.Actor(nil), h.killActors...), append([]int(nil), h.killTasks...), append([]store.Actor(nil), h.pauseActors...), append([]bool(nil), h.pauseValues...)
}

type w5Output struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (o *w5Output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.Write(p)
}

func (o *w5Output) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.String()
}

func (o *w5Output) Len() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.Len()
}

type w5Executor struct {
	mu     sync.Mutex
	calls  [][]string
	answer []byte
	err    error
}

func (e *w5Executor) w5Exec(_ context.Context, argv []string) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, append([]string(nil), argv...))
	if e.err != nil {
		return append([]byte(nil), e.answer...), e.err
	}
	return append([]byte(nil), e.answer...), nil
}

func (e *w5Executor) w5Calls() [][]string {
	e.mu.Lock()
	defer e.mu.Unlock()
	calls := make([][]string, len(e.calls))
	for i := range e.calls {
		calls[i] = append([]string(nil), e.calls[i]...)
	}
	return calls
}

func w5StartRun(t *testing.T, home board.Home, exec func(context.Context, []string) ([]byte, error), o *w5Output) (*io.PipeWriter, <-chan error) {
	return w5StartRunWithRefresh(t, home, exec, o, time.Hour)
}

func w5StartRunWithRefresh(t *testing.T, home board.Home, exec func(context.Context, []string) ([]byte, error), o *w5Output, refresh time.Duration) (*io.PipeWriter, <-chan error) {
	return w5StartRunWithRefreshHerdr(t, home, exec, o, "w5-herdr", refresh)
}

func w5StartRunWithRefreshHerdr(t *testing.T, home board.Home, exec func(context.Context, []string) ([]byte, error), o *w5Output, herdr string, refresh time.Duration) (*io.PipeWriter, <-chan error) {
	t.Helper()
	in, writer := io.Pipe()
	errs := make(chan error, 1)
	go func() {
		errs <- board.Run(context.Background(), board.Options{
			Home:    home,
			IsHome:  true,
			Herdr:   herdr,
			In:      in,
			Out:     o,
			Refresh: refresh,
			Exec:    exec,
		})
	}()
	t.Cleanup(func() { _ = writer.Close() })
	return writer, errs
}

func w5Eventually(t *testing.T, description string, ok func() bool) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if ok() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", description)
		case <-tick.C:
		}
	}
}

func w5Quit(t *testing.T, in *io.PipeWriter, errs <-chan error) {
	t.Helper()
	if _, err := io.WriteString(in, "q"); err != nil {
		t.Fatalf("write quit: %v", err)
	}
	select {
	case err := <-errs:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not quit")
	}
}

func TestRunTicksAtTheConfiguredRefreshInterval(t *testing.T) {
	home := &w5Home{}
	out := &w5Output{}
	in, errs := w5StartRunWithRefresh(t, home, nil, out, 5*time.Millisecond)

	w5Eventually(t, "the timer refresh", func() bool { return home.w5ListCalls() >= 2 })
	w5Quit(t, in, errs)
}

func TestRunRefreshesOnlineDataAndTheLastNotes(t *testing.T) {
	now := time.Now()
	blocked := model.Task{Number: 21, Title: "w5 blocked task", Status: model.StatusBlocked}
	started := model.Task{Number: 22, Title: "w5 started task", Status: model.StatusStarted}
	home := &w5Home{
		tasks:  []model.Task{blocked, started},
		runs:   []model.Run{{Task: started.Number, StartedTS: now}},
		status: api.Status{RunnerOn: true},
		detail: store.TaskDetail{History: []model.Event{{Kind: model.KindNote, Data: model.MustData(model.NoteData{Text: "w5 latest note"})}}},
	}
	out := &w5Output{}
	in, errs := w5StartRun(t, home, nil, out)

	w5Eventually(t, "the online refresh and task details", func() bool {
		status, runs := home.w5OnlineCalls()
		return status == 1 && runs == 1 && home.w5GetTaskCalls() == 2 && strings.Contains(out.String(), "w5 latest note")
	})
	w5Quit(t, in, errs)
}

func TestRunRefreshesAfterWritesWithAnEmptyActor(t *testing.T) {
	home := &w5Home{tasks: []model.Task{{Number: 7, Title: "w5 write task", Status: model.StatusOpen}}}
	out := &w5Output{}
	in, errs := w5StartRun(t, home, nil, out)

	w5Eventually(t, "the first board draw", func() bool {
		return strings.Contains(out.String(), "w5 write task")
	})
	if _, err := io.WriteString(in, "s"); err != nil {
		t.Fatalf("write start key: %v", err)
	}
	w5Eventually(t, "the start write", func() bool { return len(home.w5SetActors()) == 1 })
	w5Eventually(t, "the refresh caused by the write", func() bool { return home.w5ListCalls() >= 2 })
	w5Quit(t, in, errs)

	if got := home.w5SetActors(); !reflect.DeepEqual(got, []store.Actor{{}}) {
		t.Errorf("write actors = %#v, want one empty actor", got)
	}
	if !strings.Contains(out.String(), "\x1b[?1049h") {
		t.Error("Run did not enter the alternate screen")
	}
}

func TestRunFocusesOnlyTheSelectedRunsWorkspaceAndPane(t *testing.T) {
	now := time.Now()
	home := &w5Home{
		tasks: []model.Task{{Number: 8, Title: "w5 focus task", Status: model.StatusStarted}},
		runs:  []model.Run{{Task: 8, Workspace: "w5-workspace", Pane: "w5-pane", StartedTS: now}},
	}
	exec := &w5Executor{}
	out := &w5Output{}
	in, errs := w5StartRun(t, home, exec.w5Exec, out)

	w5Eventually(t, "the focus task draw", func() bool {
		return strings.Contains(out.String(), "w5 focus task")
	})
	if _, err := io.WriteString(in, "f"); err != nil {
		t.Fatalf("write focus key: %v", err)
	}
	w5Eventually(t, "the three focus commands", func() bool { return len(exec.w5Calls()) == 3 })
	w5Quit(t, in, errs)

	want := [][]string{
		{"w5-herdr", "workspace", "focus", "w5-workspace"},
		{"w5-herdr", "pane", "zoom", "w5-pane", "--on"},
		{"w5-herdr", "pane", "zoom", "w5-pane", "--off"},
	}
	if got := exec.w5Calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("focus argv = %#v, want %#v", got, want)
	}
}

func TestRunRejectsInvalidTargetsAndAViewerThatIsNotInstalled(t *testing.T) {
	t.Run("invalid pane or workspace runs no command", func(t *testing.T) {
		now := time.Now()
		home := &w5Home{
			tasks: []model.Task{{Number: 9, Title: "w5 invalid focus", Status: model.StatusStarted}},
			runs:  []model.Run{{Task: 9, Workspace: "w5 invalid workspace", Pane: "w5-pane", StartedTS: now}},
		}
		exec := &w5Executor{}
		out := &w5Output{}
		in, errs := w5StartRun(t, home, exec.w5Exec, out)

		w5Eventually(t, "the invalid focus task draw", func() bool {
			return strings.Contains(out.String(), "w5 invalid focus")
		})
		before := out.Len()
		if _, err := io.WriteString(in, "f"); err != nil {
			t.Fatalf("write focus key: %v", err)
		}
		w5Eventually(t, "the refused focus draw", func() bool { return out.Len() > before })
		w5Quit(t, in, errs)

		if got := exec.w5Calls(); len(got) != 0 {
			t.Errorf("invalid focus ran %#v, want no command", got)
		}
	})

	t.Run("invalid ref runs no command", func(t *testing.T) {
		task := model.Task{Number: 10, Title: "w5 invalid ref", Status: model.StatusOpen, Project: "/w5/project"}
		home := &w5Home{
			tasks:  []model.Task{task},
			detail: store.TaskDetail{Task: task, History: []model.Event{{Kind: model.KindNote, Data: model.MustData(model.NoteData{Ref: "https://"})}}},
		}
		exec := &w5Executor{}
		out := &w5Output{}
		in, errs := w5StartRun(t, home, exec.w5Exec, out)

		w5Eventually(t, "the invalid-ref task draw", func() bool {
			return strings.Contains(out.String(), "w5 invalid ref")
		})
		if _, err := io.WriteString(in, "\r"); err != nil {
			t.Fatalf("write enter key: %v", err)
		}
		w5Eventually(t, "the task detail", func() bool { return home.w5GetTaskCalls() == 1 && strings.Contains(out.String(), "FILES") })
		before := out.Len()
		if _, err := io.WriteString(in, "o"); err != nil {
			t.Fatalf("write open key: %v", err)
		}
		w5Eventually(t, "the refused ref draw", func() bool { return out.Len() > before })
		w5Quit(t, in, errs)

		if got := exec.w5Calls(); len(got) != 0 {
			t.Errorf("invalid ref ran %#v, want no command", got)
		}
	})

	t.Run("missing viewer does not open a valid file", func(t *testing.T) {
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat("run_test.go"); err != nil {
			t.Fatalf("test file is not available to validate: %v", err)
		}
		task := model.Task{Number: 11, Title: "w5 viewer task", Status: model.StatusOpen, Project: wd}
		home := &w5Home{
			tasks:  []model.Task{task},
			detail: store.TaskDetail{Task: task, History: []model.Event{{Kind: model.KindNote, Data: model.MustData(model.NoteData{Ref: "run_test.go"})}}},
		}
		exec := &w5Executor{answer: []byte(`{"result":{"plugins":[]}}`)}
		out := &w5Output{}
		in, errs := w5StartRun(t, home, exec.w5Exec, out)

		w5Eventually(t, "the viewer task draw", func() bool {
			return strings.Contains(out.String(), "w5 viewer task")
		})
		if _, err := io.WriteString(in, "\r"); err != nil {
			t.Fatalf("write enter key: %v", err)
		}
		w5Eventually(t, "the task detail", func() bool { return home.w5GetTaskCalls() == 1 && strings.Contains(out.String(), "FILES") })
		if _, err := io.WriteString(in, "o"); err != nil {
			t.Fatalf("write open key: %v", err)
		}
		w5Eventually(t, "the viewer check", func() bool { return len(exec.w5Calls()) == 1 })
		w5Quit(t, in, errs)

		want := [][]string{{"w5-herdr", "plugin", "list", "--plugin", "herdr-file-viewer", "--json"}}
		if got := exec.w5Calls(); !reflect.DeepEqual(got, want) {
			t.Errorf("missing-viewer argv = %#v, want only %#v", got, want)
		}
	})
}

func TestRunOpensURLAndExistingFileRefsWithArgv(t *testing.T) {
	t.Run("URL goes straight to the operating system opener", func(t *testing.T) {
		const ref = "https://example.test/w5"
		task := model.Task{Number: 31, Title: "w5 URL ref", Status: model.StatusOpen}
		home := &w5Home{
			tasks:  []model.Task{task},
			detail: store.TaskDetail{Task: task, History: []model.Event{{Kind: model.KindNote, Data: model.MustData(model.NoteData{Ref: ref})}}},
		}
		exec := &w5Executor{}
		out := &w5Output{}
		in, errs := w5StartRun(t, home, exec.w5Exec, out)

		w5Eventually(t, "the URL task draw", func() bool { return strings.Contains(out.String(), task.Title) })
		if _, err := io.WriteString(in, "\r"); err != nil {
			t.Fatalf("write enter key: %v", err)
		}
		w5Eventually(t, "the URL task detail", func() bool { return home.w5GetTaskCalls() == 1 })
		if _, err := io.WriteString(in, "o"); err != nil {
			t.Fatalf("write open key: %v", err)
		}
		w5Eventually(t, "the URL opener argv", func() bool { return len(exec.w5Calls()) == 1 })
		w5Quit(t, in, errs)

		opener := "xdg-open"
		if runtime.GOOS == "darwin" {
			opener = "open"
		}
		if got, want := exec.w5Calls(), [][]string{{opener, ref}}; !reflect.DeepEqual(got, want) {
			t.Errorf("URL argv = %#v, want %#v", got, want)
		}
	})

	t.Run("existing files use the viewer and probe it once", func(t *testing.T) {
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		refs := []string{"run_test.go", "open.go"}
		for _, ref := range refs {
			if _, err := os.Stat(filepath.Join(wd, ref)); err != nil {
				t.Fatalf("%s is not available to validate: %v", ref, err)
			}
		}
		task := model.Task{Number: 32, Title: "w5 file refs", Status: model.StatusOpen, Project: wd}
		home := &w5Home{
			tasks: []model.Task{task},
			detail: store.TaskDetail{Task: task, History: []model.Event{
				{Kind: model.KindNote, Data: model.MustData(model.NoteData{Ref: refs[0]})},
				{Kind: model.KindNote, Data: model.MustData(model.NoteData{Ref: refs[1]})},
			}},
		}
		exec := &w5Executor{answer: []byte(`{"result":{"plugins":[{}]}}`)}
		out := &w5Output{}
		in, errs := w5StartRun(t, home, exec.w5Exec, out)

		w5Eventually(t, "the file task draw", func() bool { return strings.Contains(out.String(), task.Title) })
		if _, err := io.WriteString(in, "\r"); err != nil {
			t.Fatalf("write enter key: %v", err)
		}
		w5Eventually(t, "the file task detail", func() bool { return home.w5GetTaskCalls() == 1 })
		if _, err := io.WriteString(in, "o\r"); err != nil {
			t.Fatalf("open first ref: %v", err)
		}
		w5Eventually(t, "the first file argv", func() bool { return len(exec.w5Calls()) == 2 })
		if _, err := io.WriteString(in, "oj\r"); err != nil {
			t.Fatalf("open second ref: %v", err)
		}
		w5Eventually(t, "the second file argv without a second viewer probe", func() bool { return len(exec.w5Calls()) == 3 })
		w5Quit(t, in, errs)

		want := [][]string{
			{"w5-herdr", "plugin", "list", "--plugin", "herdr-file-viewer", "--json"},
			{"w5-herdr", "plugin", "pane", "open", "--plugin", "herdr-file-viewer", "--entrypoint", "file-viewer", "--env", "HERDR_FILE_VIEWER_OPEN=" + filepath.Join(wd, refs[0]), "--focus"},
			{"w5-herdr", "plugin", "pane", "open", "--plugin", "herdr-file-viewer", "--entrypoint", "file-viewer", "--env", "HERDR_FILE_VIEWER_OPEN=" + filepath.Join(wd, refs[1]), "--focus"},
		}
		if got := exec.w5Calls(); !reflect.DeepEqual(got, want) {
			t.Errorf("file argv = %#v, want %#v", got, want)
		}
	})
}

func TestRunExecutesEveryBoardWriteWithAnEmptyActor(t *testing.T) {
	t.Run("adds a parsed task", func(t *testing.T) {
		home := &w5Home{}
		out := &w5Output{}
		in, errs := w5StartRun(t, home, nil, out)
		w5Eventually(t, "the empty board draw", func() bool { return out.Len() > 0 })
		if _, err := io.WriteString(in, "+w5 added #ops @alpha\r"); err != nil {
			t.Fatalf("write add task: %v", err)
		}
		w5Eventually(t, "the added task", func() bool { actors, _ := home.w5Add(); return len(actors) == 1 })
		w5Quit(t, in, errs)

		actors, inputs := home.w5Add()
		if !reflect.DeepEqual(actors, []store.Actor{{}}) {
			t.Errorf("AddTask actors = %#v, want one empty actor", actors)
		}
		if !reflect.DeepEqual(inputs, []store.AddTaskInput{{TaskData: model.TaskData{Title: "w5 added", Thread: "ops", Project: "alpha"}}}) {
			t.Errorf("AddTask inputs = %#v", inputs)
		}
	})

	t.Run("toggles a step", func(t *testing.T) {
		task := model.Task{Number: 51, Title: "w5 stepped", Status: model.StatusOpen, Steps: []model.Step{{ShortID: "w5-step", Text: "ship"}}}
		home := &w5Home{tasks: []model.Task{task}, detail: store.TaskDetail{Task: task}}
		out := &w5Output{}
		in, errs := w5StartRun(t, home, nil, out)
		w5Eventually(t, "the step task draw", func() bool { return strings.Contains(out.String(), task.Title) })
		if _, err := io.WriteString(in, "\rt\r"); err != nil {
			t.Fatalf("write toggle step: %v", err)
		}
		w5Eventually(t, "the step write", func() bool { actors, _ := home.w5Step(); return len(actors) == 1 })
		w5Quit(t, in, errs)

		actors, ops := home.w5Step()
		if !reflect.DeepEqual(actors, []store.Actor{{}}) {
			t.Errorf("Step actors = %#v, want one empty actor", actors)
		}
		if !reflect.DeepEqual(ops, []model.StepOp{{Op: "toggle", ShortID: "w5-step"}}) {
			t.Errorf("Step operations = %#v", ops)
		}
	})

	t.Run("kills the selected live run", func(t *testing.T) {
		task := model.Task{Number: 52, Title: "w5 running", Status: model.StatusStarted}
		home := &w5Home{tasks: []model.Task{task}, runs: []model.Run{{Task: task.Number, StartedTS: time.Now()}}}
		out := &w5Output{}
		in, errs := w5StartRun(t, home, nil, out)
		w5Eventually(t, "the live run draw", func() bool { return strings.Contains(out.String(), task.Title) })
		if _, err := io.WriteString(in, "ky"); err != nil {
			t.Fatalf("write kill confirmation: %v", err)
		}
		w5Eventually(t, "the kill write", func() bool { killed, _, _, _ := home.w5KillPause(); return len(killed) == 1 })
		w5Quit(t, in, errs)

		killed, tasks, _, _ := home.w5KillPause()
		if !reflect.DeepEqual(killed, []store.Actor{{}}) {
			t.Errorf("KillRun actors = %#v, want one empty actor", killed)
		}
		if !reflect.DeepEqual(tasks, []int{task.Number}) {
			t.Errorf("KillRun tasks = %#v, want %#v", tasks, []int{task.Number})
		}
	})

	t.Run("pauses the runner", func(t *testing.T) {
		home := &w5Home{status: api.Status{RunnerState: api.RunnerStateOn}}
		out := &w5Output{}
		in, errs := w5StartRun(t, home, nil, out)
		w5Eventually(t, "the runner draw", func() bool { status, _ := home.w5OnlineCalls(); return status == 1 })
		if _, err := io.WriteString(in, "P"); err != nil {
			t.Fatalf("write pause: %v", err)
		}
		w5Eventually(t, "the pause write", func() bool { _, _, paused, _ := home.w5KillPause(); return len(paused) == 1 })
		w5Quit(t, in, errs)

		_, _, paused, values := home.w5KillPause()
		if !reflect.DeepEqual(paused, []store.Actor{{}}) {
			t.Errorf("PauseRunner actors = %#v, want one empty actor", paused)
		}
		if !reflect.DeepEqual(values, []bool{true}) {
			t.Errorf("PauseRunner values = %#v, want []bool{true}", values)
		}
	})
}

func TestRunRearmsBlockedTasksBeforeSettingThemReady(t *testing.T) {
	for _, test := range []struct {
		name       string
		answer     string
		queued     bool
		wantAppend bool
		wantSet    bool
		wantStatus string
	}{
		{name: "answer appends then readies", answer: "w5 answer", wantAppend: true, wantSet: true},
		{name: "empty answer readies without a note", wantSet: true},
		{name: "queued answer leaves the task blocked", answer: "w5 queued", queued: true, wantAppend: true, wantStatus: "the home did not answer"},
	} {
		t.Run(test.name, func(t *testing.T) {
			task := model.Task{Number: 53, Title: "w5 blocked", Status: model.StatusBlocked}
			home := &w5Home{tasks: []model.Task{task}, queued: test.queued}
			out := &w5Output{}
			in, errs := w5StartRun(t, home, nil, out)
			w5Eventually(t, "the blocked task draw", func() bool { return strings.Contains(out.String(), task.Title) })
			if _, err := io.WriteString(in, "n"+test.answer+"\r"); err != nil {
				t.Fatalf("write rearm answer: %v", err)
			}
			w5Eventually(t, "the rearm result", func() bool {
				return len(home.w5Append()) == boolCount(test.wantAppend) && len(home.w5SetActors()) == boolCount(test.wantSet) && (test.wantStatus == "" || strings.Contains(out.String(), test.wantStatus))
			})
			w5Quit(t, in, errs)

			if got := home.w5Append(); test.wantAppend {
				if len(got) != 1 || got[0].Actor != (store.Actor{}) || got[0].Kind != model.KindNote || got[0].Note == nil || got[0].Note.Task != task.Number || got[0].Note.Text != test.answer {
					t.Errorf("Append request = %#v", got)
				}
			} else if len(got) != 0 {
				t.Errorf("Append requests = %#v, want none", got)
			}
			if got := home.w5SetActors(); len(got) != boolCount(test.wantSet) {
				t.Errorf("SetTask calls = %#v, want %d", got, boolCount(test.wantSet))
			}
			if test.wantSet {
				ready := model.StatusReady
				if got := home.w5SetPatches(); !reflect.DeepEqual(got, []model.Patch{{Status: &ready}}) {
					t.Errorf("SetTask patches = %#v, want ready", got)
				}
			}
		})
	}
}

func boolCount(v bool) int {
	if v {
		return 1
	}
	return 0
}

func TestRunRefreshesOfflineDoneAndRefusals(t *testing.T) {
	t.Run("offline does not ask for online data", func(t *testing.T) {
		timestamp := time.Now()
		home := &w5Home{tasks: []model.Task{{Number: 54, Title: "w5 offline", Status: model.StatusOpen}}, offline: true, snapshotTS: &timestamp}
		out := &w5Output{}
		in, errs := w5StartRun(t, home, nil, out)
		w5Eventually(t, "the offline draw", func() bool { return strings.Contains(out.String(), "offline (snapshot") })
		w5Quit(t, in, errs)
		if status, runs := home.w5OnlineCalls(); status != 0 || runs != 0 {
			t.Errorf("offline refresh made Status=%d and ListRuns=%d calls, want neither", status, runs)
		}
	})

	t.Run("done drawer asks for done tasks", func(t *testing.T) {
		home := &w5Home{tasks: []model.Task{{Number: 55, Title: "w5 done drawer", Status: model.StatusOpen}}}
		out := &w5Output{}
		in, errs := w5StartRun(t, home, nil, out)
		w5Eventually(t, "the board draw", func() bool { return strings.Contains(out.String(), "w5 done drawer") })
		if _, err := io.WriteString(in, "d"); err != nil {
			t.Fatalf("write done drawer: %v", err)
		}
		w5Eventually(t, "the done list query", func() bool {
			for _, f := range home.w5ListFilters() {
				if reflect.DeepEqual(f, store.Filter{Statuses: []model.Status{model.StatusDone}}) {
					return true
				}
			}
			return false
		})
		w5Quit(t, in, errs)
	})

	for _, test := range []struct {
		name string
		home *w5Home
		err  string
	}{
		{name: "list tasks", home: &w5Home{list: errors.New("w5 list failed")}, err: "w5 list failed"},
		{name: "status", home: &w5Home{state: errors.New("w5 status failed")}, err: "w5 status failed"},
		{name: "runs", home: &w5Home{run: errors.New("w5 runs failed")}, err: "w5 runs failed"},
		{name: "task details", home: &w5Home{tasks: []model.Task{{Number: 56, Status: model.StatusStarted}}, get: errors.New("w5 detail failed")}, err: "w5 detail failed"},
	} {
		t.Run(test.name+" refusal reaches the screen", func(t *testing.T) {
			out := &w5Output{}
			in, errs := w5StartRun(t, test.home, nil, out)
			w5Eventually(t, "the refresh refusal", func() bool { return strings.Contains(out.String(), test.err) })
			w5Quit(t, in, errs)
		})
	}
}

func TestRunShowsEveryEffectRefusal(t *testing.T) {
	for _, test := range []struct {
		name  string
		home  *w5Home
		exec  func(context.Context, []string) ([]byte, error)
		input string
		err   string
		load  bool
	}{
		{name: "load task", home: &w5Home{tasks: []model.Task{{Number: 57, Title: "w5 load", Status: model.StatusOpen}}, get: errors.New("w5 load refused")}, input: "\r", err: "w5 load refused"},
		{name: "set task", home: &w5Home{tasks: []model.Task{{Number: 58, Title: "w5 set", Status: model.StatusOpen}}, set: errors.New("w5 set refused")}, input: "s", err: "w5 set refused"},
		{name: "step task", home: &w5Home{tasks: []model.Task{{Number: 59, Title: "w5 step", Status: model.StatusOpen, Steps: []model.Step{{ShortID: "w5", Text: "step"}}}}, step: errors.New("w5 step refused")}, input: "\rt\r", err: "w5 step refused"},
		{name: "kill run", home: &w5Home{tasks: []model.Task{{Number: 60, Title: "w5 kill", Status: model.StatusStarted}}, runs: []model.Run{{Task: 60, StartedTS: time.Now()}}, kill: errors.New("w5 kill refused")}, input: "ky", err: "w5 kill refused"},
		{name: "pause runner", home: &w5Home{status: api.Status{RunnerState: api.RunnerStateOn}, pause: errors.New("w5 pause refused")}, input: "P", err: "w5 pause refused"},
		{name: "append rearm note", home: &w5Home{tasks: []model.Task{{Number: 61, Title: "w5 rearm", Status: model.StatusBlocked}}, append: errors.New("w5 append refused")}, input: "nw5 answer\r", err: "w5 append refused"},
		{name: "focus run", home: &w5Home{tasks: []model.Task{{Number: 62, Title: "w5 focus refusal", Status: model.StatusStarted}}, runs: []model.Run{{Task: 62, Workspace: "w5-workspace", Pane: "w5-pane", StartedTS: time.Now()}}}, exec: (&w5Executor{err: errors.New("w5 focus refused")}).w5Exec, input: "f", err: "w5 focus refused"},
		{name: "open URL", home: &w5Home{tasks: []model.Task{{Number: 63, Title: "w5 open refusal", Status: model.StatusOpen}}, detail: store.TaskDetail{Task: model.Task{Number: 63, Title: "w5 open refusal", Status: model.StatusOpen}, History: []model.Event{{Kind: model.KindNote, Data: model.MustData(model.NoteData{Ref: "https://example.test/w5-refusal"})}}}}, exec: (&w5Executor{err: errors.New("w5 open refused")}).w5Exec, input: "\r", err: "w5 open refused", load: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			out := &w5Output{}
			in, errs := w5StartRun(t, test.home, test.exec, out)
			w5Eventually(t, "the initial draw", func() bool { return out.Len() > 0 })
			if _, err := io.WriteString(in, test.input); err != nil {
				t.Fatalf("write %s input: %v", test.name, err)
			}
			if test.load {
				w5Eventually(t, "the task detail", func() bool { return test.home.w5GetTaskCalls() == 1 && strings.Contains(out.String(), "FILES") })
				if _, err := io.WriteString(in, "o"); err != nil {
					t.Fatalf("write open ref: %v", err)
				}
			}
			w5Eventually(t, "the effect refusal", func() bool { return strings.Contains(out.String(), test.err) })
			w5Quit(t, in, errs)
		})
	}
}

func TestRunUsesItsDefaultExecutorAndTreatsCancelledContextAsSuccess(t *testing.T) {
	t.Run("default executor receives focus argv", func(t *testing.T) {
		task := model.Task{Number: 64, Title: "w5 default executor", Status: model.StatusStarted}
		home := &w5Home{tasks: []model.Task{task}, runs: []model.Run{{Task: task.Number, Workspace: "w5-workspace", Pane: "w5-pane", StartedTS: time.Now()}}}
		out := &w5Output{}
		in, errs := w5StartRun(t, home, nil, out)
		w5Eventually(t, "the focus task draw", func() bool { return strings.Contains(out.String(), task.Title) })
		if _, err := io.WriteString(in, "f"); err != nil {
			t.Fatalf("write focus: %v", err)
		}
		w5Eventually(t, "the default executor failure", func() bool { return strings.Contains(out.String(), "w5-herdr workspace focus") })
		w5Quit(t, in, errs)
	})

	t.Run("cancelled context is not a Run error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		in, writer := io.Pipe()
		defer writer.Close()
		errs := make(chan error, 1)
		go func() {
			errs <- board.Run(ctx, board.Options{Home: &w5Home{}, IsHome: true, In: in, Out: &w5Output{}})
		}()
		cancel()
		select {
		case err := <-errs:
			if err != nil {
				t.Fatalf("Run() after cancellation = %v, want nil", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Run did not stop after context cancellation")
		}
	})
}

func TestRunDoesNotProbeForAViewerWithoutHerdr(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	task := model.Task{Number: 65, Title: "w5 no viewer", Status: model.StatusOpen, Project: wd}
	home := &w5Home{tasks: []model.Task{task}, detail: store.TaskDetail{Task: task, History: []model.Event{{Kind: model.KindNote, Data: model.MustData(model.NoteData{Ref: "run_test.go"})}}}}
	exec := &w5Executor{}
	out := &w5Output{}
	in, errs := w5StartRunWithRefreshHerdr(t, home, exec.w5Exec, out, "", time.Hour)
	w5Eventually(t, "the task draw", func() bool { return strings.Contains(out.String(), task.Title) })
	if _, err := io.WriteString(in, "\r"); err != nil {
		t.Fatalf("write task page: %v", err)
	}
	w5Eventually(t, "the task detail", func() bool { return home.w5GetTaskCalls() == 1 && strings.Contains(out.String(), "FILES") })
	if _, err := io.WriteString(in, "o"); err != nil {
		t.Fatalf("write open file: %v", err)
	}
	w5Eventually(t, "the missing-viewer refusal", func() bool { return strings.Contains(out.String(), "no file viewer is installed") })
	w5Quit(t, in, errs)
	if got := exec.w5Calls(); len(got) != 0 {
		t.Errorf("missing herdr ran %#v, want no command", got)
	}
}
