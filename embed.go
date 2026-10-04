// Package desk holds the files the desk binary embeds.
package desk

import _ "embed"

//go:embed profiles/claude-code/skills/desk/SKILL.md
var skill string

//go:embed router/system.md
var routerSystem string

// Skill returns the text of the agent skill (profiles/claude-code/skills/desk/SKILL.md).
func Skill() string { return skill }

// RouterSystem returns the built-in router system prompt (router/system.md).
func RouterSystem() string { return routerSystem }
