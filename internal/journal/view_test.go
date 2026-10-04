package journal_test

import (
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/desk/internal/journal"
	"github.com/federbenjamin/desk/internal/model"
)

var journalTime = time.Date(2026, time.September, 28, 14, 0, 0, 0, time.UTC)

func journalEvent(id int64, kind model.Kind, data any, tags ...string) model.Event {
	return model.Event{
		ID:      id,
		TS:      journalTime.Add(time.Duration(id) * time.Minute),
		Session: "sid",
		Who:     model.WhoAgent,
		Kind:    kind,
		Data:    model.MustData(data),
		Tags:    tags,
	}
}

func journalTask(number int, title string, status model.Status, doneAt int64, tags ...string) model.SessionTask {
	return model.SessionTask{
		Task:   model.Task{Number: number, Title: title, Status: status},
		DoneAt: doneAt,
		Tags:   tags,
	}
}

func eventIDs(lines []journal.Line) []int64 {
	ids := make([]int64, len(lines))
	for i, line := range lines {
		ids[i] = line.EventID
	}
	return ids
}

func hasEvent(lines []journal.Line, id int64) bool {
	for _, line := range lines {
		if line.EventID == id {
			return true
		}
	}
	return false
}

func lineForEvent(t *testing.T, lines []journal.Line, id int64) journal.Line {
	t.Helper()
	for _, line := range lines {
		if line.EventID == id {
			return line
		}
	}
	t.Fatalf("event e%d is absent", id)
	return journal.Line{}
}

func hasTask(lines []journal.Line, number int) bool {
	for _, line := range lines {
		if line.Task == number {
			return true
		}
	}
	return false
}

func TestBuildHidesMergedBranchNotesAndDoneTasksOnlyOnThatBranch(t *testing.T) {
	data := model.SessionData{
		Session: "sid",
		Events: []model.Event{
			journalEvent(1, model.KindNote, model.NoteData{Text: "finished x"}, model.BranchTag("quick/x")),
			journalEvent(3, model.KindNote, model.NoteData{Text: "still working y"}, model.BranchTag("quick/y")),
			journalEvent(5, model.KindMerged, model.MergedData{Branch: "quick/x", PR: 7, SHA: "abc"}),
		},
		Tasks: []model.SessionTask{
			journalTask(1, "done on x", model.StatusDone, 2, model.BranchTag("quick/x")),
			journalTask(2, "open on x", model.StatusOpen, 0, model.BranchTag("quick/x")),
			journalTask(3, "done on y", model.StatusDone, 4, model.BranchTag("quick/y")),
		},
	}

	view := journal.Build(data, false)
	if hasEvent(view.Work, 1) || hasTask(view.Todo, 1) {
		t.Fatalf("merged branch history remained visible: work=%v todo=%v", eventIDs(view.Work), view.Todo)
	}
	if !hasEvent(view.Work, 3) || !hasTask(view.Todo, 2) || !hasTask(view.Todo, 3) {
		t.Fatalf("another branch or open task was hidden: work=%v todo=%v", eventIDs(view.Work), view.Todo)
	}
}

func TestBuildKeepsQuestionNotesAndOpenQuestionTasksButHidesDoneQuestionTaskAfterMerge(t *testing.T) {
	data := model.SessionData{
		Session: "sid",
		Events: []model.Event{
			journalEvent(1, model.KindNote, model.NoteData{Text: "needs an answer"}, model.BranchTag("quick/x"), "question"),
			journalEvent(4, model.KindMerged, model.MergedData{Branch: "quick/x"}),
		},
		Tasks: []model.SessionTask{
			journalTask(1, "open question", model.StatusOpen, 0, model.BranchTag("quick/x"), "question"),
			journalTask(2, "answered question", model.StatusDone, 3, model.BranchTag("quick/x"), "question"),
		},
	}

	view := journal.Build(data, false)
	if !hasEvent(view.Work, 1) || !hasTask(view.Todo, 1) {
		t.Fatalf("a question that must remain visible was hidden: work=%v todo=%v", eventIDs(view.Work), view.Todo)
	}
	if hasTask(view.Todo, 2) {
		t.Fatalf("done question task remained after its branch merged: todo=%v", view.Todo)
	}
}

func TestBuildKeepsLastTunableDecisionAndHidesSupersededOneAfterMerge(t *testing.T) {
	data := model.SessionData{
		Session: "sid",
		Events: []model.Event{
			journalEvent(1, model.KindDecision, model.DecisionData{Text: "use five workers"}, "tunable:workers"),
			journalEvent(2, model.KindDecision, model.DecisionData{Text: "use six workers", Replaces: 1}, "tunable:workers"),
			journalEvent(3, model.KindMerged, model.MergedData{Branch: "quick/x"}),
		},
	}

	view := journal.Build(data, false)
	if hasEvent(view.Decisions, 1) || !hasEvent(view.Decisions, 2) {
		t.Fatalf("tunable decisions were pruned incorrectly: %v", eventIDs(view.Decisions))
	}

	all := journal.Build(data, true)
	if !lineForEvent(t, all.Decisions, 1).Hidden || lineForEvent(t, all.Decisions, 2).Hidden {
		t.Fatalf("all view did not mark only the superseded tunable decision hidden: %#v", all.Decisions)
	}
}

func TestBuildKeepsBranchTaskUntilItIsDoneAndItsOwnBranchMerges(t *testing.T) {
	open := model.SessionData{
		Session: "sid",
		Events:  []model.Event{journalEvent(3, model.KindMerged, model.MergedData{Branch: "quick/y"})},
		Tasks:   []model.SessionTask{journalTask(1, "ship x", model.StatusOpen, 0, model.BranchTag("quick/x"))},
	}
	if !hasTask(journal.Build(open, false).Todo, 1) {
		t.Fatal("open task was hidden by another branch's merge")
	}

	done := open
	done.Tasks = []model.SessionTask{journalTask(1, "ship x", model.StatusDone, 2, model.BranchTag("quick/x"))}
	done.Events = append(done.Events, journalEvent(4, model.KindMerged, model.MergedData{Branch: "quick/x"}))
	if hasTask(journal.Build(done, false).Todo, 1) {
		t.Fatal("done task remained after its own branch merged")
	}
}

func TestBuildShowsMergedLineOnceAndNeverHidesIt(t *testing.T) {
	data := model.SessionData{Session: "sid", Events: []model.Event{
		journalEvent(1, model.KindNote, model.NoteData{Text: "built"}, model.BranchTag("quick/x")),
		journalEvent(2, model.KindMerged, model.MergedData{Branch: "quick/x", PR: 42, SHA: "deadbeef"}),
	}}

	for _, all := range []bool{false, true} {
		view := journal.Build(data, all)
		count := 0
		for _, line := range view.Work {
			if line.EventID == 2 {
				count++
				if line.Hidden {
					t.Fatalf("all=%t: merged line was hidden", all)
				}
			}
		}
		if count != 1 {
			t.Fatalf("all=%t: merged line appeared %d times", all, count)
		}
	}
}

func TestBuildShowsEarlierSessionMergeOnlyWhenThatBranchHasChainHistory(t *testing.T) {
	externalMerge := journalEvent(2, model.KindMerged, model.MergedData{Branch: "quick/x"})
	externalMerge.Session = "earlier"

	withoutHistory := journal.Build(model.SessionData{Session: "sid", Events: []model.Event{externalMerge}}, false)
	if hasEvent(withoutHistory.Work, 2) {
		t.Fatal("merge from an earlier session appeared without any chain history on its branch")
	}

	withHistory := journal.Build(model.SessionData{Session: "sid", Events: []model.Event{
		journalEvent(1, model.KindNote, model.NoteData{Text: "branch work"}, model.BranchTag("quick/x")),
		externalMerge,
	}}, false)
	if !hasEvent(withHistory.Work, 2) {
		t.Fatal("merge from an earlier session was hidden despite chain history on its branch")
	}
}

func TestBuildShowsMergeWrittenByAContinuedSessionWithoutBranchWork(t *testing.T) {
	merge := journalEvent(2, model.KindMerged, model.MergedData{Branch: "quick/x"})
	merge.Session = "earlier"

	view := journal.Build(model.SessionData{
		Session: "sid",
		Chain:   []string{"earlier"},
		Events:  []model.Event{merge},
	}, false)
	if !hasEvent(view.Work, 2) {
		t.Fatal("merge from a continued session was hidden without branch work")
	}
}

func TestBuildAllIncludesHiddenLinesInTheirOwnSectionsAndOrder(t *testing.T) {
	data := model.SessionData{
		Session: "sid",
		Events: []model.Event{
			journalEvent(1, model.KindNote, model.NoteData{Text: "old branch work"}, model.BranchTag("quick/x")),
			journalEvent(2, model.KindDecision, model.DecisionData{Text: "old decision"}),
			journalEvent(3, model.KindDecision, model.DecisionData{Text: "new decision", Replaces: 2}),
			journalEvent(4, model.KindMerged, model.MergedData{Branch: "quick/x"}),
		},
		Tasks: []model.SessionTask{journalTask(1, "old branch task", model.StatusDone, 1, model.BranchTag("quick/x"))},
	}

	view := journal.Build(data, true)
	if got := eventIDs(view.Work); len(got) != 2 || got[0] != 1 || got[1] != 4 {
		t.Fatalf("work log did not retain event order: %v", got)
	}
	if !lineForEvent(t, view.Work, 1).Hidden || !view.Todo[0].Hidden || !lineForEvent(t, view.Decisions, 2).Hidden {
		t.Fatalf("hidden lines were not retained in their sections: %#v", view)
	}
}

func TestBuildHidesSessionNotesOnlyAfterTwoLaterCompactions(t *testing.T) {
	data := model.SessionData{Session: "sid", Events: []model.Event{
		journalEvent(1, model.KindNote, model.NoteData{Text: "old session note"}),
		journalEvent(2, model.KindCompacted, struct{}{}),
		journalEvent(3, model.KindNote, model.NoteData{Text: "recent session note"}),
		journalEvent(4, model.KindCompacted, struct{}{}),
		journalEvent(5, model.KindNote, model.NoteData{Text: "branch note"}, model.BranchTag("quick/x")),
	}}

	view := journal.Build(data, false)
	if hasEvent(view.Work, 1) {
		t.Fatal("session note remained after two later compactions")
	}
	if !hasEvent(view.Work, 3) || !hasEvent(view.Work, 5) {
		t.Fatalf("recent or branch note was hidden: %v", eventIDs(view.Work))
	}
}

func TestBuildHidesOlderCompactionMarkerButKeepsNewestTwo(t *testing.T) {
	data := model.SessionData{Session: "sid", Events: []model.Event{
		journalEvent(1, model.KindCompacted, struct{}{}),
		journalEvent(2, model.KindCompacted, struct{}{}),
		journalEvent(3, model.KindCompacted, struct{}{}),
	}}

	view := journal.Build(data, false)
	if hasEvent(view.Work, 1) || !hasEvent(view.Work, 2) || !hasEvent(view.Work, 3) {
		t.Fatalf("compaction markers were pruned incorrectly: %v", eventIDs(view.Work))
	}
}

func TestBuildFirstCompactionHidesDoneSessionTasksButNotSessionNotesOrDecisions(t *testing.T) {
	data := model.SessionData{
		Session: "sid",
		Events: []model.Event{
			journalEvent(1, model.KindNote, model.NoteData{Text: "first fact"}),
			journalEvent(4, model.KindDecision, model.DecisionData{Text: "keep decision"}),
			journalEvent(5, model.KindCompacted, struct{}{}),
		},
		Tasks: []model.SessionTask{
			journalTask(1, "done session task", model.StatusDone, 2),
			journalTask(2, "answered question", model.StatusDone, 3, "question"),
			journalTask(3, "open task", model.StatusOpen, 0),
			journalTask(4, "open question", model.StatusOpen, 0, "question"),
		},
	}

	view := journal.Build(data, false)
	if !hasEvent(view.Work, 1) || !hasEvent(view.Decisions, 4) {
		t.Fatalf("first compaction hid a session note or decision: work=%v decisions=%v", eventIDs(view.Work), eventIDs(view.Decisions))
	}
	if hasTask(view.Todo, 1) || hasTask(view.Todo, 2) || !hasTask(view.Todo, 3) || !hasTask(view.Todo, 4) {
		t.Fatalf("first compaction pruned tasks incorrectly: %#v", view.Todo)
	}
}

func TestBuildHidesReplacedDecisionAfterCompaction(t *testing.T) {
	data := model.SessionData{Session: "sid", Events: []model.Event{
		journalEvent(1, model.KindDecision, model.DecisionData{Text: "old decision"}),
		journalEvent(2, model.KindDecision, model.DecisionData{Text: "replacement", Replaces: 1}),
		journalEvent(3, model.KindCompacted, struct{}{}),
	}}

	view := journal.Build(data, false)
	if hasEvent(view.Decisions, 1) || !hasEvent(view.Decisions, 2) {
		t.Fatalf("replacement did not control decision visibility: %v", eventIDs(view.Decisions))
	}
}

func TestBuildKeepsDecisionThatIsLastForAnyTunableKey(t *testing.T) {
	data := model.SessionData{Session: "sid", Events: []model.Event{
		journalEvent(1, model.KindDecision, model.DecisionData{Text: "shared settings"}, "tunable:workers", "tunable:retry"),
		journalEvent(2, model.KindDecision, model.DecisionData{Text: "new worker count", Replaces: 1}, "tunable:workers"),
		journalEvent(3, model.KindMerged, model.MergedData{Branch: "quick/x"}),
	}}

	view := journal.Build(data, false)
	if !hasEvent(view.Decisions, 1) || !hasEvent(view.Decisions, 2) {
		t.Fatalf("a decision last for one tunable key was hidden: %v", eventIDs(view.Decisions))
	}
}

func TestMarkdownMarksDoneTaskWithCheckedBox(t *testing.T) {
	got := journal.Build(model.SessionData{
		Session: "sid",
		Tasks:   []model.SessionTask{journalTask(3, "finished", model.StatusDone, 1)},
	}, false).Markdown()
	if !strings.Contains(got, "- [x] [session] T3 finished\n") {
		t.Fatalf("Markdown() did not render a done task with a checked box: %q", got)
	}
}

func TestMarkdownRendersEverySectionAndLineShape(t *testing.T) {
	data := model.SessionData{
		Session: "sid",
		Events: []model.Event{
			journalEvent(1, model.KindNote, model.NoteData{Text: "work", Ref: "abc"}, model.BranchTag("quick/y")),
			func() model.Event {
				e := journalEvent(2, model.KindNote, model.NoteData{Text: "task work"})
				e.Task = 7
				return e
			}(),
			journalEvent(3, model.KindCompacted, struct{}{}),
			journalEvent(4, model.KindMerged, model.MergedData{Branch: "quick/x", PR: 42, SHA: "deadbeef"}),
			journalEvent(5, model.KindDecision, model.DecisionData{Text: "decide", Replaces: 1}, "tag"),
		},
		Tasks: []model.SessionTask{journalTask(2, "ready task", model.StatusReady, 0, model.BranchTag("quick/x"))},
	}

	got := journal.Build(data, false).Markdown()
	want := "# Session sid\n\n## Work log\n\n" +
		"- 14:01Z [quick/y] work (abc)\n" +
		"- 14:02Z [session] T7: task work\n" +
		"- 14:03Z [session] compacted\n" +
		"- 14:04Z [quick/x] merged #42 (deadbeef)\n\n" +
		"## Todo\n\n" +
		"- [ ] [quick/x] T2 ready task (ready)\n\n" +
		"## Decisions\n\n" +
		"- 2026-09-28 [agent] decide #tag — replaces e1 · e5\n"
	if got != want {
		t.Fatalf("Markdown() =\n%s\nwant\n%s", got, want)
	}

	empty := journal.Build(model.SessionData{Session: "empty"}, false).Markdown()
	for _, heading := range []string{"# Session empty", "## Work log", "## Todo", "## Decisions"} {
		if !strings.Contains(empty, heading) {
			t.Fatalf("empty Markdown() omitted %q: %q", heading, empty)
		}
	}
}
