package board_test

import (
	"os"
	"testing"
)

// TestMain pins the terminal type. The renderer picks how it redraws a changed line from TERM, and the Run and
// Capture tests read the bytes it writes: under another TERM a text can reach the output in pieces.
func TestMain(m *testing.M) {
	if err := os.Setenv("TERM", "xterm-256color"); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
