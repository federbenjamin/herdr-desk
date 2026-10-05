// Package herdrdesk holds the files the herdr-desk binary embeds.
package herdrdesk

import _ "embed"

//go:embed profiles/claude-code/skills/herdr-desk/SKILL.md
var skill string

// Skill returns the text of the agent skill (profiles/claude-code/skills/herdr-desk/SKILL.md).
func Skill() string { return skill }
