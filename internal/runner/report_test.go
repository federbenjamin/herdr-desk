package runner_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/herdr/herdrtest"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/sidebar"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func TestStartReportsTheRunAndCoordinatorRows(t *testing.T) {
	f := newFixture(t, "", "self")
	h := newReportingHerdr(f.herdr)
	coordinator := reportCoordinator(t, f, h)
	task := f.armThread("show the start", "agent")

	run := f.startRun(f.runnerWith(h), task.Number)

	assertReports(t, h, []reportCall{
		{pane: run.Pane, source: sidebar.Source, name: sidebar.Token, value: "T1 running · since 15:00"},
		{pane: coordinator.Pane, source: sidebar.Source, name: sidebar.Token, value: "1 running"},
	})
}

func TestTrackReportsTheWorkerAndCoordinatorRowsAfterAnEvent(t *testing.T) {
	f := newFixture(t, "", "self")
	h := newReportingHerdr(f.herdr)
	coordinator := reportCoordinator(t, f, h)
	task := f.armThread("wait for an answer", "agent")
	r := f.runnerWith(h)
	run := f.startRun(r, task.Number)
	h.resetReports()
	h.Set(run.Pane, run.Session, "blocked")

	if err := r.Track(f.ctx, run.Pane); err != nil {
		t.Fatalf("Track: %v", err)
	}

	assertReports(t, h, []reportCall{
		{pane: run.Pane, source: sidebar.Source, name: sidebar.Token, value: "T1 needs you · blocked"},
		{pane: coordinator.Pane, source: sidebar.Source, name: sidebar.Token, value: "1 need you"},
	})
}

func TestAfterSetReportsTheCurrentRunAndCoordinatorRows(t *testing.T) {
	f := newFixture(t, "", "self")
	h := newReportingHerdr(f.herdr)
	coordinator := reportCoordinator(t, f, h)
	task := f.armThread("a person blocks it", "agent")
	r := f.runnerWith(h)
	run := f.startRun(r, task.Number)
	h.resetReports()
	blocked := model.StatusBlocked
	if _, err := f.store.SetTask(f.ctx, store.Actor{}, task.Number, model.Patch{Status: &blocked}); err != nil {
		t.Fatalf("block T%d: %v", task.Number, err)
	}

	r.AfterSet(f.ctx, task.Number)

	assertReports(t, h, []reportCall{
		{pane: run.Pane, source: sidebar.Source, name: sidebar.Token, value: "T1 running · since 15:00"},
		{pane: coordinator.Pane, source: sidebar.Source, name: sidebar.Token, value: "1 need you · 1 running"},
	})
}

func TestKillReportsOnlyTheCoordinatorAfterAWaitingRunEnds(t *testing.T) {
	f := newFixture(t, "", "self")
	f.config.Runner.Cap = 0
	h := newReportingHerdr(f.herdr)
	coordinator := reportCoordinator(t, f, h)
	task := f.armThread("cancel before spawn", "agent")
	r := f.runnerWith(h)
	if _, err := r.Start(f.ctx, store.Actor{}, task.Number, store.RunRoute{Root: f.root}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.resetReports()

	if _, err := r.Kill(f.ctx, store.Actor{}, task.Number); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	assertReports(t, h, []reportCall{
		{pane: coordinator.Pane, source: sidebar.Source, name: sidebar.Token, value: "1 need you"},
	})
}

func TestClosedPaneDoesNotReceiveARowWhenTrackEndsTheRun(t *testing.T) {
	f := newFixture(t, "", "self")
	h := newReportingHerdr(f.herdr)
	task := f.armThread("closed worker", "agent")
	r := f.runnerWith(h)
	run := f.startRun(r, task.Number)
	h.resetReports()
	h.Remove(run.Pane)

	if err := r.Track(f.ctx, run.Pane); err != nil {
		t.Fatalf("Track: %v", err)
	}

	assertReports(t, h, nil)
	if got := f.run(task.Number).State; got != model.RunEnded {
		t.Fatalf("run after closed pane = %q, want ended", got)
	}
	if got := f.task(task.Number).Task.Status; got != model.StatusReview {
		t.Fatalf("task after closed pane = %q, want review", got)
	}
}

func TestJobsDoesNotReportAnUnchangedRun(t *testing.T) {
	f := newFixture(t, "", "self")
	h := newReportingHerdr(f.herdr)
	task := f.armThread("keep working", "agent")
	r := f.runnerWith(h)
	f.startRun(r, task.Number)
	h.resetReports()

	r.Jobs(f.ctx)

	assertReports(t, h, nil)
	if got := f.run(task.Number).State; got != model.RunRunning {
		t.Fatalf("run after unchanged jobs tick = %q, want running", got)
	}
}

type reportCall struct {
	pane, source, name, value string
}

type reportingHerdr struct {
	*herdrtest.Herdr
	reports []reportCall
}

func newReportingHerdr(h *herdrtest.Herdr) *reportingHerdr {
	return &reportingHerdr{Herdr: h}
}

func (h *reportingHerdr) ReportToken(ctx context.Context, pane, source, name, value string) error {
	h.reports = append(h.reports, reportCall{pane: pane, source: source, name: name, value: value})
	return h.Herdr.ReportToken(ctx, pane, source, name, value)
}

func (h *reportingHerdr) resetReports() {
	h.reports = nil
}

func reportCoordinator(t *testing.T, f *fixture, h *reportingHerdr) model.Coordinator {
	t.Helper()
	created, err := h.CreateWorkspace(f.ctx, f.root, "test coordinator", nil)
	if err != nil {
		t.Fatalf("create coordinator workspace: %v", err)
	}
	c := model.Coordinator{Session: "coordinator-session", Workspace: created.Workspace, Pane: created.Pane}
	if err := f.store.SetCoordinator(f.ctx, store.Actor{}, c); err != nil {
		t.Fatalf("record coordinator: %v", err)
	}
	return c
}

func assertReports(t *testing.T, h *reportingHerdr, want []reportCall) {
	t.Helper()
	if !reflect.DeepEqual(h.reports, want) {
		t.Fatalf("reports = %#v, want %#v", h.reports, want)
	}
}
