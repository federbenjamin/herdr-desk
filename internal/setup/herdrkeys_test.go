package setup_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/setup"
	"github.com/pelletier/go-toml/v2"
)

func TestWriteHerdrKeysAddsOnlyDeskBindingsInsideItsFence(t *testing.T) {
	out, bound, skipped := setup.WriteHerdrKeys("onboarding = false\n", false)
	if !reflect.DeepEqual(bound, []string{"prefix+t", "prefix+a"}) {
		t.Errorf("bound = %#v; want prefix+t and prefix+a", bound)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped = %#v; want none", skipped)
	}
	for _, text := range []string{
		"# >>> herdr-desk keys", "# <<< herdr-desk keys", "key = \"prefix+t\"", "command = \"herdr-desk.open-board\"",
		"key = \"prefix+a\"", "command = \"herdr-desk.capture\"", "type = \"plugin_action\"",
	} {
		if !strings.Contains(out, text) {
			t.Errorf("written config does not contain %q:\n%s", text, out)
		}
	}
	if strings.Contains(out, "ctrl+d") {
		t.Errorf("written config binds forbidden ctrl+d:\n%s", out)
	}
	empty, emptyBound, emptySkipped := setup.WriteHerdrKeys("", false)
	if !reflect.DeepEqual(emptyBound, []string{"prefix+t", "prefix+a"}) || len(emptySkipped) != 0 || !strings.HasPrefix(empty, "# >>> herdr-desk keys\n") {
		t.Errorf("empty config result = %q, %#v, %#v; want a fenced herdr-desk block with both bindings", empty, emptyBound, emptySkipped)
	}
	noTrailingNewline, _, _ := setup.WriteHerdrKeys("onboarding = false", false)
	if !strings.HasPrefix(noTrailingNewline, "onboarding = false\n\n# >>> herdr-desk keys\n") {
		t.Errorf("config without a trailing newline = %q; want its text followed by one blank line and the herdr-desk fence", noTrailingNewline)
	}
}

func TestWriteHerdrKeysSkipsConflictsUnlessForcedAndIsIdempotent(t *testing.T) {
	conflicted := `[[keys.command]]
key = "prefix+t"
type = "plugin_action"
command = "other.open"
`
	skippedOut, bound, skipped := setup.WriteHerdrKeys(conflicted, false)
	if !reflect.DeepEqual(bound, []string{"prefix+a"}) {
		t.Errorf("without force bound = %#v; want only prefix+a", bound)
	}
	if !reflect.DeepEqual(skipped, []string{"prefix+t"}) {
		t.Errorf("without force skipped = %#v; want prefix+t", skipped)
	}
	if !strings.Contains(skippedOut, "command = \"other.open\"") {
		t.Errorf("without force removed conflicting binding:\n%s", skippedOut)
	}
	if strings.Contains(skippedOut, "command = \"herdr-desk.open-board\"") {
		t.Errorf("without force installed herdr-desk binding over a conflict:\n%s", skippedOut)
	}
	bothConflicted := conflicted + `[[keys.command]]
key = "prefix+a"
type = "plugin_action"
command = "other.capture"
`
	unchanged, noBound, bothSkipped := setup.WriteHerdrKeys(bothConflicted, false)
	if unchanged != bothConflicted || len(noBound) != 0 || !reflect.DeepEqual(bothSkipped, []string{"prefix+t", "prefix+a"}) {
		t.Errorf("two conflicts result = %q, %#v, %#v; want the original config and both keys skipped", unchanged, noBound, bothSkipped)
	}
	afterBlock := "# >>> herdr-desk keys\n# <<< herdr-desk keys\n" + conflicted
	afterOut, afterBound, afterSkipped := setup.WriteHerdrKeys(afterBlock, false)
	if !reflect.DeepEqual(afterBound, []string{"prefix+a"}) || !reflect.DeepEqual(afterSkipped, []string{"prefix+t"}) || !strings.Contains(afterOut, "command = \"other.open\"") {
		t.Errorf("conflict after an existing herdr-desk block = %q, %#v, %#v; want prefix+t skipped and prefix+a rebound", afterOut, afterBound, afterSkipped)
	}

	forced, forceBound, forceSkipped := setup.WriteHerdrKeys(conflicted, true)
	if !reflect.DeepEqual(forceBound, []string{"prefix+t", "prefix+a"}) || len(forceSkipped) != 0 {
		t.Errorf("with force bound, skipped = %#v, %#v; want both bound and none skipped", forceBound, forceSkipped)
	}
	if strings.Contains(forced, "command = \"other.open\"") || !strings.Contains(forced, "command = \"herdr-desk.open-board\"") {
		t.Errorf("with force did not replace conflicting binding:\n%s", forced)
	}
	again, againBound, againSkipped := setup.WriteHerdrKeys(forced, false)
	if again != forced {
		t.Errorf("second WriteHerdrKeys changed an already-written config:\nfirst:\n%s\nsecond:\n%s", forced, again)
	}
	if !reflect.DeepEqual(againBound, []string{"prefix+t", "prefix+a"}) || len(againSkipped) != 0 {
		t.Errorf("second WriteHerdrKeys bound, skipped = %#v, %#v; want both herdr-desk keys and none skipped", againBound, againSkipped)
	}
}

// The blank and comment lines after a removed binding belong to the binding that follows it.
func TestWriteHerdrKeysForceKeepsTheCommentsAboveTheNextBinding(t *testing.T) {
	const keep = "\n# my own binding\n# opens the notes\n"
	conflicted := "[[keys.command]]\nkey = \"prefix+t\"\ntype = \"plugin_action\"\ncommand = \"other.open\"\n" +
		keep + "[[keys.command]]\nkey = \"prefix+n\"\ntype = \"plugin_action\"\ncommand = \"other.notes\"\n"
	out, bound, _ := setup.WriteHerdrKeys(conflicted, true)
	if !reflect.DeepEqual(bound, []string{"prefix+t", "prefix+a"}) {
		t.Fatalf("bound = %#v; want both keys", bound)
	}
	want := keep + "[[keys.command]]\nkey = \"prefix+n\""
	if !strings.Contains(out, want) {
		t.Errorf("the comments above the next binding are gone:\n%s", out)
	}
	if strings.Contains(out, "other.open") {
		t.Errorf("the conflicting binding stayed:\n%s", out)
	}
	var parsed struct {
		Keys struct {
			Command []map[string]string `toml:"command"`
		} `toml:"keys"`
	}
	if err := toml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not TOML: %v\n%s", err, out)
	}
	if len(parsed.Keys.Command) != 3 {
		t.Errorf("parsed %d bindings, want 3 (notes, open-board, capture):\n%s", len(parsed.Keys.Command), out)
	}
	again, _, _ := setup.WriteHerdrKeys(out, false)
	if again != out {
		t.Errorf("second run changed the text:\nfirst:\n%s\nsecond:\n%s", out, again)
	}
}
