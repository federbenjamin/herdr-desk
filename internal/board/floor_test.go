package board_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/federbenjamin/herdr-desk/internal/board"
)

func TestViewBelowTenRowsShowsOnlyTheHeightRequirement(t *testing.T) {
	s := board.NewState(board.Config{})
	s, _ = s.Update(tea.WindowSizeMsg{Width: 80, Height: 9})
	want := "herdr-desk needs 10 rows; this one has 9"
	for _, screen := range []struct {
		name string
		text string
	}{{"Text", s.Text()}, {"Render", s.Render()}} {
		t.Run(screen.name, func(t *testing.T) {
			if screen.text != want {
				t.Fatalf("%s() = %q, want %q", screen.name, screen.text, want)
			}
			if strings.Contains(screen.text, "\n") {
				t.Fatalf("%s() drew more than the height requirement: %q", screen.name, screen.text)
			}
		})
	}

	atFloor := board.NewState(board.Config{})
	atFloor, _ = atFloor.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	if got := atFloor.Text(); got == want {
		t.Fatalf("Text() at 10 rows still reports the under-height screen")
	}
}
