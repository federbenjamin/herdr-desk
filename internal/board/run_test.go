package board_test

import (
	"bytes"
	"context"
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

	tasks  []model.Task
	runs   []model.Run
	detail store.TaskDetail
	status api.Status

	listCalls    int
	getTaskCalls int
	statusCalls  int
	runsCalls    int
	setActors    []store.Actor
}

func (h *w5Home) ListTasks(_ context.Context, _ store.Filter) (api.TaskList, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.listCalls++
	return api.TaskList{Tasks: append([]model.Task(nil), h.tasks...)}, nil
}

func (h *w5Home) GetTask(_ context.Context, _ int) (store.TaskDetail, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.getTaskCalls++
	return h.detail, nil
}

func (h *w5Home) AddTask(_ context.Context, _ store.Actor, _ store.AddTaskInput) (model.Task, error) {
	return model.Task{}, nil
}

func (h *w5Home) SetTask(_ context.Context, a store.Actor, number int, _ model.Patch) (model.Task, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.setActors = append(h.setActors, a)
	for _, task := range h.tasks {
		if task.Number == number {
			return task, nil
		}
	}
	return model.Task{Number: number}, nil
}

func (h *w5Home) Step(_ context.Context, _ store.Actor, number int, _ model.StepOp) (model.Task, error) {
	return model.Task{Number: number}, nil
}

func (h *w5Home) Append(_ context.Context, _ api.AppendRequest) (model.Event, bool, error) {
	return model.Event{}, false, nil
}

func (h *w5Home) ListRuns(context.Context) ([]model.Run, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.runsCalls++
	return append([]model.Run(nil), h.runs...), nil
}

func (h *w5Home) Status(context.Context) (api.Status, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.statusCalls++
	return h.status, nil
}

func (h *w5Home) KillRun(_ context.Context, _ store.Actor, task int) (model.Task, error) {
	return model.Task{Number: task}, nil
}

func (h *w5Home) PauseRunner(context.Context, store.Actor, bool) (api.Status, error) {
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
}

func (e *w5Executor) w5Exec(_ context.Context, argv []string) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, append([]string(nil), argv...))
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
	t.Helper()
	in, writer := io.Pipe()
	errs := make(chan error, 1)
	go func() {
		errs <- board.Run(context.Background(), board.Options{
			Home:    home,
			IsHome:  true,
			Herdr:   "w5-herdr",
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
			detail: store.TaskDetail{Task: task, History: []model.Event{{Kind: model.KindNote, Data: model.MustData(model.NoteData{Ref: "not a ref"})}}},
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
