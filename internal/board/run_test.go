package board_test

import (
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

func w5StartRun(t *testing.T, home board.Home, exec func(context.Context, []string) ([]byte, error), o *lockedOutput) (*io.PipeWriter, <-chan error) {
	return w5StartRunWithRefresh(t, home, exec, o, time.Hour)
}

func w5StartRunWithRefresh(t *testing.T, home board.Home, exec func(context.Context, []string) ([]byte, error), o *lockedOutput, refresh time.Duration) (*io.PipeWriter, <-chan error) {
	return w5StartRunWithRefreshHerdr(t, home, exec, o, "w5-herdr", refresh)
}

func w5StartRunWithRefreshHerdr(t *testing.T, home board.Home, exec func(context.Context, []string) ([]byte, error), o *lockedOutput, herdr string, refresh time.Duration) (*io.PipeWriter, <-chan error) {
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
	home := &fakeHome{}
	out := &lockedOutput{}
	in, errs := w5StartRunWithRefresh(t, home, nil, out, 5*time.Millisecond)

	eventually(t, "the timer refresh", func() bool { return home.w5ListCalls() >= 2 })
	w5Quit(t, in, errs)
}

func TestRunRefreshesOnlineDataAndTheLastNotes(t *testing.T) {
	now := time.Now()
	blocked := model.Task{Number: 21, Title: "w5 blocked task", Status: model.StatusBlocked}
	started := model.Task{Number: 22, Title: "w5 started task", Status: model.StatusStarted}
	home := &fakeHome{
		tasks:  []model.Task{blocked, started},
		runs:   []model.Run{{Task: started.Number, StartedTS: now}},
		status: api.Status{RunnerOn: true},
		detail: store.TaskDetail{History: []model.Event{{Kind: model.KindNote, Data: model.MustData(model.NoteData{Text: "w5 latest note"})}}},
	}
	out := &lockedOutput{}
	in, errs := w5StartRun(t, home, nil, out)

	eventually(t, "the online refresh and task details", func() bool {
		status, runs := home.w5OnlineCalls()
		return status == 1 && runs == 1 && home.w5GetTaskCalls() == 2 && strings.Contains(out.String(), "w5 latest note")
	})
	w5Quit(t, in, errs)
}

func TestRunRefreshesAfterWritesWithAnEmptyActor(t *testing.T) {
	home := &fakeHome{tasks: []model.Task{{Number: 7, Title: "w5 write task", Status: model.StatusOpen}}}
	out := &lockedOutput{}
	in, errs := w5StartRun(t, home, nil, out)

	eventually(t, "the first board draw", func() bool {
		return strings.Contains(out.String(), "w5 write task")
	})
	if _, err := io.WriteString(in, "s"); err != nil {
		t.Fatalf("write start key: %v", err)
	}
	eventually(t, "the start write", func() bool { return len(home.w5SetActors()) == 1 })
	eventually(t, "the refresh caused by the write", func() bool { return home.w5ListCalls() >= 2 })
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
	home := &fakeHome{
		tasks: []model.Task{{Number: 8, Title: "w5 focus task", Status: model.StatusStarted}},
		runs:  []model.Run{{Task: 8, Workspace: "w5-workspace", Pane: "w5-pane", StartedTS: now}},
	}
	exec := &w5Executor{}
	out := &lockedOutput{}
	in, errs := w5StartRun(t, home, exec.w5Exec, out)

	eventually(t, "the focus task draw", func() bool {
		return strings.Contains(out.String(), "w5 focus task")
	})
	if _, err := io.WriteString(in, "f"); err != nil {
		t.Fatalf("write focus key: %v", err)
	}
	eventually(t, "the three focus commands", func() bool { return len(exec.w5Calls()) == 3 })
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
		home := &fakeHome{
			tasks: []model.Task{{Number: 9, Title: "w5 invalid focus", Status: model.StatusStarted}},
			runs:  []model.Run{{Task: 9, Workspace: "w5 invalid workspace", Pane: "w5-pane", StartedTS: now}},
		}
		exec := &w5Executor{}
		out := &lockedOutput{}
		in, errs := w5StartRun(t, home, exec.w5Exec, out)

		eventually(t, "the invalid focus task draw", func() bool {
			return strings.Contains(out.String(), "w5 invalid focus")
		})
		before := out.Len()
		if _, err := io.WriteString(in, "f"); err != nil {
			t.Fatalf("write focus key: %v", err)
		}
		eventually(t, "the refused focus draw", func() bool { return out.Len() > before })
		w5Quit(t, in, errs)

		if got := exec.w5Calls(); len(got) != 0 {
			t.Errorf("invalid focus ran %#v, want no command", got)
		}
	})

	t.Run("invalid ref runs no command", func(t *testing.T) {
		task := model.Task{Number: 10, Title: "w5 invalid ref", Status: model.StatusOpen, Project: "/w5/project"}
		home := &fakeHome{
			tasks:  []model.Task{task},
			detail: store.TaskDetail{Task: task, History: []model.Event{{Kind: model.KindNote, Data: model.MustData(model.NoteData{Ref: "https://"})}}},
		}
		exec := &w5Executor{}
		out := &lockedOutput{}
		in, errs := w5StartRun(t, home, exec.w5Exec, out)

		eventually(t, "the invalid-ref task draw", func() bool {
			return strings.Contains(out.String(), "w5 invalid ref")
		})
		if _, err := io.WriteString(in, "\r"); err != nil {
			t.Fatalf("write enter key: %v", err)
		}
		eventually(t, "the task detail", func() bool { return home.w5GetTaskCalls() == 1 && strings.Contains(out.String(), "FILES") })
		before := out.Len()
		if _, err := io.WriteString(in, "o"); err != nil {
			t.Fatalf("write open key: %v", err)
		}
		eventually(t, "the refused ref draw", func() bool { return out.Len() > before })
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
		home := &fakeHome{
			tasks:  []model.Task{task},
			detail: store.TaskDetail{Task: task, History: []model.Event{{Kind: model.KindNote, Data: model.MustData(model.NoteData{Ref: "run_test.go"})}}},
		}
		exec := &w5Executor{answer: []byte(`{"result":{"plugins":[]}}`)}
		out := &lockedOutput{}
		in, errs := w5StartRun(t, home, exec.w5Exec, out)

		eventually(t, "the viewer task draw", func() bool {
			return strings.Contains(out.String(), "w5 viewer task")
		})
		if _, err := io.WriteString(in, "\r"); err != nil {
			t.Fatalf("write enter key: %v", err)
		}
		eventually(t, "the task detail", func() bool { return home.w5GetTaskCalls() == 1 && strings.Contains(out.String(), "FILES") })
		if _, err := io.WriteString(in, "o"); err != nil {
			t.Fatalf("write open key: %v", err)
		}
		eventually(t, "the viewer check", func() bool { return len(exec.w5Calls()) == 1 })
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
		home := &fakeHome{
			tasks:  []model.Task{task},
			detail: store.TaskDetail{Task: task, History: []model.Event{{Kind: model.KindNote, Data: model.MustData(model.NoteData{Ref: ref})}}},
		}
		exec := &w5Executor{}
		out := &lockedOutput{}
		in, errs := w5StartRun(t, home, exec.w5Exec, out)

		eventually(t, "the URL task draw", func() bool { return strings.Contains(out.String(), task.Title) })
		if _, err := io.WriteString(in, "\r"); err != nil {
			t.Fatalf("write enter key: %v", err)
		}
		eventually(t, "the URL task detail", func() bool { return home.w5GetTaskCalls() == 1 })
		if _, err := io.WriteString(in, "o"); err != nil {
			t.Fatalf("write open key: %v", err)
		}
		eventually(t, "the URL opener argv", func() bool { return len(exec.w5Calls()) == 1 })
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
		home := &fakeHome{
			tasks: []model.Task{task},
			detail: store.TaskDetail{Task: task, History: []model.Event{
				{Kind: model.KindNote, Data: model.MustData(model.NoteData{Ref: refs[0]})},
				{Kind: model.KindNote, Data: model.MustData(model.NoteData{Ref: refs[1]})},
			}},
		}
		exec := &w5Executor{answer: []byte(`{"result":{"plugins":[{}]}}`)}
		out := &lockedOutput{}
		in, errs := w5StartRun(t, home, exec.w5Exec, out)

		eventually(t, "the file task draw", func() bool { return strings.Contains(out.String(), task.Title) })
		if _, err := io.WriteString(in, "\r"); err != nil {
			t.Fatalf("write enter key: %v", err)
		}
		eventually(t, "the file task detail", func() bool { return home.w5GetTaskCalls() == 1 })
		if _, err := io.WriteString(in, "o\r"); err != nil {
			t.Fatalf("open first ref: %v", err)
		}
		eventually(t, "the first file argv", func() bool { return len(exec.w5Calls()) == 2 })
		if _, err := io.WriteString(in, "oj\r"); err != nil {
			t.Fatalf("open second ref: %v", err)
		}
		eventually(t, "the second file argv without a second viewer probe", func() bool { return len(exec.w5Calls()) == 3 })
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
		home := &fakeHome{}
		out := &lockedOutput{}
		in, errs := w5StartRun(t, home, nil, out)
		eventually(t, "the empty board draw", func() bool { return out.Len() > 0 })
		if _, err := io.WriteString(in, "+w5 added #ops @alpha\r"); err != nil {
			t.Fatalf("write add task: %v", err)
		}
		eventually(t, "the added task", func() bool { actors, _ := home.w5Add(); return len(actors) == 1 })
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
		home := &fakeHome{tasks: []model.Task{task}, detail: store.TaskDetail{Task: task}}
		out := &lockedOutput{}
		in, errs := w5StartRun(t, home, nil, out)
		eventually(t, "the step task draw", func() bool { return strings.Contains(out.String(), task.Title) })
		if _, err := io.WriteString(in, "\rt\r"); err != nil {
			t.Fatalf("write toggle step: %v", err)
		}
		eventually(t, "the step write", func() bool { actors, _ := home.w5Step(); return len(actors) == 1 })
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
		home := &fakeHome{tasks: []model.Task{task}, runs: []model.Run{{Task: task.Number, StartedTS: time.Now()}}}
		out := &lockedOutput{}
		in, errs := w5StartRun(t, home, nil, out)
		eventually(t, "the live run draw", func() bool { return strings.Contains(out.String(), task.Title) })
		if _, err := io.WriteString(in, "ky"); err != nil {
			t.Fatalf("write kill confirmation: %v", err)
		}
		eventually(t, "the kill write", func() bool { killed, _, _, _ := home.w5KillPause(); return len(killed) == 1 })
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
		home := &fakeHome{status: api.Status{RunnerState: api.RunnerStateOn}}
		out := &lockedOutput{}
		in, errs := w5StartRun(t, home, nil, out)
		eventually(t, "the runner draw", func() bool { status, _ := home.w5OnlineCalls(); return status == 1 })
		if _, err := io.WriteString(in, "P"); err != nil {
			t.Fatalf("write pause: %v", err)
		}
		eventually(t, "the pause write", func() bool { _, _, paused, _ := home.w5KillPause(); return len(paused) == 1 })
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
			home := &fakeHome{tasks: []model.Task{task}, queued: test.queued}
			out := &lockedOutput{}
			in, errs := w5StartRun(t, home, nil, out)
			eventually(t, "the blocked task draw", func() bool { return strings.Contains(out.String(), task.Title) })
			if _, err := io.WriteString(in, "n"+test.answer+"\r"); err != nil {
				t.Fatalf("write rearm answer: %v", err)
			}
			eventually(t, "the rearm result", func() bool {
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
		home := &fakeHome{tasks: []model.Task{{Number: 54, Title: "w5 offline", Status: model.StatusOpen}}, offline: true, snapshotTS: &timestamp}
		out := &lockedOutput{}
		in, errs := w5StartRun(t, home, nil, out)
		eventually(t, "the offline draw", func() bool { return strings.Contains(out.String(), "offline (snapshot") })
		w5Quit(t, in, errs)
		if status, runs := home.w5OnlineCalls(); status != 0 || runs != 0 {
			t.Errorf("offline refresh made Status=%d and ListRuns=%d calls, want neither", status, runs)
		}
	})

	t.Run("done drawer asks for done tasks", func(t *testing.T) {
		home := &fakeHome{tasks: []model.Task{{Number: 55, Title: "w5 done drawer", Status: model.StatusOpen}}}
		out := &lockedOutput{}
		in, errs := w5StartRun(t, home, nil, out)
		eventually(t, "the board draw", func() bool { return strings.Contains(out.String(), "w5 done drawer") })
		if _, err := io.WriteString(in, "d"); err != nil {
			t.Fatalf("write done drawer: %v", err)
		}
		eventually(t, "the done list query", func() bool {
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
		home *fakeHome
		err  string
	}{
		{name: "list tasks", home: &fakeHome{list: errors.New("w5 list failed")}, err: "w5 list failed"},
		{name: "status", home: &fakeHome{state: errors.New("w5 status failed")}, err: "w5 status failed"},
		{name: "runs", home: &fakeHome{run: errors.New("w5 runs failed")}, err: "w5 runs failed"},
		{name: "task details", home: &fakeHome{tasks: []model.Task{{Number: 56, Status: model.StatusStarted}}, get: errors.New("w5 detail failed")}, err: "w5 detail failed"},
	} {
		t.Run(test.name+" refusal reaches the screen", func(t *testing.T) {
			out := &lockedOutput{}
			in, errs := w5StartRun(t, test.home, nil, out)
			eventually(t, "the refresh refusal", func() bool { return strings.Contains(out.String(), test.err) })
			w5Quit(t, in, errs)
		})
	}
}

func TestRunShowsEveryEffectRefusal(t *testing.T) {
	for _, test := range []struct {
		name  string
		home  *fakeHome
		exec  func(context.Context, []string) ([]byte, error)
		input string
		err   string
		load  bool
	}{
		{name: "load task", home: &fakeHome{tasks: []model.Task{{Number: 57, Title: "w5 load", Status: model.StatusOpen}}, get: errors.New("w5 load refused")}, input: "\r", err: "w5 load refused"},
		{name: "set task", home: &fakeHome{tasks: []model.Task{{Number: 58, Title: "w5 set", Status: model.StatusOpen}}, set: errors.New("w5 set refused")}, input: "s", err: "w5 set refused"},
		{name: "step task", home: &fakeHome{tasks: []model.Task{{Number: 59, Title: "w5 step", Status: model.StatusOpen, Steps: []model.Step{{ShortID: "w5", Text: "step"}}}}, step: errors.New("w5 step refused")}, input: "\rt\r", err: "w5 step refused"},
		{name: "kill run", home: &fakeHome{tasks: []model.Task{{Number: 60, Title: "w5 kill", Status: model.StatusStarted}}, runs: []model.Run{{Task: 60, StartedTS: time.Now()}}, kill: errors.New("w5 kill refused")}, input: "ky", err: "w5 kill refused"},
		{name: "pause runner", home: &fakeHome{status: api.Status{RunnerState: api.RunnerStateOn}, pause: errors.New("w5 pause refused")}, input: "P", err: "w5 pause refused"},
		{name: "append rearm note", home: &fakeHome{tasks: []model.Task{{Number: 61, Title: "w5 rearm", Status: model.StatusBlocked}}, append: errors.New("w5 append refused")}, input: "nw5 answer\r", err: "w5 append refused"},
		{name: "focus run", home: &fakeHome{tasks: []model.Task{{Number: 62, Title: "w5 focus refusal", Status: model.StatusStarted}}, runs: []model.Run{{Task: 62, Workspace: "w5-workspace", Pane: "w5-pane", StartedTS: time.Now()}}}, exec: (&w5Executor{err: errors.New("w5 focus refused")}).w5Exec, input: "f", err: "w5 focus refused"},
		{name: "open URL", home: &fakeHome{tasks: []model.Task{{Number: 63, Title: "w5 open refusal", Status: model.StatusOpen}}, detail: store.TaskDetail{Task: model.Task{Number: 63, Title: "w5 open refusal", Status: model.StatusOpen}, History: []model.Event{{Kind: model.KindNote, Data: model.MustData(model.NoteData{Ref: "https://example.test/w5-refusal"})}}}}, exec: (&w5Executor{err: errors.New("w5 open refused")}).w5Exec, input: "\r", err: "w5 open refused", load: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			out := &lockedOutput{}
			in, errs := w5StartRun(t, test.home, test.exec, out)
			eventually(t, "the initial draw", func() bool { return out.Len() > 0 })
			if _, err := io.WriteString(in, test.input); err != nil {
				t.Fatalf("write %s input: %v", test.name, err)
			}
			if test.load {
				eventually(t, "the task detail", func() bool { return test.home.w5GetTaskCalls() == 1 && strings.Contains(out.String(), "FILES") })
				if _, err := io.WriteString(in, "o"); err != nil {
					t.Fatalf("write open ref: %v", err)
				}
			}
			eventually(t, "the effect refusal", func() bool { return strings.Contains(out.String(), test.err) })
			w5Quit(t, in, errs)
		})
	}
}

func TestRunUsesItsDefaultExecutorAndTreatsCancelledContextAsSuccess(t *testing.T) {
	t.Run("default executor receives focus argv", func(t *testing.T) {
		task := model.Task{Number: 64, Title: "w5 default executor", Status: model.StatusStarted}
		home := &fakeHome{tasks: []model.Task{task}, runs: []model.Run{{Task: task.Number, Workspace: "w5-workspace", Pane: "w5-pane", StartedTS: time.Now()}}}
		out := &lockedOutput{}
		in, errs := w5StartRun(t, home, nil, out)
		eventually(t, "the focus task draw", func() bool { return strings.Contains(out.String(), task.Title) })
		if _, err := io.WriteString(in, "f"); err != nil {
			t.Fatalf("write focus: %v", err)
		}
		eventually(t, "the default executor failure", func() bool { return strings.Contains(out.String(), "w5-herdr workspace focus") })
		w5Quit(t, in, errs)
	})

	t.Run("cancelled context is not a Run error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		in, writer := io.Pipe()
		defer writer.Close()
		errs := make(chan error, 1)
		go func() {
			errs <- board.Run(ctx, board.Options{Home: &fakeHome{}, IsHome: true, In: in, Out: &lockedOutput{}})
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
	home := &fakeHome{tasks: []model.Task{task}, detail: store.TaskDetail{Task: task, History: []model.Event{{Kind: model.KindNote, Data: model.MustData(model.NoteData{Ref: "run_test.go"})}}}}
	exec := &w5Executor{}
	out := &lockedOutput{}
	in, errs := w5StartRunWithRefreshHerdr(t, home, exec.w5Exec, out, "", time.Hour)
	eventually(t, "the task draw", func() bool { return strings.Contains(out.String(), task.Title) })
	if _, err := io.WriteString(in, "\r"); err != nil {
		t.Fatalf("write task page: %v", err)
	}
	eventually(t, "the task detail", func() bool { return home.w5GetTaskCalls() == 1 && strings.Contains(out.String(), "FILES") })
	if _, err := io.WriteString(in, "o"); err != nil {
		t.Fatalf("write open file: %v", err)
	}
	eventually(t, "the missing-viewer refusal", func() bool { return strings.Contains(out.String(), "no file viewer is installed") })
	w5Quit(t, in, errs)
	if got := exec.w5Calls(); len(got) != 0 {
		t.Errorf("missing herdr ran %#v, want no command", got)
	}
}
