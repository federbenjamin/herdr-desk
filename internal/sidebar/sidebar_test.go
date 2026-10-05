package sidebar_test

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/sidebar"
)

func TestRunTextShowsTheApprovedPaneRows(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, time.October, 4, 18, 5, 0, 0, time.UTC)
	cases := []struct {
		name string
		task model.Task
		run  model.Run
		ref  string
		want string
	}{
		{"running uses the local start time", model.Task{Number: 21, Status: model.StatusStarted}, model.Run{State: model.RunRunning, StartedTS: started}, "", "T21 running · since 14:05"},
		{"review translates a pull URL", model.Task{Number: 22, Status: model.StatusReview}, model.Run{State: model.RunEnded}, "https://github.com/federbenjamin/herdr-desk/pull/12/files", "T22 review · PR #12"},
		{"blocked asks for a person", model.Task{Number: 23, Status: model.StatusBlocked}, model.Run{State: model.RunEnded}, "", "T23 needs you · blocked"},
		{"idle review says why", model.Task{Number: 24, Status: model.StatusReview}, model.Run{State: model.RunIdle}, "", "T24 review · went idle"},
		{"done keeps the open pane identifiable", model.Task{Number: 22, Status: model.StatusDone}, model.Run{State: model.RunEnded}, "", "T22 done"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sidebar.RunText(tc.task, tc.run, true, tc.ref, newYork); got != tc.want {
				t.Fatalf("RunText() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRunTextOmitsPanesThatCannotShowARow(t *testing.T) {
	task := model.Task{Number: 7, Status: model.StatusStarted}
	for _, tc := range []struct {
		name    string
		current bool
		state   string
	}{
		{"a stale run", false, model.RunRunning},
		{"a starting run", true, model.RunStarting},
		{"a waiting run", true, model.RunWaiting},
		{"a failed run", true, model.RunFailed},
		{"a killed run", true, model.RunKilled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sidebar.RunText(task, model.Run{State: tc.state}, tc.current, "", time.UTC); got != "" {
				t.Fatalf("RunText() = %q, want an empty row", got)
			}
		})
	}
}

func TestRunTextKeepsTheTaskAndStateWhenTheDetailIsTooLong(t *testing.T) {
	task := model.Task{Number: 123456789, Status: model.StatusBlocked}
	got := sidebar.RunText(task, model.Run{State: model.RunEnded}, true, "", time.UTC)
	if !strings.HasPrefix(got, "T123456789 needs you") {
		t.Fatalf("RunText() = %q, want the complete task id and state", got)
	}
	if n := utf8.RuneCountInString(got); n > sidebar.MaxWidth {
		t.Fatalf("RunText() has %d runes, want at most %d: %q", n, sidebar.MaxWidth, got)
	}
}

func TestCoordinatorTextShowsTheApprovedSummaryRows(t *testing.T) {
	cases := []struct {
		needYou int
		running int
		waiting int
		want    string
	}{
		{needYou: 2, running: 2, want: "2 need you · 2 running"},
		{running: 2, want: "2 running"},
		{running: 2, waiting: 1, want: "2 running · 1 waiting"},
		{want: "idle"},
	}
	for _, tc := range cases {
		if got := sidebar.CoordinatorText(tc.needYou, tc.running, tc.waiting); got != tc.want {
			t.Errorf("CoordinatorText(%d, %d, %d) = %q, want %q", tc.needYou, tc.running, tc.waiting, got, tc.want)
		}
	}
}
