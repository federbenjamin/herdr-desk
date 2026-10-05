package runner_test

import (
	"reflect"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/herdr"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/runner"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func TestOutcomeAppliesEveryPaneStateRow(t *testing.T) {
	const (
		runSession   = "run-session"
		otherSession = "other-session"
		paneID       = "pane-42"
	)

	handBack := func(from, to string, status, ifStatus model.Status, note string) store.HandBack {
		return store.HandBack{
			From:     from,
			To:       to,
			Status:   status,
			IfStatus: ifStatus,
			Tags:     []string{model.TagRunner},
			Note:     note,
		}
	}

	cases := []struct {
		name      string
		run       model.Run
		pane      herdr.Pane
		found     bool
		wrote     bool
		want      store.HandBack
		wantWrite bool
	}{
		{
			name:  "a gone running pane ends the run without replacing a concurrent task status",
			run:   model.Run{State: model.RunRunning, Session: runSession},
			found: false,
			wrote: true,
			want: handBack(model.RunRunning, model.RunEnded, model.StatusReview, model.StatusStarted,
				"the pane closed without a hand-back"),
			wantWrite: true,
		},
		{
			name:  "a gone idle pane records that its worker never wrote",
			run:   model.Run{State: model.RunIdle, Session: runSession},
			found: false,
			wrote: false,
			want: handBack(model.RunIdle, model.RunEnded, model.StatusReview, model.StatusStarted,
				"the pane closed without a hand-back, before the worker wrote anything"),
			wantWrite: true,
		},
		{
			name:  "a blocked pane owned by the run makes the run idle",
			run:   model.Run{State: model.RunRunning, Session: runSession},
			pane:  herdr.Pane{ID: paneID, Session: runSession, Status: "blocked"},
			found: true,
			want: handBack(model.RunRunning, model.RunIdle, model.StatusBlocked, "",
				"the worker is waiting for an answer in pane "+paneID),
			wantWrite: true,
		},
		{
			name:  "a blocked pane before its session begins makes the run idle",
			run:   model.Run{State: model.RunRunning, Session: runSession},
			pane:  herdr.Pane{ID: paneID, Status: "blocked"},
			found: true,
			want: handBack(model.RunRunning, model.RunIdle, model.StatusBlocked, "",
				"the worker is waiting for an answer in pane "+paneID),
			wantWrite: true,
		},
		{
			name:      "a repeated blocked event on an idle run writes nothing",
			run:       model.Run{State: model.RunIdle, Session: runSession},
			pane:      herdr.Pane{ID: paneID, Session: runSession, Status: "blocked"},
			found:     true,
			wantWrite: false,
		},
		{
			name:  "an owned idle pane asks a running run to review",
			run:   model.Run{State: model.RunRunning, Session: runSession},
			pane:  herdr.Pane{ID: paneID, Session: runSession, Status: "idle"},
			found: true,
			want: handBack(model.RunRunning, model.RunIdle, model.StatusReview, "",
				"went idle without handing back"),
			wantWrite: true,
		},
		{
			name:  "an owned done pane asks a running run to review",
			run:   model.Run{State: model.RunRunning, Session: runSession},
			pane:  herdr.Pane{ID: paneID, Session: runSession, Status: "done"},
			found: true,
			want: handBack(model.RunRunning, model.RunIdle, model.StatusReview, "",
				"went idle without handing back"),
			wantWrite: true,
		},
		{
			name:      "a repeated idle event on an idle run writes nothing",
			run:       model.Run{State: model.RunIdle, Session: runSession},
			pane:      herdr.Pane{ID: paneID, Session: runSession, Status: "idle"},
			found:     true,
			wantWrite: false,
		},
		{
			name:      "a repeated done event on an idle run writes nothing",
			run:       model.Run{State: model.RunIdle, Session: runSession},
			pane:      herdr.Pane{ID: paneID, Session: runSession, Status: "done"},
			found:     true,
			wantWrite: false,
		},
		{
			name:      "a working pane owned by a resumed idle run marks it started",
			run:       model.Run{State: model.RunIdle, Session: runSession},
			pane:      herdr.Pane{ID: paneID, Session: runSession, Status: "working"},
			found:     true,
			want:      handBack(model.RunIdle, model.RunRunning, model.StatusStarted, "", ""),
			wantWrite: true,
		},
		{
			name:      "a working pane cannot restart an already running run",
			run:       model.Run{State: model.RunRunning, Session: runSession},
			pane:      herdr.Pane{ID: paneID, Session: runSession, Status: "working"},
			found:     true,
			wantWrite: false,
		},
		{
			name:      "a pane from another session writes nothing",
			run:       model.Run{State: model.RunRunning, Session: runSession},
			pane:      herdr.Pane{ID: paneID, Session: otherSession, Status: "blocked"},
			found:     true,
			wantWrite: false,
		},
		{
			name:      "an unstarted session with an idle status writes nothing",
			run:       model.Run{State: model.RunRunning, Session: runSession},
			pane:      herdr.Pane{ID: paneID, Status: "idle"},
			found:     true,
			wantWrite: false,
		},
		{
			name:      "a non-trackable run writes nothing",
			run:       model.Run{State: model.RunWaiting, Session: runSession},
			pane:      herdr.Pane{ID: paneID, Session: runSession, Status: "blocked"},
			found:     true,
			wantWrite: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, gotWrite := runner.Outcome(tc.run, tc.pane, tc.found, tc.wrote)
			if gotWrite != tc.wantWrite || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Outcome(%#v, %#v, %t, %t) = (%#v, %t), want (%#v, %t)", tc.run, tc.pane, tc.found, tc.wrote, got, gotWrite, tc.want, tc.wantWrite)
			}
		})
	}
}
