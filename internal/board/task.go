package board

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/federbenjamin/desk/internal/model"
)

// task is the task the task page shows: the loaded detail's, else the list's, else only its number.
func (s State) task() model.Task {
	n := s.shown()
	if s.hasDetail && s.detail.Task.Number == n {
		return s.detail.Task
	}
	for _, list := range [][]model.Task{s.data.Tasks, s.data.Done} {
		if i := slices.IndexFunc(list, func(t model.Task) bool { return t.Number == n }); i >= 0 {
			return list[i]
		}
	}
	return model.Task{Number: n}
}

func (s State) history() []model.Event {
	if s.hasDetail && s.detail.Task.Number == s.shown() {
		return s.detail.History
	}
	return nil
}

func (s State) taskKey(k string) (State, []Effect) {
	t := s.task()
	if t.Number == 0 {
		if k == "esc" {
			s.page = pageBoard
		}
		return s, nil
	}
	if s.steps {
		return s.stepsKey(k, t)
	}
	switch k {
	case "esc":
		s.page = pageBoard
	case "down", "j":
		s.taskTop++
	case "up":
		s.taskTop--
	case "e":
		if s.refuse(k) {
			return s, nil
		}
		s.editing = true
		s.notes, s.notesFrom, s.notesTop = newNotes(t.Notes), t.Notes, 0
	case "t":
		s.steps = true
		s.stepSel = 0
	case "R":
		if !s.refuse(k) {
			s.prompt = newPrompt(promptRoot, "root: ", t.Number, t.Root)
		}
	case "M":
		if !s.refuse(k) {
			s.prompt = newPrompt(promptModel, "model: ", t.Number, t.Model)
		}
	case "I":
		if s.refuse(k) {
			return s, nil
		}
		isolations := model.Isolations()
		next := isolations[(slices.Index(isolations, t.Isolation)+1)%len(isolations)]
		return s, []Effect{SetTask{Task: t.Number, Patch: model.Patch{Isolation: &next}}}
	case "o":
		refs := s.refs()
		switch len(refs) {
		case 0:
			s.status = taskID(t.Number) + " has no ref"
		case 1:
			return s, []Effect{OpenRef{Ref: refs[0], Dir: t.Project}}
		default:
			s.picking, s.pickSel = true, 0
		}
	case "?":
		s.keys = true
	case "q", "ctrl+c":
		return s, []Effect{Quit{}}
	default:
		return s.act(k, t)
	}
	return s, nil
}

func (s State) stepsKey(k string, t model.Task) (State, []Effect) {
	steps := t.Steps
	s.stepSel = max(min(s.stepSel, len(steps)-1), 0)
	switch k {
	case "esc":
		s.steps = false
		return s, nil
	case "down", "j":
		s.stepSel = max(min(s.stepSel+1, len(steps)-1), 0)
		return s, nil
	case "up":
		s.stepSel = max(s.stepSel-1, 0)
		return s, nil
	case "q", "ctrl+c":
		return s, []Effect{Quit{}}
	case "space", "enter", "a", "r", "x":
		if s.refuse(k) {
			return s, nil
		}
	default:
		return s, nil
	}
	if k == "a" {
		s.prompt = newPrompt(promptStepAdd, "step: ", t.Number, "")
		return s, nil
	}
	if len(steps) == 0 {
		return s, nil
	}
	step := steps[s.stepSel]
	switch k {
	case "r":
		s.prompt = newPrompt(promptStepRename, "step: ", t.Number, step.Text)
		s.prompt.step = step.ShortID
		return s, nil
	case "x":
		return s, []Effect{StepTask{Task: t.Number, Op: model.StepOp{Op: "remove", ShortID: step.ShortID}}}
	}
	return s, []Effect{StepTask{Task: t.Number, Op: model.StepOp{Op: "toggle", ShortID: step.ShortID}}}
}

// noWrap is wider than any notes line, so the editor never wraps and its rows are the text's lines.
const noWrap = 1 << 16

func newNotes(text string) textarea.Model {
	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.MaxHeight = 0
	ta.CharLimit = 0
	ta.SetWidth(noWrap)
	ta.SetValue(text)
	_ = ta.Focus()
	return ta
}

func (s State) notesKey(m tea.KeyPressMsg) (State, []Effect) {
	switch m.String() {
	case "esc", "ctrl+c":
		s.editing = false
		return s, nil
	case "ctrl+s":
		t := s.task()
		if t.Notes != s.notesFrom {
			// Someone wrote the notes while the editor was open; a save would replace their text.
			s.notesFrom = t.Notes
			s.status = taskID(t.Number) + "'s notes changed while you edited: ctrl+s replaces them, esc keeps them"
			return s, nil
		}
		s.editing = false
		v := s.notes.Value()
		s.notesOut = unsaved{task: t.Number, text: v, from: s.notesFrom}
		return s, []Effect{SetTask{Task: t.Number, Patch: model.Patch{Notes: &v}}}
	}
	s.notes = typeNotes(s.notes, m)
	return s, nil
}

// typeNotes sends a key or a paste to the notes editor. It copies the buffer first, as typeInto does for a text
// input, then puts the cursor back where it was.
func typeNotes(ta textarea.Model, m tea.Msg) textarea.Model {
	row, col := ta.Line(), ta.Column()
	ta.SetValue(ta.Value())
	ta.MoveToBegin()
	for range row {
		ta.CursorDown()
	}
	ta.SetCursorColumn(col)
	ta, _ = ta.Update(m)
	return ta
}

func (s State) pickKey(k string) (State, []Effect) {
	refs := s.refs()
	switch k {
	case "down", "j":
		s.pickSel = min(s.pickSel+1, len(refs)-1)
	case "up":
		s.pickSel = max(s.pickSel-1, 0)
	case "esc", "ctrl+c":
		s.picking = false
	case "enter":
		s.picking = false
		if s.pickSel < len(refs) {
			return s, []Effect{OpenRef{Ref: refs[s.pickSel], Dir: s.task().Project}}
		}
	}
	return s, nil
}

// refs are the distinct refs of the history's notes and sets, in order. An unreadable event has none.
func (s State) refs() []string {
	var out []string
	for _, ev := range s.history() {
		var ref string
		switch ev.Kind {
		case model.KindNote:
			var d model.NoteData
			if json.Unmarshal(ev.Data, &d) == nil {
				ref = d.Ref
			}
		case model.KindSet:
			var p model.Patch
			if json.Unmarshal(ev.Data, &p) == nil {
				ref = p.Ref
			}
		}
		if ref != "" && !slices.Contains(out, ref) {
			out = append(out, ref)
		}
	}
	return out
}

func (s State) taskColumn(p palette, w, body int) []string {
	t := s.task()
	if t.Number == 0 {
		return append([]string{"", rule(w)}, fit(nil, 0, body)...)
	}
	left := p.id(taskID(t.Number)) + "  " + p.status(t.Status, string(t.Status)) + "  " + oneLine(t.Title)
	right := base(t.Project)
	if t.Thread != "" {
		right = join(" · ", right, p.thread("#"+oneLine(t.Thread)))
	}
	head := []string{spread(left, right, w), rule(w)}
	if s.editing {
		return append(head, fit(s.notesLines(p, w), s.notesTop, body)...)
	}
	lines, _ := s.taskBody(p, w)
	for i := range lines {
		lines[i] = cut(lines[i], w)
	}
	return append(head, fit(lines, s.taskTop, body)...)
}

func orDash(v string) string {
	if v == "" {
		return "-"
	}
	return v
}

// taskBody is the task page under its head, and the index of the selected step's line in steps mode (else -1).
func (s State) taskBody(p palette, w int) (lines []string, stepLine int) {
	t := s.task()
	stepLine = -1
	lines = []string{
		"root " + orDash(oneLine(t.Root)) + " · isolation " + orDash(oneLine(t.Isolation)) + " · model " + orDash(oneLine(t.Model)),
		"",
		p.section("NOTES"),
	}
	if t.Notes != "" {
		for _, l := range strings.Split(t.Notes, "\n") {
			lines = append(lines, strings.Split(ansi.Wrap(clean(l), max(w, 1), ""), "\n")...)
		}
	}
	if len(t.Steps) > 0 {
		done := 0
		for _, st := range t.Steps {
			if st.Done {
				done++
			}
		}
		lines = append(lines, "", p.section(fmt.Sprintf("STEPS  %d/%d", done, len(t.Steps))))
		for i, st := range t.Steps {
			box := "[ ] "
			if st.Done {
				box = "[x] "
			}
			lead := ""
			if s.steps {
				lead = mark(i == s.stepSel)
				if i == s.stepSel {
					stepLine = len(lines)
				}
			}
			lines = append(lines, lead+box+oneLine(st.Text))
		}
	}
	hist := s.history()
	sessions := map[string]bool{}
	for _, ev := range hist {
		if ev.Session != "" {
			sessions[ev.Session] = true
		}
	}
	across := fmt.Sprintf("across %d sessions", len(sessions))
	if len(sessions) == 1 {
		across = "across 1 session"
	}
	lines = append(lines, "", p.section("HISTORY")+"  "+across)
	for _, ev := range hist {
		lines = append(lines, p.dim(ev.TS.Local().Format("01-02 15:04"))+"  "+pad(s.who(ev), 6)+"  "+oneLine(s.eventText(ev)))
	}
	if refs := s.refs(); len(refs) > 0 {
		lines = append(lines, "", p.section("FILES"))
		for _, r := range refs {
			lines = append(lines, oneLine(r))
		}
	}
	return lines, stepLine
}

func (s State) notesLines(p palette, w int) []string {
	lines := []string{p.section("NOTES") + "  editing"}
	row, col := s.notes.Line(), s.notes.Column()
	for i, l := range strings.Split(s.notes.Value(), "\n") {
		if i == row {
			l = withCursor(p, l, col)
		}
		lines = append(lines, strings.Split(ansi.Wrap(l, max(w, 1), ""), "\n")...)
	}
	return lines
}

// notesCursor is the index of the editor's drawn line that holds the cursor: the line its styled cell is on.
func (s State) notesCursor(w int) int {
	mark, _, _ := strings.Cut(colour.cursor("x"), "x")
	lines := s.notesLines(colour, w)
	for i, l := range lines {
		if strings.Contains(l, mark) {
			return i
		}
	}
	return len(lines) - 1
}

func isRunner(ev model.Event) bool { return ev.Run != 0 && ev.Session == "" }

func (s State) who(ev model.Event) string {
	switch {
	case isRunner(ev):
		return "runner"
	case ev.Who == model.WhoAgent:
		return "agent"
	}
	return "you"
}

// eventText is a history line's text. An event whose data this client cannot read says so, and is never drawn
// as an event with empty fields.
func (s State) eventText(ev model.Event) string {
	unreadable := string(ev.Kind) + " · unreadable"
	read := func(v any) bool { return json.Unmarshal(ev.Data, v) == nil }
	switch ev.Kind {
	case model.KindTask:
		var d model.TaskData
		if !read(&d) {
			return unreadable
		}
		st := d.Status
		if st == "" {
			st = model.StatusOpen
		}
		text := "created · " + string(st)
		if d.Thread != "" {
			text += " · #" + d.Thread
		}
		return text
	case model.KindSet:
		var p model.Patch
		if !read(&p) {
			return unreadable
		}
		return s.setText(ev, p)
	case model.KindStep:
		var op model.StepOp
		if !read(&op) {
			return unreadable
		}
		what := op.Text
		if what == "" {
			what = op.ShortID
		}
		return "step " + op.Op + " " + what
	case model.KindNote:
		var d model.NoteData
		if !read(&d) {
			return unreadable
		}
		text := `note "` + d.Text + `"`
		if d.Ref != "" {
			text += " [" + d.Ref + "]"
		}
		return text
	case model.KindDecision:
		var d model.DecisionData
		if !read(&d) {
			return unreadable
		}
		return "decision " + d.Text
	case model.KindMerged:
		var d model.MergedData
		if !read(&d) {
			return unreadable
		}
		return "merged " + d.Branch
	}
	return string(ev.Kind)
}

func (s State) setText(ev model.Event, p model.Patch) string {
	if p.Status != nil && *p.Status == model.StatusStarted && isRunner(ev) {
		if i := slices.IndexFunc(s.data.Runs, func(r model.Run) bool { return r.ID == ev.Run }); i >= 0 {
			r := s.data.Runs[i]
			text := "claimed · routed: " + base(r.Root) + ", " + r.Isolation + ", " + r.Model
			if r.Reason != "" {
				text += " — " + r.Reason
			}
			return text
		}
	}
	var parts []string
	if p.Status != nil {
		parts = append(parts, string(*p.Status))
	}
	if p.Title != nil {
		parts = append(parts, "title")
	}
	if p.Notes != nil {
		parts = append(parts, "notes")
	}
	if p.Thread != nil {
		parts = append(parts, "thread #"+*p.Thread)
	}
	if p.Root != nil {
		parts = append(parts, "root "+*p.Root)
	}
	if p.Isolation != nil {
		parts = append(parts, "isolation "+*p.Isolation)
	}
	if p.Model != nil {
		parts = append(parts, "model "+*p.Model)
	}
	switch {
	case p.Archived == nil:
	case *p.Archived:
		parts = append(parts, "archived")
	default:
		parts = append(parts, "unarchived")
	}
	if p.Merged {
		parts = append(parts, "merged")
	}
	text := strings.Join(parts, " · ")
	if p.Ref != "" {
		text += " [" + p.Ref + "]"
	}
	return text
}
