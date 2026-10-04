// Package desk holds the files the desk binary embeds.
package desk

import _ "embed"

//go:embed profiles/claude-code/skills/desk/SKILL.md
var skill string

// Skill returns the text of the agent skill (profiles/claude-code/skills/desk/SKILL.md).
func Skill() string { return skill }
