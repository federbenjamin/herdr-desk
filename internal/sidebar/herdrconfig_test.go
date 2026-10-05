package sidebar_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/sidebar"
)

func TestWriteHerdrSidebarAddsOneFencedDefaultRowAndIsIdempotent(t *testing.T) {
	out, note := sidebar.WriteHerdrSidebar("theme = \"night\"\n")
	if note != "" {
		t.Fatalf("WriteHerdrSidebar() note = %q, want none", note)
	}
	for _, want := range []string{
		"# >>> herdr-desk sidebar", "# <<< herdr-desk sidebar", "[ui.sidebar.agents]",
		`["state_icon", "machine", "workspace", "tab"]`, `["agent"]`, `token = "$desk"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("written config does not contain %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "fg =") || strings.Contains(out, "tab_bar") {
		t.Fatalf("sidebar row added its own colour or tab-bar entry:\n%s", out)
	}
	if again, againNote := sidebar.WriteHerdrSidebar(out); again != out || againNote != "" {
		t.Fatalf("second WriteHerdrSidebar() = %q, %q; want unchanged config and no note", again, againNote)
	}
}

// The written bold rule matches every row text that asks for a person, a run's and the coordinator's, and no other:
// it is read from the block setup writes and run against every text RunText and CoordinatorText return.
func TestWriteHerdrSidebarBoldsNeedsYouRunRows(t *testing.T) {
	out, _ := sidebar.WriteHerdrSidebar("")
	match := regexp.MustCompile(`contains\s*=\s*"([^"]+)"`).FindStringSubmatch(out)
	if len(match) != 2 {
		t.Fatalf("sidebar block has no contains rule:\n%s", out)
	}
	rule := match[1]
	bolds := func(text string, want bool) {
		t.Helper()
		if strings.Contains(text, rule) != want {
			t.Errorf("rule %q on %q: bold = %t, want %t", rule, text, !want, want)
		}
	}

	statuses := []model.Status{model.StatusOpen, model.StatusReady, model.StatusStarted, model.StatusBlocked, model.StatusReview, model.StatusDone}
	states := slices.Concat(model.LiveRunStates(), model.FinalRunStates())
	started := time.Date(2026, 1, 2, 14, 5, 0, 0, time.UTC)
	for _, st := range statuses {
		for _, rs := range states {
			for _, ref := range []string{"", "https://github.com/o/r/pull/12", "branch-name"} {
				text := sidebar.RunText(model.Task{Number: 23, Status: st}, model.Run{State: rs, StartedTS: started}, true, ref, time.UTC)
				if text == "" {
					continue
				}
				bolds(text, st == model.StatusBlocked && rs != model.RunRunning)
			}
		}
	}
	for needYou := range 3 {
		for running := range 3 {
			for waiting := range 3 {
				bolds(sidebar.CoordinatorText(needYou, running, waiting), needYou > 0)
			}
		}
	}
	for _, text := range []string{"T23 needs you · blocked", "2 need you · 2 running", "1 need you"} {
		bolds(text, true)
	}
	for _, text := range []string{"2 running", "T21 running · since 14:05", "T22 review · PR #12", "idle"} {
		bolds(text, false)
	}
}

func TestWriteHerdrSidebarLeavesAnExistingUserTableUntouched(t *testing.T) {
	config := "[ui.sidebar.agents]\nrows = [[\"agent\"]]\n"
	out, note := sidebar.WriteHerdrSidebar(config)
	if out != config {
		t.Fatalf("WriteHerdrSidebar() rewrote a user-owned table:\n%s", out)
	}
	if !strings.Contains(note, `token = "$desk"`) || !strings.Contains(note, "[ui.sidebar.agents]") {
		t.Fatalf("WriteHerdrSidebar() note = %q, want instructions for the $desk row", note)
	}

	withToken := "[ui.sidebar.agents]\nrows = [[{ token = \"$desk\" }]]\n"
	if got, gotNote := sidebar.WriteHerdrSidebar(withToken); got != withToken || gotNote != "" {
		t.Fatalf("WriteHerdrSidebar() with a user token = %q, %q; want unchanged config and no note", got, gotNote)
	}
}
