package journal

import (
	"fmt"
	"strings"
	"time"

	"github.com/federbenjamin/desk/internal/model"
)

// Markdown renders the view as the session's journal file.
func (v View) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Session %s\n\n", v.Session)
	b.WriteString("## Work log\n\n")
	for _, l := range v.Work {
		b.WriteString(workLine(l))
	}
	b.WriteString("\n## Todo\n\n")
	for _, l := range v.Todo {
		b.WriteString(todoLine(l))
	}
	b.WriteString("\n## Decisions\n\n")
	for _, l := range v.Decisions {
		b.WriteString(decisionLine(l))
	}
	return b.String()
}

func workLine(l Line) string {
	s := fmt.Sprintf("- %s [%s] ", clock(l.TS), l.Scope)
	if l.Task > 0 {
		s += fmt.Sprintf("T%d: ", l.Task)
	}
	s += l.Text
	if l.Ref != "" {
		s += " (" + l.Ref + ")"
	}
	return s + "\n"
}

func todoLine(l Line) string {
	box := " "
	if l.Status == model.StatusDone {
		box = "x"
	}
	s := fmt.Sprintf("- [%s] [%s] T%d %s", box, l.Scope, l.Task, l.Text)
	if l.Status != model.StatusOpen && l.Status != model.StatusDone {
		s += fmt.Sprintf(" (%s)", l.Status)
	}
	return s + "\n"
}

func decisionLine(l Line) string {
	s := fmt.Sprintf("- %s [%s] %s", l.TS.UTC().Format("2006-01-02"), l.Who, l.Text)
	for _, t := range l.Tags {
		s += " #" + t
	}
	if l.Replaces != 0 {
		s += fmt.Sprintf(" — replaces e%d", l.Replaces)
	}
	return s + fmt.Sprintf(" · e%d\n", l.EventID)
}

func clock(t time.Time) string { return t.UTC().Format("15:04") + "Z" }
