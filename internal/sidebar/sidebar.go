// Package sidebar owns herdr-desk's row in herdr's sidebar: the token a pane's card shows, its text, and the config
// row that draws it.
package sidebar

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/federbenjamin/herdr-desk/internal/model"
)

// Token is the name of the pane metadata token herdr-desk reports; herdr's config shows it as $desk.
const Token = "desk"

// Source is the source herdr-desk reports pane metadata as.
const Source = "herdr-desk"

// MaxWidth is the most runes a row holds: a 30-column sidebar less the card's indent.
const MaxWidth = 28

const sep = " · "

// RunText is the row of the run's pane: the task id, then the state, then one detail, cut to MaxWidth with the id and
// the state kept whole. ref is the ref the run's worker handed back with, "" when none; loc is where "since" is
// read (nil → local time). A run that is not its task's current run, has no pane yet, or was killed or failed shows
// nothing: "".
func RunText(t model.Task, r model.Run, current bool, ref string, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	id := "T" + strconv.Itoa(t.Number)
	switch {
	case !current || r.State == model.RunStarting || r.State == model.RunWaiting || r.State == model.RunKilled || r.State == model.RunFailed:
		return ""
	case r.State == model.RunRunning:
		since := ""
		if !r.StartedTS.IsZero() {
			since = "since " + r.StartedTS.In(loc).Format("15:04")
		}
		return row(id+" running", since)
	case t.Status == model.StatusDone:
		return row(id+" done", "")
	case t.Status == model.StatusBlocked:
		return row(id+" needs you", "blocked")
	case t.Status == model.StatusReview && r.State == model.RunIdle:
		return row(id+" review", "went idle")
	case t.Status == model.StatusReview:
		return row(id+" review", refText(ref))
	}
	return row(id+" "+string(t.Status), "")
}

// CoordinatorText is the row of the coordinator's pane: how many tasks need a person (blocked or in review), how
// many runs are running, and how many wait; "idle" when all three are none.
func CoordinatorText(needYou, running, waiting int) string {
	var parts []string
	if needYou > 0 {
		parts = append(parts, strconv.Itoa(needYou)+" need you")
	}
	if running > 0 {
		parts = append(parts, strconv.Itoa(running)+" running")
	}
	if waiting > 0 {
		parts = append(parts, strconv.Itoa(waiting)+" waiting")
	}
	if len(parts) == 0 {
		return "idle"
	}
	return cut(strings.Join(parts, sep), MaxWidth)
}

var pullRef = regexp.MustCompile(`/pull/(\d+)(?:[/?#]|$)`)

// refText is how a hand-back's ref reads on the row: "PR #<n>" for a pull request's URL, else the ref on one line.
func refText(ref string) string {
	if m := pullRef.FindStringSubmatch(ref); m != nil {
		return "PR #" + m[1]
	}
	return strings.Join(strings.FieldsFunc(ref, func(c rune) bool { return c <= ' ' || c == 0x7f }), " ")
}

// row joins head and tail, cutting the tail first so the row fits MaxWidth; the head is never cut.
func row(head, tail string) string {
	room := MaxWidth - utf8.RuneCountInString(head) - utf8.RuneCountInString(sep)
	if tail == "" || room < 2 {
		return head
	}
	return head + sep + cut(tail, room)
}

// cut cuts s to n runes, the last of them "…" when s is longer.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimRight(string([]rune(s)[:n-1]), " ") + "…"
}

const (
	blockOpen  = "# >>> herdr-desk sidebar"
	blockClose = "# <<< herdr-desk sidebar"
)

// deskRow is the sidebar row that shows the token; "needs you" is bold and the row sets no colour of its own.
const deskRow = `[{ token = "$desk", rules = [{ contains = "needs you", bold = true }] }]`

// block is herdr's default agent rows (herdr 0.9.1) with the $desk row after them.
const block = blockOpen + `
[ui.sidebar.agents]
rows = [
  ["state_icon", "machine", "workspace", "tab"],
  ["agent"],
  ` + deskRow + `,
]
` + blockClose + "\n"

// WriteHerdrSidebar edits herdr's config text so its agent cards show the $desk row. A config with no
// [ui.sidebar.agents] table gets one, fenced by "# >>> herdr-desk sidebar" and "# <<< herdr-desk sidebar"; a fenced
// block already there is rewritten in place, so a second run returns the text unchanged. A config that has the table
// itself is the user's to edit: the text comes back unchanged, and note, when its rows do not show $desk yet, says
// what to add.
func WriteHerdrSidebar(configText string) (out string, note string) {
	lines := strings.SplitAfter(configText, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if before, after, ok := fenced(lines); ok {
		return strings.Join(before, "") + block + strings.Join(after, ""), ""
	}
	if hasAgentsTable(lines) {
		if !showsToken(lines) {
			note = "herdr: add " + deskRow + " to your [ui.sidebar.agents] rows"
		}
		return configText, note
	}
	var sb strings.Builder
	sb.WriteString(configText)
	if configText != "" {
		if !strings.HasSuffix(configText, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	sb.WriteString(block)
	return sb.String(), ""
}

// fenced returns the lines before and after the fenced block, without it; false when there is no complete block.
func fenced(lines []string) (before, after []string, ok bool) {
	open := -1
	for i, l := range lines {
		switch strings.TrimSpace(l) {
		case blockOpen:
			open = i
		case blockClose:
			if open >= 0 {
				return lines[:open:open], lines[i+1:], true
			}
		}
	}
	return nil, nil, false
}

var (
	tableHeader = regexp.MustCompile(`^\[\[?\s*([A-Za-z0-9_."' -]+?)\s*\]\]?\s*(#.*)?$`)
	keyLine     = regexp.MustCompile(`^([A-Za-z0-9_."' -]+?)\s*=(.*)$`)
)

// hasAgentsTable reports whether the config defines anything under ui.sidebar.agents: the table, a sub-table such as
// rows_by_agent, or a dotted key. The lines of a value spread over several lines are not read as headers.
func hasAgentsTable(lines []string) bool {
	table := ""
	depth := 0
	for _, l := range lines {
		text := strings.TrimSpace(l)
		if depth > 0 {
			depth += bracketDepth(text)
			continue
		}
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if m := tableHeader.FindStringSubmatch(text); m != nil {
			table = keyPath(m[1])
			if underAgents(table) {
				return true
			}
			continue
		}
		if m := keyLine.FindStringSubmatch(text); m != nil {
			path := keyPath(m[1])
			if table != "" {
				path = table + "." + path
			}
			if underAgents(path) {
				return true
			}
			depth = max(bracketDepth(m[2]), 0)
		}
	}
	return false
}

// keyPath is a dotted TOML key with its whitespace and quotes taken out.
func keyPath(k string) string {
	return strings.NewReplacer(" ", "", "\t", "", `"`, "", "'", "").Replace(k)
}

func underAgents(path string) bool {
	return path == "ui.sidebar.agents" || strings.HasPrefix(path, "ui.sidebar.agents.")
}

// bracketDepth is the brackets and braces s opens less those it closes, outside strings and a comment.
func bracketDepth(s string) int {
	depth := 0
	var quote rune
	escaped := false
	for _, c := range s {
		switch {
		case quote != 0:
			switch {
			case escaped:
				escaped = false
			case c == '\\' && quote == '"':
				escaped = true
			case c == quote:
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return depth
		case c == '[' || c == '{':
			depth++
		case c == ']' || c == '}':
			depth--
		}
	}
	return depth
}

// showsToken reports whether a line that is not a comment names the $desk token.
func showsToken(lines []string) bool {
	for _, l := range lines {
		text := strings.TrimSpace(l)
		if !strings.HasPrefix(text, "#") && strings.Contains(text, `"$`+Token+`"`) {
			return true
		}
	}
	return false
}
