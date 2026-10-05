package board_test

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/federbenjamin/herdr-desk/internal/board"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func TestCapturePopupShowsPromptAndConvertsLineToAddTask(t *testing.T) {
	capture := board.NewCapture("capture: ")
	capture, effects := capture.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	wantEffects(t, effects, nil)

	for _, r := range "ship popup #ops @alpha" {
		capture, effects = capture.Update(press(r))
		wantEffects(t, effects, nil)
	}

	if got, want := capture.Text(), "capture: ship popup #ops @alpha\n#thread  @project  ·  enter adds  ·  esc cancels"; got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}

	_, effects = capture.Update(named(tea.KeyEnter))
	wantEffects(t, effects, []board.Effect{board.AddTask{Data: model.TaskData{
		Title:   "ship popup",
		Thread:  "ops",
		Project: "alpha",
	}}})
}

func TestCapturePopupTurnsPastedLineIntoAnAddTask(t *testing.T) {
	capture := board.NewCapture("capture: ")
	capture, effects := capture.Update(tea.PasteMsg{Content: "ship popup #ops @alpha"})
	wantEffects(t, effects, nil)

	if got, want := capture.Text(), "capture: ship popup #ops @alpha\n#thread  @project  ·  enter adds  ·  esc cancels"; got != want {
		t.Fatalf("Text() after paste = %q, want %q", got, want)
	}

	_, effects = capture.Update(named(tea.KeyEnter))
	wantEffects(t, effects, []board.Effect{board.AddTask{Data: model.TaskData{
		Title:   "ship popup",
		Thread:  "ops",
		Project: "alpha",
	}}})
}

func TestCapturePopupRetainsRefusalUntilTaskLands(t *testing.T) {
	capture := board.NewCapture("capture: ")
	for _, r := range "bad line" {
		var effects []board.Effect
		capture, effects = capture.Update(press(r))
		wantEffects(t, effects, nil)
	}

	refusal := errors.New("unknown-project: no such project")
	capture, effects := capture.Update(board.Failed{Err: refusal})
	wantEffects(t, effects, nil)
	if got := capture.Text(); !strings.Contains(got, "capture: bad line") || !strings.Contains(got, refusal.Error()) {
		t.Fatalf("refused capture = %q, want its line and refusal", got)
	}

	capture, effects = capture.Update(named(tea.KeyEnter))
	wantEffects(t, effects, []board.Effect{board.AddTask{Data: model.TaskData{Title: "bad line"}}})
	_, effects = capture.Update(board.Added{Task: model.Task{Number: 17}})
	wantEffects(t, effects, []board.Effect{board.Quit{}})
}

func TestCapturePopupCancelsWithoutCreatingATask(t *testing.T) {
	for _, key := range []struct {
		name string
		msg  tea.KeyPressMsg
	}{
		{name: "empty line", msg: named(tea.KeyEnter)},
		{name: "escape", msg: named(tea.KeyEscape)},
		{name: "ctrl+c", msg: tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}},
	} {
		t.Run(key.name, func(t *testing.T) {
			capture := board.NewCapture("capture: ")
			_, effects := capture.Update(key.msg)
			wantEffects(t, effects, []board.Effect{board.Quit{}})
		})
	}
}

func TestCapturePopupSendsOneAddTaskAndWaitsForItsAnswer(t *testing.T) {
	capture := board.NewCapture("capture: ")
	capture, _ = capture.Update(press('a'))
	capture, effects := capture.Update(named(tea.KeyEnter))
	wantEffects(t, effects, []board.Effect{board.AddTask{Data: model.TaskData{Title: "a"}}})
	for _, msg := range []tea.Msg{named(tea.KeyEnter), press('b'), tea.PasteMsg{Content: "c"}, named(tea.KeyEsc), ctrl('c')} {
		capture, effects = capture.Update(msg)
		wantEffects(t, effects, nil)
	}
	if got := capture.Text(); !strings.HasPrefix(got, "capture: a\n") {
		t.Fatalf("the line changed while its add was out: %q", got)
	}
	_, effects = capture.Update(board.Added{Task: model.Task{Number: 3}})
	wantEffects(t, effects, []board.Effect{board.Quit{}})

	// A cancel that waited ends the popup when the add fails too; with no cancel the line is kept for a retry.
	for _, cancel := range []bool{true, false} {
		capture := board.NewCapture("capture: ")
		capture, _ = capture.Update(press('a'))
		capture, _ = capture.Update(named(tea.KeyEnter))
		if cancel {
			capture, _ = capture.Update(named(tea.KeyEsc))
		}
		capture, effects = capture.Update(board.Failed{Err: errors.New("refused")})
		if cancel {
			wantEffects(t, effects, []board.Effect{board.Quit{}})
			continue
		}
		wantEffects(t, effects, nil)
		_, effects = capture.Update(named(tea.KeyEnter))
		wantEffects(t, effects, []board.Effect{board.AddTask{Data: model.TaskData{Title: "a"}}})
	}
}

func TestCaptureFlowKeepsRefusedLineAndReturnsTheLandedTask(t *testing.T) {
	refusal := errors.New("unknown-project: no such project")
	landed := model.Task{Number: 42, Title: "ship popup", Thread: "ops", Project: "alpha"}
	home := &fakeHome{only: map[string]bool{"AddTask": true}, addReplies: []addReply{{err: refusal}, {task: landed}}}
	in, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	out := &lockedOutput{}
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

	// A terminal sends Enter as a carriage return; ctrl+u clears the kept line before the next one is typed.
	if _, err := io.WriteString(writer, "bad @missing\r"); err != nil {
		t.Fatalf("write refused line: %v", err)
	}
	eventually(t, "the refusal to be shown", func() bool {
		_, inputs := home.w5Add()
		return len(inputs) == 1 && strings.Contains(out.String(), refusal.Error())
	})
	if !strings.Contains(out.String(), "bad @missing") {
		t.Fatalf("refused line was not kept in the popup: %q", out.String())
	}
	if _, err := io.WriteString(writer, "\x15ship popup #ops @alpha\r"); err != nil {
		t.Fatalf("write accepted line: %v", err)
	}

	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("Capture() error = %v", got.err)
		}
		if !reflect.DeepEqual(got.task, &landed) {
			t.Fatalf("Capture() task = %#v, want landed task", got.task)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Capture did not finish after its task landed")
	}

	actors, inputs := home.w5Add()
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
	if got := home.unscriptedCalls(); len(got) != 0 {
		t.Errorf("Capture called the home beyond AddTask: %v", got)
	}
	if strings.Contains(out.String(), "\x1b[?1049h") {
		t.Errorf("Capture output entered the alternate screen: %q", out.String())
	}
}
