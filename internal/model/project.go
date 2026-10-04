package model

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// ResolveProject returns project as it is when it is "" or an absolute path. A bare name resolves to the one
// known project path with that base name; none or several is unknown-project. The store and the command line
// both decide with it.
func ResolveProject(project string, known []string) (string, error) {
	if project == "" || filepath.IsAbs(project) {
		return project, nil
	}
	if strings.ContainsRune(project, filepath.Separator) {
		return "", &Refusal{Code: CodeUnknownProject, Msg: fmt.Sprintf("project %q is neither an absolute path nor a bare name", project)}
	}
	var found []string
	for _, p := range known {
		if p != "" && filepath.Base(p) == project && !slices.Contains(found, p) {
			found = append(found, p)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", &Refusal{Code: CodeUnknownProject, Msg: fmt.Sprintf("no known project is named %q", project)}
	default:
		return "", &Refusal{Code: CodeUnknownProject, Msg: fmt.Sprintf("%d known projects are named %q; give the path", len(found), project)}
	}
}
