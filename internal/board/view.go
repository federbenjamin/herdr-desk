package board

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/model"
)

// Section is one board section: its title and the statuses it lists, in order.
type Section struct {
	Title    string
	Statuses []model.Status
}

// Sections returns the board's three sections: NEEDS YOU (blocked, review), IN MOTION (started),
// ON DECK (ready, open).
func Sections() []Section {
	return []Section{
		{"NEEDS YOU", []model.Status{model.StatusBlocked, model.StatusReview}},
		{"IN MOTION", []model.Status{model.StatusStarted}},
		{"ON DECK", []model.Status{model.StatusReady, model.StatusOpen}},
	}
}

// Age prints a duration as the board does: seconds under a minute, minutes under an hour, hours under
// 48 hours, then days: 40s, 5m, 30h, 2d. A negative duration prints 0s.
func Age(d time.Duration) string {
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// Widths that pick the layout.
const (
	narrowBelow = 78
	wideFrom    = 110
	doneShown   = 20
)

const (
	boardFooter = "+ add  n ready  s start  b blocked  r review  x done  a #agent  f focus  k kill  P pause  / search  p project  t thread  d done  ? keys"
	shortFooter = "? keys  q quit"
	taskFooter  = "e notes  t steps  n ready  x done  o open  R root  I isolation  M model  f focus  esc back"
	stepsFooter = "space toggle  a add  r rename  x remove  esc back"
	notesFooter = "ctrl+s save  esc cancel"
	pickFooter  = "enter open  esc close"
)

type palette struct{ colour bool }

var (
	plain  = palette{}
	colour = palette{colour: true}
)

func (p palette) paint(st lipgloss.Style, s string) string {
	if !p.colour || s == "" {
		return s
	}
	return st.Render(s)
}

func fg(c ansi.BasicColor) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

func (p palette) status(st model.Status, s string) string {
	c := lipgloss.BrightBlack
	switch st {
	case model.StatusBlocked:
		c = lipgloss.Red
	case model.StatusReview:
		c = lipgloss.Yellow
	case model.StatusStarted:
		c = lipgloss.Green
	case model.StatusReady:
		c = lipgloss.Cyan
	}
	return p.paint(fg(c), s)
}

func (p palette) dim(s string) string     { return p.paint(fg(lipgloss.BrightBlack), s) }
func (p palette) id(s string) string      { return p.paint(fg(lipgloss.Blue), s) }
func (p palette) thread(s string) string  { return p.paint(fg(lipgloss.Magenta), s) }
func (p palette) section(s string) string { return p.paint(fg(lipgloss.Yellow).Bold(true), s) }
func (p palette) bold(s string) string    { return p.paint(lipgloss.NewStyle().Bold(true), s) }
func (p palette) cursor(s string) string  { return p.paint(lipgloss.NewStyle().Reverse(true), s) }

func width(s string) int { return ansi.StringWidth(s) }

func cut(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if width(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

func pad(s string, w int) string {
	if n := w - width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

func spread(left, right string, w int) string {
	if right == "" {
		return cut(left, w)
	}
	gap := w - width(left) - width(right)
	if gap < 2 {
		return cut(left+"  "+right, w)
	}
	return left + strings.Repeat(" ", gap) + right
}

func rule(w int) string { return strings.Repeat("─", max(w, 0)) }

// oneLine is text an agent, the user, or the home wrote, made one line and safe to draw: runs of white space
// become one space, and clean drops the rest of its control characters.
func oneLine(s string) string { return clean(strings.Join(strings.Fields(s), " ")) }

// clean drops every control character but the tab, which becomes a space, so text from the home reaches the
// terminal as text: no escape sequence, bell, or carriage return in it can act.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
}

func wrapGroups(s string, w int) []string {
	var lines []string
	cur := ""
	for _, g := range strings.Split(s, "  ") {
		switch {
		case cur == "":
			cur = g
		case width(cur)+2+width(g) <= w:
			cur += "  " + g
		default:
			lines = append(lines, cur)
			cur = g
		}
	}
	return append(lines, cur)
}

func base(project string) string {
	if project == "" {
		return ""
	}
	return oneLine(filepath.Base(project))
}

func join(sep string, parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}

func taskID(n int) string { return "T" + strconv.Itoa(n) }

// Text is the screen as plain text: no escape byte, lines joined by "\n", no line wider than the width.
func (s State) Text() string { return s.view(plain) }

// Render is the screen with styles, using only the terminal's 16 palette colours.
func (s State) Render() string { return s.view(colour) }

func (s State) wide() bool { return s.width >= wideFrom }

func (s State) split() (int, int) {
	left := (s.width - 3) / 2
	return left, s.width - 3 - left
}

func (s State) bodyHeight(bottom int) int {
	return max(s.height-bottom-4, 1)
}

func (s State) view(p palette) string {
	w := s.width
	bottom := s.bottom(p, w)
	body := s.bodyHeight(len(bottom))
	var top []string
	switch {
	case s.keys:
		top = s.keysLines(p, body+2)
	case s.wide():
		lw, rw := s.split()
		left := s.boardColumn(p, lw, body)
		right := s.taskColumn(p, rw, body)
		for i := range left {
			top = append(top, pad(cut(left[i], lw), lw)+" "+p.dim("│")+" "+right[i])
		}
	case s.page == pageTask:
		top = s.taskColumn(p, w, body)
	default:
		top = s.boardColumn(p, w, body)
	}
	lines := append(top, rule(w))
	lines = append(lines, bottom...)
	lines = append(lines, oneLine(s.status))
	for i := range lines {
		lines[i] = cut(lines[i], w)
	}
	return strings.Join(lines, "\n")
}

func fit(lines []string, top, n int) []string {
	out := make([]string, n)
	for i := range n {
		if top+i < len(lines) && top+i >= 0 {
			out[i] = lines[top+i]
		}
	}
	return out
}

func (s State) bottom(p palette, w int) []string {
	switch {
	case s.adding:
		return s.add.lines(p, w)
	case s.editing:
		return []string{notesFooter}
	case s.prompt.kind != promptNone:
		return []string{s.prompt.line(p)}
	case s.picking:
		lines := []string{"open which ref?"}
		for i, r := range s.refs() {
			lines = append(lines, mark(i == s.pickSel)+oneLine(r))
		}
		return append(lines, pickFooter)
	case s.page == pageTask && s.steps:
		return wrapGroups(stepsFooter, w)
	case s.page == pageTask:
		return wrapGroups(taskFooter, w)
	case w < narrowBelow:
		return []string{shortFooter}
	}
	return wrapGroups(boardFooter, w)
}

func mark(selected bool) string {
	if selected {
		return "▸ "
	}
	return "  "
}

func (s State) boardColumn(p palette, w, body int) []string {
	lines, _, _ := s.boardLines(p, w)
	return append([]string{s.header(p, w), rule(w)}, fit(lines, s.top, body)...)
}

func (s State) header(p palette, w int) string {
	left := p.bold("herdr-desk") + "  " + s.projectLabel() + " ▾  thread: " + s.threadLabel() + " ▾"
	return spread(left, s.runnerLabel(), w)
}

func (s State) projectLabel() string {
	switch s.proj.kind {
	case projOne:
		return s.proj.name
	case projNone:
		return "no project"
	}
	return "all"
}

func (s State) threadLabel() string {
	if s.thread == "" {
		return "all"
	}
	return oneLine(s.thread)
}

func (s State) runnerLabel() string {
	d := s.data
	if d.Offline {
		age := "of unknown age"
		if d.SnapshotTS != nil {
			age = Age(s.now().Sub(*d.SnapshotTS))
		}
		return "offline (snapshot " + age + ")"
	}
	where := "home"
	if !s.cfg.IsHome {
		where = "client"
	}
	word := s.runnerWord()
	glyph := "○"
	switch word {
	case api.RunnerStateOn:
		glyph = "●"
	case api.RunnerStatePaused:
		glyph = "◐"
	default:
		word = oneLine(word)
		return "runner " + glyph + " " + word + " · " + where
	}
	count := strconv.Itoa(s.liveRuns())
	if d.Status.RunnerCap > 0 {
		count += "/" + strconv.Itoa(d.Status.RunnerCap)
	}
	return "runner " + glyph + " " + word + " · " + count + " · " + where
}

func (s State) liveRuns() int {
	n := 0
	for _, r := range s.data.Runs {
		if r.EndedTS.IsZero() {
			n++
		}
	}
	return n
}

// liveRun returns the task's live run: the last one in Data.Runs whose EndedTS is zero.
func (s State) liveRun(task int) (model.Run, bool) {
	var found model.Run
	ok := false
	for _, r := range s.data.Runs {
		if r.Task == task && r.EndedTS.IsZero() {
			found, ok = r, true
		}
	}
	return found, ok
}

type group struct {
	title   string
	tasks   []model.Task
	inboxAt int // the index the inbox line goes before; -1 for none
	more    int
}

func (s State) groups() []group {
	var out []group
	for _, sec := range Sections() {
		g := group{title: sec.Title, inboxAt: -1}
		for _, st := range sec.Statuses {
			if st == model.StatusOpen {
				g.inboxAt = len(g.tasks)
			}
			for _, t := range s.data.Tasks {
				if t.Status == st && s.matches(t) {
					g.tasks = append(g.tasks, t)
				}
			}
		}
		out = append(out, g)
	}
	if s.drawer {
		var done []model.Task
		for _, t := range s.data.Done {
			if s.matches(t) {
				done = append(done, t)
			}
		}
		slices.SortStableFunc(done, func(a, b model.Task) int { return b.UpdatedTS.Compare(a.UpdatedTS) })
		g := group{title: "DONE", inboxAt: -1}
		if len(done) > doneShown {
			g.more = len(done) - doneShown
			done = done[:doneShown]
		}
		g.tasks = done
		out = append(out, g)
	}
	return out
}

func (s State) rows() []model.Task {
	var out []model.Task
	for _, g := range s.groups() {
		out = append(out, g.tasks...)
	}
	return out
}

func (s State) boardLines(p palette, w int) (lines []string, selLine, selSpan int) {
	detail := s.width >= narrowBelow
	i := 0
	for _, g := range s.groups() {
		lines = append(lines, p.section(g.title))
		for j, t := range g.tasks {
			if j == g.inboxAt {
				lines = append(lines, p.dim("  inbox"))
			}
			if i == s.sel {
				selLine = len(lines)
			}
			lines = append(lines, s.rowLine(p, t, i == s.sel, w, detail))
			if n := s.noteLine(p, t, w); n != "" {
				lines = append(lines, n)
			}
			if i == s.sel {
				selSpan = len(lines) - selLine
			}
			i++
		}
		if g.inboxAt == len(g.tasks) {
			lines = append(lines, p.dim("  inbox"))
		}
		if g.more > 0 {
			lines = append(lines, p.dim(fmt.Sprintf("  +%d more", g.more)))
		}
	}
	return lines, selLine, selSpan
}

func (s State) rowLine(p palette, t model.Task, selected bool, w int, detail bool) string {
	id := taskID(t.Number)
	st := fmt.Sprintf("%-7s", t.Status)
	lead := mark(selected) + id + "  " + st + "  "
	room := w - width(lead)
	title := oneLine(t.Title)
	tag, rest := s.rowDetail(t)
	det := join(" · ", tag, rest)
	head := mark(selected) + p.id(id) + "  " + p.status(t.Status, st) + "  "
	if !detail || det == "" {
		return head + cut(title, room)
	}
	if width(title)+2+width(det) > room {
		titleRoom := room - 2 - width(det)
		if titleRoom < 8 {
			return head + cut(title, room)
		}
		title = cut(title, titleRoom)
	}
	gap := room - width(title) - width(det)
	painted := p.thread(tag)
	if rest != "" {
		if tag != "" {
			painted += p.dim(" · ")
		}
		painted += p.dim(rest)
	}
	return head + title + strings.Repeat(" ", gap) + painted
}

func (s State) rowDetail(t model.Task) (tag, rest string) {
	switch {
	case t.Status == model.StatusReady && t.Thread == "agent":
		tag = "#agent · queued"
	case t.Thread != "":
		tag = "#" + oneLine(t.Thread)
	}
	if t.Status == model.StatusStarted {
		if r, ok := s.liveRun(t.Number); ok {
			age := ""
			if !r.StartedTS.IsZero() {
				age = Age(s.now().Sub(r.StartedTS))
			}
			return tag, join(" · ", base(r.Root), oneLine(r.Isolation), oneLine(r.Model), age)
		}
	}
	return tag, join(" · ", base(t.Project), Age(s.now().Sub(t.UpdatedTS))+" ago")
}

func (s State) noteLine(p palette, t model.Task, w int) string {
	note, ok := s.data.Notes[t.Number]
	if !ok || note == "" {
		return ""
	}
	label := ""
	switch t.Status {
	case model.StatusBlocked:
	case model.StatusStarted:
		label = "last note: "
	default:
		return ""
	}
	indent := strings.Repeat(" ", width(mark(false)+taskID(t.Number)+"  "))
	room := w - width(indent) - width("↳ "+label+`""`)
	if room < 1 {
		return ""
	}
	return indent + p.dim("↳ "+label+`"`+cut(oneLine(note), room)+`"`)
}

func (s State) keysLines(p palette, n int) []string {
	lines := []string{p.section("KEYS")}
	keys := boardKeys
	if s.page == pageTask {
		keys = taskKeys
	}
	for _, k := range keys {
		lines = append(lines, "  "+pad(k[0], 8)+k[1])
	}
	return fit(lines, 0, n)
}

var boardKeys = [][2]string{
	{"↓ j", "next row"},
	{"↑", "previous row"},
	{"g G", "first row · last row"},
	{"enter", "open the task"},
	{"+", "add a task"},
	{"n", "ready (on a blocked task, answer first)"},
	{"s", "started"},
	{"b", "blocked"},
	{"r", "review"},
	{"x", "done (asks unless in review)"},
	{"a", "#agent on or off"},
	{"f", "focus the run's pane"},
	{"k", "kill the run (task → blocked)"},
	{"P", "pause or resume the runner"},
	{"/", "search"},
	{"p", "next project"},
	{"t", "next thread"},
	{"d", "done drawer"},
	{"?", "keys"},
	{"esc", "close · clear the search"},
	{"q", "quit"},
}

var taskKeys = [][2]string{
	{"↓ j ↑", "scroll"},
	{"e", "edit the notes (ctrl+s saves)"},
	{"t", "steps: space toggles, a adds, r renames, x removes"},
	{"n", "ready (on a blocked task, answer first)"},
	{"s b r", "started · blocked · review"},
	{"x", "done (asks unless in review)"},
	{"a", "#agent on or off"},
	{"o", "open a ref"},
	{"R", "root"},
	{"I", "next isolation"},
	{"M", "model"},
	{"f", "focus the run's pane"},
	{"k", "kill the run (task → blocked)"},
	{"P", "pause or resume the runner"},
	{"?", "keys"},
	{"esc", "back"},
	{"q", "quit"},
}
