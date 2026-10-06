package runner_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func TestW5StartLetsPeopleAndTheRecordedCoordinatorStart(t *testing.T) {
	for _, tt := range []struct {
		name  string
		actor store.Actor
		setup func(t *testing.T, f *fixture)
	}{
		{
			name:  "a person",
			actor: store.Actor{},
			setup: func(_ *testing.T, _ *fixture) {},
		},
		{
			name:  "the recorded coordinator",
			actor: store.Actor{Session: "coordinator-session"},
			setup: func(t *testing.T, f *fixture) {
				t.Helper()
				if err := f.store.SetCoordinator(f.ctx, store.Actor{}, model.Coordinator{Session: "coordinator-session"}); err != nil {
					t.Fatalf("set coordinator: %v", err)
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, "", "self")
			tt.setup(t, f)
			task := f.armThread("starts", "agent")
			run, err := f.runner().Start(f.ctx, tt.actor, task.Number, store.RunRoute{Root: f.root})
			if err != nil || run.State != model.RunRunning {
				t.Fatalf("Start() = (%#v, %v); want a running run", run, err)
			}
		})
	}
}

func TestW5StartRefusesALiveSessionBeforeItReadsTheTask(t *testing.T) {
	f := newFixture(t, "", "self")
	owner := f.armThread("owns a live run", "agent")
	live := f.startRun(f.runner(), owner.Number)

	for _, actor := range []store.Actor{
		{Session: live.Session},
		{Session: live.Session, Run: live.ID},
	} {
		_, err := f.runner().Start(f.ctx, actor, 9999, store.RunRoute{})
		if fireCode(err) != model.CodeNotAllowed {
			t.Fatalf("Start() with %#v error = %v; want not-allowed before unknown-task", actor, err)
		}
		if want := fmt.Sprintf("run %d of T%d", live.ID, owner.Number); !strings.Contains(err.Error(), want) {
			t.Fatalf("Start() live-session error = %v; want it to name %s", err, want)
		}
	}
}

func TestW5StartDefersOtherAgentsUntilAfterResolve(t *testing.T) {
	f := newFixture(t, "", "self")
	task := f.armThread("deferred agent", "agent")
	agent := store.Actor{Session: "other-agent"}

	_, err := f.runner().Start(f.ctx, agent, task.Number, store.RunRoute{Root: "/not-listed"})
	if fireCode(err) != model.CodeBadInput {
		t.Fatalf("Start() invalid route error = %v; want bad-input before the root permission refusal", err)
	}

	_, err = f.runner().Start(f.ctx, agent, task.Number, store.RunRoute{Root: f.root})
	if fireCode(err) != model.CodeNotAllowed {
		t.Fatalf("Start() agent on a root without agents_may_start error = %v; want not-allowed", err)
	}
}

func TestW5StartLetsOtherAgentsUseAnOptedInResolvedRoot(t *testing.T) {
	f := newFixture(t, "", "self")
	f.config.Roots[0].AgentsMayStart = true
	task := f.armThread("agent starts here", "agent")

	run, err := f.runner().Start(f.ctx, store.Actor{Session: "other-agent"}, task.Number, store.RunRoute{Root: f.root})
	if err != nil || run.State != model.RunRunning {
		t.Fatalf("Start() = (%#v, %v); want a running run from the opted-in root", run, err)
	}
}

func TestW5StartAppliesTheDailyCapToAnOptedInAgent(t *testing.T) {
	f := newFixture(t, "", "self")
	f.config.Roots[0].AgentsMayStart = true
	f.config.Runner.MaxRunsPerDay = 1
	personTask := f.armThread("uses the only daily start", "agent")
	f.startRun(f.runner(), personTask.Number)
	agentTask := f.armThread("agent cannot bypass the cap", "agent")

	_, err := f.runner().Start(f.ctx, store.Actor{Session: "other-agent"}, agentTask.Number, store.RunRoute{Root: f.root})
	if fireCode(err) != model.CodeCapReached {
		t.Fatalf("Start() error = %v; want cap-reached for an opted-in agent after the daily cap is spent", err)
	}
}
