package board_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/federbenjamin/desk/internal/api"
	"github.com/federbenjamin/desk/internal/board"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
)

func w6Key(code rune, text string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Text: text}
}

func w6Effects(t *testing.T, got, want []board.Effect) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v, want %#v", got, want)
	}
}

func TestCapturePopupShowsPromptAndConvertsLineToAddTask(t *testing.T) {
	capture := board.NewCapture("capture: ")
	capture, effects := capture.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	w6Effects(t, effects, nil)

	for _, r := range "ship popup #ops @alpha" {
		capture, effects = capture.Update(w6Key(r, string(r)))
		w6Effects(t, effects, nil)
	}

	if got, want := capture.Text(), "capture: ship popup #ops @alpha\n#thread  @project  ·  enter adds  ·  esc cancels"; got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}

	_, effects = capture.Update(w6Key(tea.KeyEnter, ""))
	w6Effects(t, effects, []board.Effect{board.AddTask{Data: model.TaskData{
		Title:   "ship popup",
		Thread:  "ops",
		Project: "alpha",
	}}})
}

func TestCapturePopupTurnsPastedLineIntoAnAddTask(t *testing.T) {
	capture := board.NewCapture("capture: ")
	capture, effects := capture.Update(tea.PasteMsg{Content: "ship popup #ops @alpha"})
	w6Effects(t, effects, nil)

	if got, want := capture.Text(), "capture: ship popup #ops @alpha\n#thread  @project  ·  enter adds  ·  esc cancels"; got != want {
		t.Fatalf("Text() after paste = %q, want %q", got, want)
	}

	_, effects = capture.Update(w6Key(tea.KeyEnter, ""))
	w6Effects(t, effects, []board.Effect{board.AddTask{Data: model.TaskData{
		Title:   "ship popup",
		Thread:  "ops",
		Project: "alpha",
	}}})
}

func TestCapturePopupRetainsRefusalUntilTaskLands(t *testing.T) {
	capture := board.NewCapture("capture: ")
	for _, r := range "bad line" {
		var effects []board.Effect
		capture, effects = capture.Update(w6Key(r, string(r)))
		w6Effects(t, effects, nil)
	}

	refusal := errors.New("unknown-project: no such project")
	capture, effects := capture.Update(board.Failed{Err: refusal})
	w6Effects(t, effects, nil)
	if got := capture.Text(); !strings.Contains(got, "capture: bad line") || !strings.Contains(got, refusal.Error()) {
		t.Fatalf("refused capture = %q, want its line and refusal", got)
	}

	capture, effects = capture.Update(w6Key(tea.KeyEnter, ""))
	w6Effects(t, effects, []board.Effect{board.AddTask{Data: model.TaskData{Title: "bad line"}}})
	_, effects = capture.Update(board.Added{Task: model.Task{Number: 17}})
	w6Effects(t, effects, []board.Effect{board.Quit{}})
}

func TestCapturePopupCancelsWithoutCreatingATask(t *testing.T) {
	for _, key := range []struct {
		name string
		msg  tea.KeyPressMsg
	}{
		{name: "empty line", msg: w6Key(tea.KeyEnter, "")},
		{name: "escape", msg: w6Key(tea.KeyEscape, "")},
		{name: "ctrl+c", msg: tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}},
	} {
		t.Run(key.name, func(t *testing.T) {
			capture := board.NewCapture("capture: ")
			_, effects := capture.Update(key.msg)
			w6Effects(t, effects, []board.Effect{board.Quit{}})
		})
	}
}

type w6CaptureReply struct {
	task model.Task
	err  error
}

type w6CaptureHome struct {
	mu      sync.Mutex
	replies []w6CaptureReply
	inputs  []store.AddTaskInput
	actors  []store.Actor
}

func (h *w6CaptureHome) ListTasks(context.Context, store.Filter) (api.TaskList, error) {
	return api.TaskList{}, nil
}

func (h *w6CaptureHome) GetTask(context.Context, int) (store.TaskDetail, error) {
	return store.TaskDetail{}, nil
}

func (h *w6CaptureHome) AddTask(_ context.Context, actor store.Actor, input store.AddTaskInput) (model.Task, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.inputs = append(h.inputs, input)
	h.actors = append(h.actors, actor)
	if len(h.replies) == 0 {
		return model.Task{}, errors.New("unexpected AddTask")
	}
	reply := h.replies[0]
	h.replies = h.replies[1:]
	return reply.task, reply.err
}

func (h *w6CaptureHome) SetTask(context.Context, store.Actor, int, model.Patch) (model.Task, error) {
	return model.Task{}, nil
}

func (h *w6CaptureHome) Step(context.Context, store.Actor, int, model.StepOp) (model.Task, error) {
	return model.Task{}, nil
}

func (h *w6CaptureHome) Append(context.Context, api.AppendRequest) (model.Event, bool, error) {
	return model.Event{}, false, nil
}

func (h *w6CaptureHome) ListRuns(context.Context) ([]model.Run, error) {
	return nil, nil
}

func (h *w6CaptureHome) Status(context.Context) (api.Status, error) {
	return api.Status{}, nil
}

func (h *w6CaptureHome) KillRun(context.Context, store.Actor, int) (model.Task, error) {
	return model.Task{}, nil
}

func (h *w6CaptureHome) PauseRunner(context.Context, store.Actor, bool) (api.Status, error) {
	return api.Status{}, nil
}

func (h *w6CaptureHome) calls() (inputs []store.AddTaskInput, actors []store.Actor) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]store.AddTaskInput(nil), h.inputs...), append([]store.Actor(nil), h.actors...)
}

type w6CaptureOutput struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (o *w6CaptureOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.Write(p)
}

func (o *w6CaptureOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.String()
}

func w6Eventually(t *testing.T, description string, ok func() bool) {
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

func TestCaptureFlowKeepsRefusedLineAndReturnsTheLandedTask(t *testing.T) {
	refusal := errors.New("unknown-project: no such project")
	home := &w6CaptureHome{replies: []w6CaptureReply{
		{err: refusal},
		{task: model.Task{Number: 42, Title: "ship popup", Thread: "ops", Project: "alpha"}},
	}}
	in, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	out := &w6CaptureOutput{}
	result := make(chan struct {
		task *model.Task
		err  error
	}, 1)
	go func() {
		task, err := board.Capture(context.Background(), board.Options{Home: home, In: in, Out: out})
		result <- struct {
			task *model.Task
			err  error
		}{task: task, err: err}
	}()

	if _, err := io.WriteString(writer, "bad @missing\n"); err != nil {
		t.Fatalf("write refused line: %v", err)
	}
	w6Eventually(t, "the refusal to be shown", func() bool {
		inputs, _ := home.calls()
		return len(inputs) == 1 && strings.Contains(out.String(), "bad @missing") && strings.Contains(out.String(), refusal.Error())
	})
	if _, err := io.WriteString(writer, "ship popup #ops @alpha\n"); err != nil {
		t.Fatalf("write accepted line: %v", err)
	}

	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("Capture() error = %v", got.err)
		}
		if !reflect.DeepEqual(got.task, &model.Task{Number: 42, Title: "ship popup", Thread: "ops", Project: "alpha"}) {
			t.Fatalf("Capture() task = %#v, want landed task", got.task)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Capture did not finish after its task landed")
	}

	inputs, actors := home.calls()
	wantInputs := []store.AddTaskInput{
		{TaskData: model.TaskData{Title: "bad", Project: "missing"}},
		{TaskData: model.TaskData{Title: "ship popup", Thread: "ops", Project: "alpha"}},
	}
	if !reflect.DeepEqual(inputs, wantInputs) {
		t.Errorf("AddTask inputs = %#v, want %#v", inputs, wantInputs)
	}
	if !reflect.DeepEqual(actors, []store.Actor{{}, {}}) {
		t.Errorf("AddTask actors = %#v, want empty actors", actors)
	}
	if strings.Contains(out.String(), "\x1b[?1049h") {
		t.Errorf("Capture output entered the alternate screen: %q", out.String())
	}
}
