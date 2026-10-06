package worker_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/worker"
)

const handBack = "When the work is finished, hand the task back: run `herdr-desk set T<n> review --ref <a file or PR that shows the work>`. Add `--merged` when that PR is merged. If you cannot finish, record what you need with `herdr-desk note --task T<n> \"<what you need>\"`, then run `herdr-desk set T<n> blocked`. Finish with review or blocked."

func TestFirstMessageRendersTheCompleteTaskInThePublishedShape(t *testing.T) {
	t.Parallel()

	ready := model.StatusReady
	started := model.StatusStarted
	blocked := model.StatusBlocked
	base := time.Date(2026, time.October, 4, 14, 2, 0, 0, time.UTC)
	detail := store.TaskDetail{
		Task: model.Task{
			Number: 7,
			Title:  "Ship the runner",
			Notes:  "Keep the audit trail.",
			Steps: []model.Step{
				{ShortID: "s1", Text: "write code"},
				{ShortID: "s2", Text: "review output", Done: true},
			},
		},
		History: []model.Event{
			{TS: base, Kind: model.KindTask, Data: model.MustData(model.TaskData{Title: "Ship the runner"})},
			{TS: base.Add(time.Minute), Kind: model.KindSet, Data: model.MustData(model.Patch{Status: &ready})},
			{TS: base.Add(time.Minute), Kind: model.KindSet, Run: 91, Data: model.MustData(model.Patch{Status: &started})},
			{TS: base.Add(18 * time.Minute), Who: model.WhoAgent, Kind: model.KindNote, Data: model.MustData(model.NoteData{Text: "implementation is ready", Ref: "https://example.test/pr/7"})},
			{TS: base.Add(19 * time.Minute), Who: model.WhoAgent, Kind: model.KindSet, Data: model.MustData(model.Patch{Status: &blocked, Ref: "docs/blocker.md"})},
			{TS: base.Add(28 * time.Minute), Kind: model.KindDecision, Data: model.MustData(model.DecisionData{Text: "ship after review"})},
		},
	}

	want := "You are working on desk task T7: Ship the runner\n\n" +
		"Keep the audit trail.\n\n" +
		"Steps:\n" +
		"- [ ] s1 write code\n" +
		"- [x] s2 review output\n\n" +
		"History (oldest first):\n" +
		"- 2026-10-04 14:02Z user: created\n" +
		"- 2026-10-04 14:03Z user: status ready\n" +
		"- 2026-10-04 14:03Z runner: status started\n" +
		"- 2026-10-04 14:20Z agent: note: implementation is ready (https://example.test/pr/7)\n" +
		"- 2026-10-04 14:21Z agent: status blocked (docs/blocker.md)\n" +
		"- 2026-10-04 14:30Z user: decided: ship after review\n\n" +
		"When the work is finished, hand the task back: run `herdr-desk set T7 review --ref <a file or PR that shows the work>`. Add `--merged` when that PR is merged. If you cannot finish, record what you need with `herdr-desk note --task T7 \"<what you need>\"`, then run `herdr-desk set T7 blocked`. Finish with review or blocked.\n"
	if got := worker.FirstMessage(detail, true); got != want {
		t.Errorf("FirstMessage() =\n%s\nwant\n%s", got, want)
	}
}

func TestFirstMessageLeavesOutEmptySectionsAndTheirBlankLines(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		detail store.TaskDetail
		want   string
	}{
		{
			name:   "notes",
			detail: store.TaskDetail{Task: model.Task{Number: 8, Title: "No notes"}},
			want:   "You are working on desk task T8: No notes\n\n" + strings.ReplaceAll(handBack, "T<n>", "T8") + "\n",
		},
		{
			name: "steps",
			detail: store.TaskDetail{Task: model.Task{Number: 9, Title: "No steps", Notes: "Only a note."}, History: []model.Event{
				{TS: time.Date(2026, time.October, 4, 14, 2, 0, 0, time.UTC), Kind: model.KindTask, Data: model.MustData(model.TaskData{Title: "No steps"})},
			}},
			want: "You are working on desk task T9: No steps\n\nOnly a note.\n\nHistory (oldest first):\n" +
				"- 2026-10-04 14:02Z user: created\n\n" + strings.ReplaceAll(handBack, "T<n>", "T9") + "\n",
		},
		{
			name: "history",
			detail: store.TaskDetail{Task: model.Task{Number: 10, Title: "No history", Notes: "Has steps.", Steps: []model.Step{
				{ShortID: "s1", Text: "do it"},
			}}},
			want: "You are working on desk task T10: No history\n\nHas steps.\n\nSteps:\n" +
				"- [ ] s1 do it\n\n" + strings.ReplaceAll(handBack, "T<n>", "T10") + "\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := worker.FirstMessage(tc.detail, true); got != tc.want {
				t.Errorf("FirstMessage() =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

func TestFirstMessageOmitsReferencesThatAreNotPresent(t *testing.T) {
	t.Parallel()

	detail := store.TaskDetail{Task: model.Task{Number: 11, Title: "No ref"}, History: []model.Event{
		{TS: time.Date(2026, time.October, 4, 14, 2, 0, 0, time.UTC), Who: model.WhoAgent, Kind: model.KindNote, Data: model.MustData(model.NoteData{Text: "plain note"})},
	}}
	want := "You are working on desk task T11: No ref\n\nHistory (oldest first):\n" +
		"- 2026-10-04 14:02Z agent: note: plain note\n\n" + strings.ReplaceAll(handBack, "T<n>", "T11") + "\n"
	if got := worker.FirstMessage(detail, true); got != want {
		t.Errorf("FirstMessage() =\n%s\nwant\n%s", got, want)
	}
}

func TestFirstMessageNamesChangedFieldsWhenASetDoesNotChangeStatus(t *testing.T) {
	t.Parallel()

	title := "new title"
	root := "/work/desk"
	detail := store.TaskDetail{Task: model.Task{Number: 12, Title: "Changed fields"}, History: []model.Event{
		{TS: time.Date(2026, time.October, 4, 14, 2, 0, 0, time.UTC), Kind: model.KindSet, Data: model.MustData(model.Patch{Title: &title, Root: &root})},
	}}
	want := "You are working on desk task T12: Changed fields\n\nHistory (oldest first):\n" +
		"- 2026-10-04 14:02Z user: changed title, root\n\n" + strings.ReplaceAll(handBack, "T<n>", "T12") + "\n"
	if got := worker.FirstMessage(detail, true); got != want {
		t.Errorf("FirstMessage() =\n%s\nwant\n%s", got, want)
	}
}

func TestFirstMessageLeavesOutRouterAndStepEvents(t *testing.T) {
	t.Parallel()

	detail := store.TaskDetail{Task: model.Task{Number: 13, Title: "Visible history"}, History: []model.Event{
		{TS: time.Date(2026, time.October, 4, 14, 2, 0, 0, time.UTC), Kind: model.KindNote, Tags: []string{"router"}, Data: model.MustData(model.NoteData{Text: "router picked worktree"})},
		{TS: time.Date(2026, time.October, 4, 14, 3, 0, 0, time.UTC), Kind: model.KindStep, Data: model.MustData(model.StepOp{Op: "add", Text: "hidden step"})},
		{TS: time.Date(2026, time.October, 4, 14, 4, 0, 0, time.UTC), Kind: model.KindNote, Data: model.MustData(model.NoteData{Text: "visible note"})},
	}}
	want := "You are working on desk task T13: Visible history\n\nHistory (oldest first):\n" +
		"- 2026-10-04 14:04Z user: note: visible note\n\n" + strings.ReplaceAll(handBack, "T<n>", "T13") + "\n"
	if got := worker.FirstMessage(detail, true); got != want {
		t.Errorf("FirstMessage() =\n%s\nwant\n%s", got, want)
	}
}

func TestFirstMessageDropsOldestHistoryLinesBeforeExceeding64KiB(t *testing.T) {
	t.Parallel()

	history := make([]model.Event, 0, 100)
	base := time.Date(2026, time.October, 4, 0, 0, 0, 0, time.UTC)
	for i := range 100 {
		history = append(history, model.Event{
			TS:   base.Add(time.Duration(i) * time.Minute),
			Kind: model.KindNote,
			Data: model.MustData(model.NoteData{Text: fmt.Sprintf("event-%03d %s", i, strings.Repeat("x", 800))}),
		})
	}

	got := worker.FirstMessage(store.TaskDetail{Task: model.Task{Number: 14, Title: "Long history"}, History: history}, true)
	if len(got) > 64*1024 {
		t.Fatalf("FirstMessage() length = %d, want at most %d", len(got), 64*1024)
	}
	if strings.Contains(got, "event-000") {
		t.Error("FirstMessage() kept the oldest history line after reaching the cap")
	}
	if !strings.Contains(got, "event-099") {
		t.Error("FirstMessage() dropped the newest history line before older ones")
	}
	if !strings.Contains(got, strings.ReplaceAll(handBack, "T<n>", "T14")) {
		t.Error("FirstMessage() dropped the hand-back paragraph while shortening history")
	}
	var droppedLine string
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "older history lines left out") {
			droppedLine = line
			break
		}
	}
	if droppedLine == "" {
		t.Error("FirstMessage() did not say how many history lines it left out")
	} else if want := fmt.Sprintf("%d", len(history)-strings.Count(got, "event-")); !strings.Contains(droppedLine, want) {
		t.Errorf("FirstMessage() drop line = %q, want it to name %s dropped lines", droppedLine, want)
	}
}

func TestFirstMessageCarriesTemplateLookingTextWithoutExpansion(t *testing.T) {
	t.Parallel()

	detail := store.TaskDetail{Task: model.Task{Number: 15, Title: "Use {session} --model", Notes: "Keep {model} --flag unchanged."}}
	want := "You are working on desk task T15: Use {session} --model\n\n" +
		"Keep {model} --flag unchanged.\n\n" + strings.ReplaceAll(handBack, "T<n>", "T15") + "\n"
	if got := worker.FirstMessage(detail, true); got != want {
		t.Errorf("FirstMessage() =\n%s\nwant\n%s", got, want)
	}
}

func TestFirstMessageRendersRefOnlyChangesAndMergedEvents(t *testing.T) {
	t.Parallel()

	ref := "https://example.test/pr/16"
	detail := store.TaskDetail{Task: model.Task{Number: 16, Title: "Merge it"}, History: []model.Event{
		{TS: time.Date(2026, time.October, 4, 14, 2, 0, 0, time.UTC), Kind: model.KindSet, Data: model.MustData(model.Patch{Ref: ref})},
		{TS: time.Date(2026, time.October, 4, 14, 3, 0, 0, time.UTC), Kind: model.KindMerged, Data: model.MustData(model.MergedData{Branch: "feature/merge-it"})},
	}}
	want := "You are working on desk task T16: Merge it\n\nHistory (oldest first):\n" +
		"- 2026-10-04 14:02Z user: changed (https://example.test/pr/16)\n" +
		"- 2026-10-04 14:03Z user: merged feature/merge-it\n\n" + strings.ReplaceAll(handBack, "T<n>", "T16") + "\n"
	if got := worker.FirstMessage(detail, true); got != want {
		t.Errorf("FirstMessage() =\n%s\nwant\n%s", got, want)
	}
}
