package journal_test

import (
	"encoding/json"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/journal"
	"github.com/federbenjamin/herdr-desk/internal/model"
)

func corrupt(id int64, kind model.Kind, data string, session string) model.Event {
	e := journalEvent(id, kind, struct{}{})
	e.Data = json.RawMessage(data)
	e.Session = session
	return e
}

func TestBuildShowsEventsWithUnreadableDataAsPlaceholdersThatNeverHide(t *testing.T) {
	data := model.SessionData{
		Session: "sid",
		Events: []model.Event{
			corrupt(1, model.KindNote, `{"text":5}`, "sid"),
			corrupt(2, model.KindMerged, `[1]`, "another"),
			corrupt(3, model.KindDecision, `{"text":`, "sid"),
			journalEvent(4, model.KindCompacted, struct{}{}),
			journalEvent(5, model.KindCompacted, struct{}{}),
			journalEvent(6, model.KindDecision, model.DecisionData{Text: "the replacement", Replaces: 3}),
			journalEvent(7, model.KindCompacted, struct{}{}),
		},
	}
	v := journal.Build(data, false)
	for _, want := range []struct {
		lines []journal.Line
		id    int64
		text  string
	}{
		{v.Work, 1, "e1: unreadable event data"},
		{v.Work, 2, "e2: unreadable event data"},
		{v.Decisions, 3, "e3: unreadable event data"},
	} {
		if got := lineForEvent(t, want.lines, want.id); got.Text != want.text {
			t.Errorf("e%d text = %q, want %q", want.id, got.Text, want.text)
		}
	}
}
