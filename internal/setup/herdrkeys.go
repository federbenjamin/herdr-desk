package setup

import (
	"regexp"
	"strings"

	"github.com/federbenjamin/herdr-desk/internal/herdrconf"
)

// keysFence marks the block of bindings herdr-desk owns in herdr's config.
var keysFence = herdrconf.Fence{Open: "# >>> herdr-desk keys", Close: "# <<< herdr-desk keys"}

// deskKeys are the bindings herdr-desk writes, in order.
var deskKeys = []struct{ key, command string }{
	{"prefix+t", "herdr-desk.open-board"},
	{"prefix+a", "herdr-desk.capture"},
}

var keyLine = regexp.MustCompile(`^\s*key\s*=\s*["']([^"']*)["']`)

// WriteHerdrKeys edits herdr's config text. It returns the new text, the keys it bound, and the keys it left
// because another binding holds them. force replaces those bindings.
//
// Our bindings sit in one block fenced by "# >>> herdr-desk keys" and "# <<< herdr-desk keys". A block already there is
// rewritten in place, so a second run returns the text unchanged.
func WriteHerdrKeys(configText string, force bool) (out string, bound []string, skipped []string) {
	before, after, hadBlock := keysFence.Split(configText)

	var block strings.Builder
	for _, k := range deskKeys {
		var held bool
		if before, held = dropBinding(before, k.key, force); held && !force {
			skipped = append(skipped, k.key)
			continue
		}
		if after, held = dropBinding(after, k.key, force); held && !force {
			skipped = append(skipped, k.key)
			continue
		}
		if block.Len() > 0 {
			block.WriteString("\n")
		}
		block.WriteString("[[keys.command]]\nkey = \"" + k.key + "\"\ntype = \"plugin_action\"\ncommand = \"" + k.command + "\"\n")
		bound = append(bound, k.key)
	}
	if len(bound) == 0 && !hadBlock {
		return configText, bound, skipped
	}
	return keysFence.Place(before, block.String(), after, hadBlock), bound, skipped
}

// dropBinding looks for a [[keys.command]] table whose key is key. It reports whether one exists and, when
// remove is set, returns the lines without that table.
func dropBinding(lines []string, key string, remove bool) ([]string, bool) {
	held := false
	var out []string
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(strings.TrimSpace(lines[i]), "[[keys.command]]") {
			out = append(out, lines[i])
			continue
		}
		end := i + 1
		for end < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[end]), "[") {
			end++
		}
		match := false
		for _, l := range lines[i+1 : end] {
			if m := keyLine.FindStringSubmatch(l); m != nil && strings.EqualFold(m[1], key) {
				match = true
			}
		}
		if match {
			held = true
		}
		if match && remove {
			i = end - 1
			continue
		}
		out = append(out, lines[i:end]...)
		i = end - 1
	}
	return out, held
}
