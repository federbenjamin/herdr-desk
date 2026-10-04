package main

import (
	"errors"
	"strings"
	"testing"
)

func TestWorkingDirWarnsWhenTheDirectoryCannotBeRead(t *testing.T) {
	var stderr strings.Builder
	got := workingDir(func() (string, error) { return "", errors.New("getwd: no such file or directory") }, &stderr)
	if got != "" {
		t.Fatalf("workingDir() = %q, want empty", got)
	}
	if !strings.Contains(stderr.String(), "getwd: no such file or directory") {
		t.Fatalf("stderr = %q, want the cause", stderr.String())
	}

	stderr.Reset()
	if got := workingDir(func() (string, error) { return "/work/repo", nil }, &stderr); got != "/work/repo" || stderr.Len() != 0 {
		t.Fatalf("workingDir() = %q with stderr %q, want /work/repo and no warning", got, stderr.String())
	}
}
