// Package sidebar owns herdr-desk's row in herdr's sidebar: the token a pane's card shows, its text, and the config
// row that draws it.
package sidebar

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"

	"github.com/federbenjamin/herdr-desk/internal/herdrconf"
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

// fence marks the block herdr-desk owns in herdr's config.
var fence = herdrconf.Fence{Open: "# >>> herdr-desk sidebar", Close: "# <<< herdr-desk sidebar"}

// deskRow is the sidebar row that shows the token. A row that needs a person is bold: a run's "needs you" and the
// coordinator's "need you" both contain "need". The row sets no colour of its own.
const deskRow = `[{ token = "$desk", rules = [{ contains = "need", bold = true }] }]`

// block is herdr's default agent rows (herdr 0.9.1) with the $desk row after them.
const block = `[ui.sidebar.agents]
rows = [
  ["state_icon", "machine", "workspace", "tab"],
  ["agent"],
  ` + deskRow + `,
]
`

// addRow is the note that tells the user to add the row to a table of their own.
const addRow = "herdr: add " + deskRow + " to your [ui.sidebar.agents] rows"

// WriteHerdrSidebar edits herdr's config text so its agent cards show the $desk row. A config with no
// ui.sidebar.agents table gets one, fenced by "# >>> herdr-desk sidebar" and "# <<< herdr-desk sidebar"; a fenced
// block already there is rewritten in place, so a second run returns the text unchanged. A config that defines
// anything under ui.sidebar.agents itself is the user's to edit: the text comes back unchanged, and note, when no
// value under it names $desk yet, says what to add. A config that does not parse comes back unchanged with a note
// saying so.
func WriteHerdrSidebar(configText string) (out string, note string) {
	before, after, found := fence.Split(configText)
	if found {
		return fence.Place(before, block, after, true), ""
	}
	var doc map[string]any
	if err := toml.Unmarshal([]byte(configText), &doc); err != nil {
		return configText, "herdr: the config does not parse, so the $desk row was not added (" + err.Error() + "); " + addRow
	}
	agents, ok := lookup(doc, "ui", "sidebar", "agents")
	if !ok {
		return fence.Place(before, block, after, false), ""
	}
	if !names(agents, "$"+Token) {
		note = addRow
	}
	return configText, note
}

// lookup returns the value at the dotted path in a decoded TOML document.
func lookup(doc map[string]any, path ...string) (any, bool) {
	var v any = doc
	for _, k := range path {
		table, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = table[k]; !ok {
			return nil, false
		}
	}
	return v, true
}

// names reports whether s is a string anywhere in the decoded value v.
func names(v any, s string) bool {
	switch v := v.(type) {
	case string:
		return v == s
	case map[string]any:
		for _, e := range v {
			if names(e, s) {
				return true
			}
		}
	case []any:
		for _, e := range v {
			if names(e, s) {
				return true
			}
		}
	}
	return false
}
