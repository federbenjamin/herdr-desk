package sidebar_test

import (
	"regexp"
	"strings"
	"testing"

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

func TestWriteHerdrSidebarBoldsNeedsYouRunRows(t *testing.T) {
	out, _ := sidebar.WriteHerdrSidebar("")
	match := regexp.MustCompile(`contains\s*=\s*"([^"]+)"`).FindStringSubmatch(out)
	if len(match) != 2 {
		t.Fatalf("sidebar block has no contains rule:\n%s", out)
	}
	if match[1] != "needs you" {
		t.Fatalf("sidebar rule = %q, want %q", match[1], "needs you")
	}
	if !strings.Contains("T23 needs you · blocked", match[1]) {
		t.Errorf("sidebar rule %q does not match a blocked run row", match[1])
	}
	if strings.Contains("2 running", match[1]) {
		t.Errorf("sidebar rule %q matches a row with nobody needing attention", match[1])
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
