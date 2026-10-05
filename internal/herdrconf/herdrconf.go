// Package herdrconf edits herdr's config text, where herdr-desk owns only blocks of lines fenced by two marker
// comments. It is the one place such a block is found and placed; setup's keys and the sidebar row both use it.
package herdrconf

import "strings"

// Fence is the two marker lines around a block herdr-desk owns.
type Fence struct{ Open, Close string }

// Split returns the lines of text before and after the fenced block, without the block, each line keeping its
// newline. found is false when text holds no complete block; before is then every line of text.
func (f Fence) Split(text string) (before, after []string, found bool) {
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	open := -1
	for i, l := range lines {
		switch strings.TrimSpace(l) {
		case f.Open:
			open = i
		case f.Close:
			if open >= 0 {
				return lines[:open:open], lines[i+1:], true
			}
		}
	}
	return lines, nil, false
}

// Place is before, the block holding body between the fence's markers, then after, as one text. A block that was not
// found before is appended after one blank line, so it never joins the user's last line. An empty body places no
// block, so the fence goes with it.
func (f Fence) Place(before []string, body string, after []string, found bool) string {
	var sb strings.Builder
	sb.WriteString(strings.Join(before, ""))
	if body != "" {
		if !found && sb.Len() > 0 {
			if !strings.HasSuffix(sb.String(), "\n") {
				sb.WriteString("\n")
			}
			sb.WriteString("\n")
		}
		sb.WriteString(f.Open + "\n" + body + f.Close + "\n")
	}
	sb.WriteString(strings.Join(after, ""))
	return sb.String()
}
