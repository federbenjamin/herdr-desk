package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestIsTerminalIsFalseForNullDeviceAndAFile(t *testing.T) {
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	if isTerminal(devNull) {
		t.Errorf("isTerminal(%s) = true, want false: it is a character device, not a terminal", os.DevNull)
	}
	file, err := os.CreateTemp(t.TempDir(), "not-a-terminal")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if isTerminal(file) {
		t.Error("isTerminal(a regular file) = true, want false")
	}
}

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
