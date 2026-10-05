package board_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/federbenjamin/herdr-desk/internal/board"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// startHome is a Home that answers only StartRun; any other call panics on the nil embedded Home.
type startHome struct {
	board.Home
	err    error
	actors []store.Actor
	tasks  []int
	routes []store.RunRoute
}

func (h *startHome) StartRun(_ context.Context, a store.Actor, task int, route store.RunRoute) (model.Run, error) {
	h.actors = append(h.actors, a)
	h.tasks = append(h.tasks, task)
	h.routes = append(h.routes, route)
	return model.Run{Task: task}, h.err
}

func TestStartKeyOnTheBoardAsksForARunOfTheSelectedTaskOnlyOnce(t *testing.T) {
	s := w2State(w2Task(1, model.StatusOpen), w2Task(2, model.StatusReady))
	s, _ = s.Update(press('j'))
	_, effects := s.Update(press('S'))
	wantEffects(t, effects, []board.Effect{board.StartRun{Task: 1}})
}

func TestStartKeyOnTheTaskPageAsksForARunOfThatTask(t *testing.T) {
	task := w2Task(5, model.StatusReady)
	s := w2State(task)
	s, _ = s.Update(named(tea.KeyEnter))
	s, _ = s.Update(board.TaskLoaded{Detail: store.TaskDetail{Task: task}})
	_, effects := s.Update(press('S'))
	wantEffects(t, effects, []board.Effect{board.StartRun{Task: 5}})
}

// Starting is not a status write: S on an open task must not turn into a ready/started patch.
func TestStartKeyWritesNoStatusPatchWhateverTheTaskStatus(t *testing.T) {
	for _, st := range []model.Status{model.StatusOpen, model.StatusReady, model.StatusStarted, model.StatusBlocked, model.StatusReview} {
		s := w2State(w2Task(3, st))
		_, effects := s.Update(press('S'))
		if !reflect.DeepEqual(effects, []board.Effect{board.StartRun{Task: 3}}) {
			t.Errorf("S on a %s task: effects = %#v, want only StartRun{3}", st, effects)
		}
	}
}

func TestStartKeyWithNothingSelectedAsksForNothing(t *testing.T) {
	s := w2State()
	_, effects := s.Update(press('S'))
	wantEffects(t, effects, nil)
}

func TestStartKeyOfflineNeedsTheHomeAndAsksForNothing(t *testing.T) {
	s := w2State()
	s, _ = s.Update(board.Loaded{Data: board.Data{Offline: true, Tasks: []model.Task{w2Task(1, model.StatusReady)}}})
	s, effects := s.Update(press('S'))
	wantEffects(t, effects, nil)
	if !strings.Contains(s.Text(), "offline: S needs the home") {
		t.Fatalf("screen = %q, want the offline refusal for S", s.Text())
	}
}

func TestStartKeyExecutorCallsStartRunAsAPersonWithNoRouteAndAnswersNoFailureOnSuccess(t *testing.T) {
	h := &startHome{}
	msg := board.Answer(h, board.StartRun{Task: 9})
	if f, ok := msg.(board.Failed); ok {
		t.Fatalf("answer = %#v, want a success", f)
	}
	if !reflect.DeepEqual(h.tasks, []int{9}) || !reflect.DeepEqual(h.routes, []store.RunRoute{{}}) || len(h.actors) != 1 || h.actors[0].Session != "" {
		t.Fatalf("StartRun saw tasks %v, routes %#v, actors %#v; want T9, an empty route, a person", h.tasks, h.routes, h.actors)
	}
}

// A refusal such as cap-reached must reach the status line, not vanish.
func TestStartKeyRefusalShowsOnTheStatusLine(t *testing.T) {
	h := &startHome{err: &model.Refusal{Code: model.CodeCapReached, Msg: "20 runs started today, runner.max_runs_per_day is 20"}}
	s := w2State(w2Task(1, model.StatusReady))
	_, effects := s.Update(press('S'))
	if len(effects) != 1 {
		t.Fatalf("effects = %#v, want one", effects)
	}
	s, _ = board.Feed(s, board.Answer(h, effects[0]))
	if !strings.Contains(s.Text(), "max_runs_per_day") {
		t.Fatalf("screen after a refused start = %q, want the refusal's text", s.Text())
	}
	if _, ok := board.Answer(&startHome{err: errors.New("x")}, board.StartRun{Task: 1}).(board.Failed); !ok {
		t.Fatalf("a failed StartRun must answer Failed")
	}
}

// Nothing starts a ready task by itself any more, so the row must not claim it is queued.
func TestAgentThreadRowCarriesNoQueuedTag(t *testing.T) {
	task := w2Task(4, model.StatusReady)
	task.Thread = "agent"
	s := w2State(task)
	text := s.Text()
	if strings.Contains(text, "queued") {
		t.Fatalf("screen = %q, want no queued tag", text)
	}
	if !strings.Contains(text, "#agent") {
		t.Fatalf("screen = %q, want the thread shown as #agent", text)
	}
}

func TestFootersAndKeyHelpNameTheSKey(t *testing.T) {
	s := w2State(w2Task(1, model.StatusReady))
	if !strings.Contains(s.Text(), "S run") {
		t.Fatalf("board footer = %q, want S run", s.Text())
	}
	s, _ = s.Update(press('?'))
	if !strings.Contains(s.Text(), "start a run of the task") {
		t.Fatalf("keys help = %q, want the S line", s.Text())
	}
}
