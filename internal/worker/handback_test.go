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

func TestW6FirstMessageWithoutHandBackOmitsItsInstructionAndKeepsTheNewline(t *testing.T) {
	detail := store.TaskDetail{Task: model.Task{Number: 21, Title: "template worker", Notes: "Read the task file."}}

	got := worker.FirstMessage(detail, false)
	if strings.Contains(got, "hand the task back") {
		t.Fatalf("FirstMessage without hand-back = %q, want no hand-back instruction", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Fatalf("FirstMessage without hand-back = %q, want one trailing newline", got)
	}
	if want := "You are working on desk task T21: template worker\n\nRead the task file.\n"; got != want {
		t.Fatalf("FirstMessage without hand-back = %q, want %q", got, want)
	}
}

func TestW6FirstMessageWithoutHandBackStaysBoundedWhenHistoryIsTrimmed(t *testing.T) {
	history := make([]model.Event, 0, 100)
	base := time.Date(2026, time.October, 4, 0, 0, 0, 0, time.UTC)
	for i := range 100 {
		history = append(history, model.Event{
			TS:   base.Add(time.Duration(i) * time.Minute),
			Kind: model.KindNote,
			Data: model.MustData(model.NoteData{Text: fmt.Sprintf("event-%03d %s", i, strings.Repeat("x", 800))}),
		})
	}

	got := worker.FirstMessage(store.TaskDetail{Task: model.Task{Number: 22, Title: "large template task"}, History: history}, false)
	if len(got) > 64*1024 {
		t.Fatalf("FirstMessage without hand-back length = %d, want at most %d", len(got), 64*1024)
	}
	if strings.Contains(got, "hand the task back") {
		t.Fatal("shortened FirstMessage without hand-back restored the hand-back instruction")
	}
	if strings.Contains(got, "event-000") || !strings.Contains(got, "event-099") {
		t.Fatalf("shortened FirstMessage = %q, want oldest history omitted and newest retained", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Fatalf("shortened FirstMessage has no trailing newline: %q", got[len(got)-80:])
	}
}

func TestW6FirstMessageWithHandBackRetainsTheInstruction(t *testing.T) {
	got := worker.FirstMessage(store.TaskDetail{Task: model.Task{Number: 23, Title: "plain worker"}}, true)
	if !strings.Contains(got, "hand the task back") {
		t.Fatalf("FirstMessage with hand-back = %q, want the hand-back instruction", got)
	}
}

func TestW6FirstMessageHistoryNamesFirstMessagePatch(t *testing.T) {
	template := "Read {task_file}."
	detail := store.TaskDetail{
		Task: model.Task{Number: 24, Title: "show the template change"},
		History: []model.Event{{
			TS:   time.Date(2026, time.October, 4, 14, 2, 0, 0, time.UTC),
			Kind: model.KindSet,
			Data: model.MustData(model.Patch{FirstMessage: &template}),
		}},
	}

	if got := worker.FirstMessage(detail, false); !strings.Contains(got, "- 2026-10-04 14:02Z user: changed first_message\n") {
		t.Fatalf("FirstMessage history = %q, want first_message patch named", got)
	}
}
