package model

import "strings"

// ParseCapture splits a captured line: a word starting # sets the thread, a word starting @ the project, and
// the rest is the title. A bare "#" or "@" is a title word.
func ParseCapture(line string) TaskData {
	var d TaskData
	var title []string
	for _, w := range strings.Fields(line) {
		switch {
		case len(w) > 1 && w[0] == '#':
			d.Thread = w[1:]
		case len(w) > 1 && w[0] == '@':
			d.Project = w[1:]
		default:
			title = append(title, w)
		}
	}
	d.Title = strings.Join(title, " ")
	return d
}
