// Package worker builds what a worker session is started with.
package worker

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

const maxMessage = 64 << 10

// FirstMessage returns the worker's first message for a task: the title, the notes, the steps, the history, and
// how to hand the task back. Events tagged router and step events are left out. It is at most 64 KiB: the
// oldest history lines are dropped first and a line says how many.
func FirstMessage(d store.TaskDetail) string {
	t := d.Task
	head := []string{fmt.Sprintf("You are working on desk task T%d: %s", t.Number, t.Title)}
	if notes := strings.TrimSpace(t.Notes); notes != "" {
		head = append(head, notes)
	}
	if len(t.Steps) > 0 {
		lines := []string{"Steps:"}
		for _, s := range t.Steps {
			box := "[ ]"
			if s.Done {
				box = "[x]"
			}
			lines = append(lines, fmt.Sprintf("- %s %s %s", box, s.ShortID, s.Text))
		}
		head = append(head, strings.Join(lines, "\n"))
	}
	handBack := fmt.Sprintf("When the work is finished, hand the task back: run `herdr-desk set T%[1]d review --ref <a file or PR that shows the work>`. "+
		"Add `--merged` when that PR is merged. If you cannot finish, record what you need with `herdr-desk note --task T%[1]d \"<what you need>\"`, "+
		"then run `herdr-desk set T%[1]d blocked`. Finish with review or blocked.", t.Number)

	var history []string
	for _, ev := range d.History {
		if line, ok := historyLine(ev); ok {
			history = append(history, line)
		}
	}

	build := func(dropped int) string {
		parts := slices.Clone(head)
		if kept := history[dropped:]; len(kept) > 0 || dropped > 0 {
			lines := []string{"History (oldest first):"}
			if dropped > 0 {
				lines = append(lines, fmt.Sprintf("- (%d older history lines left out)", dropped))
			}
			parts = append(parts, strings.Join(append(lines, kept...), "\n"))
		}
		return strings.Join(append(parts, handBack), "\n\n") + "\n"
	}
	msg := build(0)
	if len(msg) > maxMessage {
		// Sized by arithmetic, so a long history is not rebuilt once per dropped line.
		size, dropped := len(msg), 0
		countLine := func(n int) int { return len(fmt.Sprintf("\n- (%d older history lines left out)", n)) }
		for size > maxMessage && dropped < len(history) {
			size -= len(history[dropped]) + 1
			if dropped > 0 {
				size -= countLine(dropped)
			}
			dropped++
			size += countLine(dropped)
		}
		msg = build(dropped)
	}
	if len(msg) > maxMessage {
		// Only a title, notes, or steps past the limit on their own get here: the text before the hand-back is cut.
		tail := "\n\n" + handBack + "\n"
		body := msg[:maxMessage-len(tail)]
		for !utf8.ValidString(body) {
			body = body[:len(body)-1]
		}
		msg = body + tail
	}
	return msg
}

// historyLine renders one event, and reports false for an event the message leaves out.
func historyLine(ev model.Event) (string, bool) {
	if ev.Kind == model.KindStep || slices.Contains(ev.Tags, model.TagRouter) {
		return "", false
	}
	writer := "user"
	switch {
	case ev.Run != 0 && ev.Session == "":
		writer = model.TagRunner
	case ev.Who == model.WhoAgent:
		writer = "agent"
	}
	var what, ref string
	switch ev.Kind {
	case model.KindTask:
		what = "created"
	case model.KindSet:
		var p model.Patch
		_ = json.Unmarshal(ev.Data, &p)
		ref = p.Ref
		if p.Status != nil {
			what = "status " + string(*p.Status)
		} else {
			what = "changed " + strings.Join(patchFields(p), ", ")
		}
	case model.KindNote:
		var n model.NoteData
		_ = json.Unmarshal(ev.Data, &n)
		what, ref = "note: "+n.Text, n.Ref
	case model.KindDecision:
		var dd model.DecisionData
		_ = json.Unmarshal(ev.Data, &dd)
		what = "decided: " + dd.Text
	case model.KindMerged:
		var m model.MergedData
		_ = json.Unmarshal(ev.Data, &m)
		what = "merged " + m.Branch
	default:
		what = string(ev.Kind)
	}
	line := fmt.Sprintf("- %s %s: %s", ev.TS.UTC().Format("2006-01-02 15:04Z"), writer, strings.TrimSpace(what))
	if ref != "" {
		line += " (" + ref + ")"
	}
	return line, true
}

func patchFields(p model.Patch) []string {
	var names []string
	for _, f := range []struct {
		name string
		set  bool
	}{
		{"title", p.Title != nil},
		{"notes", p.Notes != nil},
		{"thread", p.Thread != nil},
		{"root", p.Root != nil},
		{"isolation", p.Isolation != nil},
		{"model", p.Model != nil},
		{"archived", p.Archived != nil},
	} {
		if f.set {
			names = append(names, f.name)
		}
	}
	return names
}
