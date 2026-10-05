// Package journal turns a session's events and tasks into the Work log, Todo, and Decisions view.
package journal

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/model"
)

// Line is one line of the view. The json tags are the keys `herdr-desk session --json` prints, in model's
// snake_case style.
type Line struct {
	EventID  int64        `json:"event_id"` // 0 for a task line
	Task     int          `json:"task"`     // 0 when the line is about no task
	TS       time.Time    `json:"ts"`
	Scope    string       `json:"scope"` // a branch name, or "session"
	Text     string       `json:"text"`
	Ref      string       `json:"ref"`
	Status   model.Status `json:"status"`   // task lines
	Who      model.Who    `json:"who"`      // decision lines
	Tags     []string     `json:"tags"`     // tags other than branch:<b>
	Replaces int64        `json:"replaces"` // decision lines
	Hidden   bool         `json:"hidden"`   // true only in a view built with all=true, on a line the default view leaves out
}

// View is a session's rendered sections.
type View struct {
	Session   string `json:"session"`
	Work      []Line `json:"work"`
	Todo      []Line `json:"todo"`
	Decisions []Line `json:"decisions"`
}

const (
	sessionScope = "session"
	questionTag  = "question"
	tunablePre   = "tunable:"
)

// Build returns the session's view. With all=false, hidden lines are left out.
func Build(data model.SessionData, all bool) View {
	events := slices.Clone(data.Events)
	sort.SliceStable(events, func(i, j int) bool { return events[i].ID < events[j].ID })

	chain := map[string]bool{data.Session: true}
	for _, s := range data.Chain {
		chain[s] = true
	}

	var compacted []int64
	merged := map[string][]int64{}
	branchesWithWork := map[string]bool{}
	for _, e := range events {
		switch e.Kind {
		case model.KindCompacted:
			compacted = append(compacted, e.ID)
		case model.KindMerged:
			if d, _ := mergedData(e); d.Branch != "" {
				merged[d.Branch] = append(merged[d.Branch], e.ID)
			}
		case model.KindNote:
			if b := model.BranchOf(e.Tags); b != "" {
				branchesWithWork[b] = true
			}
		}
	}
	for _, t := range data.Tasks {
		if b := model.BranchOf(t.Tags); b != "" {
			branchesWithWork[b] = true
		}
	}

	v := View{Session: data.Session}
	add := func(dst *[]Line, l Line, hidden bool) {
		if hidden && !all {
			return
		}
		l.Hidden = hidden
		*dst = append(*dst, l)
	}

	var shownMerged []int64
	for _, e := range events {
		switch e.Kind {
		case model.KindNote:
			var d model.NoteData
			readable := json.Unmarshal(e.Data, &d) == nil
			if !readable {
				d = model.NoteData{Text: unreadable(e)}
			}
			branch := model.BranchOf(e.Tags)
			hidden := false
			if readable && !slices.Contains(e.Tags, questionTag) {
				if branch != "" {
					hidden = anyAfter(merged[branch], e.ID)
				} else {
					hidden = countAfter(compacted, e.ID) >= 2
				}
			}
			add(&v.Work, Line{EventID: e.ID, Task: e.Task, TS: e.TS, Scope: scopeOf(branch), Text: d.Text, Ref: d.Ref,
				Tags: lineTags(e.Tags, branch)}, hidden)
		case model.KindCompacted:
			add(&v.Work, Line{EventID: e.ID, TS: e.TS, Scope: sessionScope, Text: "compacted"},
				countAfter(compacted, e.ID) >= 2)
		case model.KindMerged:
			d, readable := mergedData(e)
			if !readable {
				add(&v.Work, Line{EventID: e.ID, TS: e.TS, Scope: sessionScope, Text: unreadable(e)}, false)
				continue
			}
			if !chain[e.Session] && !branchesWithWork[d.Branch] {
				continue
			}
			shownMerged = append(shownMerged, e.ID)
			add(&v.Work, Line{EventID: e.ID, TS: e.TS, Scope: scopeOf(d.Branch), Text: mergedText(d)}, false)
		}
	}

	tasks := slices.Clone(data.Tasks)
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].Number < tasks[j].Number })
	for _, t := range tasks {
		branch := model.BranchOf(t.Tags)
		hidden := false
		if t.Status == model.StatusDone {
			if branch != "" {
				hidden = anyAfter(merged[branch], t.DoneAt)
			} else {
				hidden = anyAfter(compacted, t.DoneAt)
			}
		}
		add(&v.Todo, Line{Task: t.Number, TS: t.CreatedTS, Scope: scopeOf(branch), Text: t.Title, Status: t.Status,
			Tags: lineTags(t.Tags, branch)}, hidden)
	}

	lastTunable := map[string]int64{}
	replacedBy := map[int64]int64{}
	var decisions []model.Event
	for _, e := range events {
		if e.Kind != model.KindDecision {
			continue
		}
		decisions = append(decisions, e)
		for _, t := range e.Tags {
			if strings.HasPrefix(t, tunablePre) {
				lastTunable[t] = e.ID
			}
		}
		if d, _ := decisionData(e); d.Replaces != 0 && d.Replaces < e.ID {
			if r, ok := replacedBy[d.Replaces]; !ok || e.ID < r {
				replacedBy[d.Replaces] = e.ID
			}
		}
	}
	for _, e := range decisions {
		d, readable := decisionData(e)
		if !readable {
			d.Text = unreadable(e)
		}
		branch := model.BranchOf(e.Tags)
		hidden := false
		if r, ok := replacedBy[e.ID]; ok && readable && !isLastTunable(e, lastTunable) {
			hidden = anyAfter(shownMerged, r) || anyAfter(compacted, r)
		}
		add(&v.Decisions, Line{EventID: e.ID, Task: e.Task, TS: e.TS, Scope: scopeOf(branch), Text: d.Text, Who: e.Who,
			Tags: lineTags(e.Tags, branch), Replaces: d.Replaces}, hidden)
	}
	return v
}

func isLastTunable(e model.Event, last map[string]int64) bool {
	for _, t := range e.Tags {
		if strings.HasPrefix(t, tunablePre) && last[t] == e.ID {
			return true
		}
	}
	return false
}

// mergedData and decisionData read an event's payload and report whether it parsed.
func mergedData(e model.Event) (model.MergedData, bool) {
	var d model.MergedData
	return d, json.Unmarshal(e.Data, &d) == nil
}

func decisionData(e model.Event) (model.DecisionData, bool) {
	var d model.DecisionData
	return d, json.Unmarshal(e.Data, &d) == nil
}

// unreadable is the text of a line whose event data does not parse, so the gap shows instead of an empty line.
func unreadable(e model.Event) string {
	return fmt.Sprintf("e%d: unreadable event data", e.ID)
}

func mergedText(d model.MergedData) string {
	s := "merged"
	if d.PR != 0 {
		s += fmt.Sprintf(" #%d", d.PR)
	}
	if d.SHA != "" {
		s += " (" + d.SHA + ")"
	}
	return s
}

func scopeOf(branch string) string {
	if branch == "" {
		return sessionScope
	}
	return branch
}

func lineTags(tags []string, branch string) []string {
	var out []string
	for _, t := range tags {
		if branch != "" && t == model.BranchTag(branch) {
			continue
		}
		out = append(out, t)
	}
	return out
}

func anyAfter(ids []int64, id int64) bool { return countAfter(ids, id) > 0 }

func countAfter(ids []int64, id int64) int {
	n := 0
	for _, x := range ids {
		if x > id {
			n++
		}
	}
	return n
}
