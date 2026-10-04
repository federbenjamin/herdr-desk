// Package journal turns a session's events and tasks into the Work log, Todo, and Decisions view.
package journal

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/federbenjamin/desk/internal/model"
)

// Line is one line of the view.
type Line struct {
	EventID  int64 // 0 for a task line
	Task     int   // 0 when the line is about no task
	TS       time.Time
	Scope    string // a branch name, or "session"
	Text     string
	Ref      string
	Status   model.Status // task lines
	Who      model.Who    // decision lines
	Tags     []string     // tags other than branch:<b>
	Replaces int64        // decision lines
	Hidden   bool         // true only in a view built with all=true, on a line the default view leaves out
}

// View is a session's rendered sections.
type View struct {
	Session   string
	Work      []Line
	Todo      []Line
	Decisions []Line
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
			if b := mergedData(e).Branch; b != "" {
				merged[b] = append(merged[b], e.ID)
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
			_ = json.Unmarshal(e.Data, &d)
			branch := model.BranchOf(e.Tags)
			hidden := false
			if !slices.Contains(e.Tags, questionTag) {
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
			d := mergedData(e)
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
		if d := decisionData(e); d.Replaces != 0 && d.Replaces < e.ID {
			if r, ok := replacedBy[d.Replaces]; !ok || e.ID < r {
				replacedBy[d.Replaces] = e.ID
			}
		}
	}
	for _, e := range decisions {
		d := decisionData(e)
		branch := model.BranchOf(e.Tags)
		hidden := false
		if r, ok := replacedBy[e.ID]; ok && !isLastTunable(e, lastTunable) {
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

func mergedData(e model.Event) model.MergedData {
	var d model.MergedData
	_ = json.Unmarshal(e.Data, &d)
	return d
}

func decisionData(e model.Event) model.DecisionData {
	var d model.DecisionData
	_ = json.Unmarshal(e.Data, &d)
	return d
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
