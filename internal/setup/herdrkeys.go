package setup

import (
	"regexp"
	"strings"
)

const (
	keysOpen  = "# >>> herdr-desk keys"
	keysClose = "# <<< herdr-desk keys"
)

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
	lines := strings.SplitAfter(configText, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	before, after, hadBlock := splitBlock(lines)

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
	var sb strings.Builder
	sb.WriteString(strings.Join(before, ""))
	if len(bound) > 0 {
		if !hadBlock && sb.Len() > 0 {
			if !strings.HasSuffix(sb.String(), "\n") {
				sb.WriteString("\n")
			}
			sb.WriteString("\n")
		}
		sb.WriteString(keysOpen + "\n" + block.String() + keysClose + "\n")
	}
	sb.WriteString(strings.Join(after, ""))
	return sb.String(), bound, skipped
}

// splitBlock returns the lines before and after our fenced block, without the block. With no complete block
// it returns every line as before.
func splitBlock(lines []string) (before, after []string, found bool) {
	open := -1
	for i, l := range lines {
		switch strings.TrimSpace(l) {
		case keysOpen:
			open = i
		case keysClose:
			if open >= 0 {
				return lines[:open:open], lines[i+1:], true
			}
		}
	}
	return lines, nil, false
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
