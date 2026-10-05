package runner_test

import (
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/runner"
)

func TestSlugMakesBoundedASCIIBranchNameParts(t *testing.T) {
	t.Parallel()

	fortyOne := strings.Repeat("a", 40) + "!"
	cases := []struct {
		name  string
		title string
		want  string
	}{
		{name: "lower cases letters", title: "Ship The Fix", want: "ship-the-fix"},
		{name: "collapses punctuation whitespace and symbols", title: "one---two / three", want: "one-two-three"},
		{name: "removes separators at both ends", title: " !! launch now ?? ", want: "launch-now"},
		{name: "does not retain non ASCII letters", title: "Café 東京 42", want: "caf-42"},
		{name: "cuts at forty characters without a trailing separator", title: fortyOne + " release", want: strings.Repeat("a", 40)},
		{name: "returns empty for symbols only", title: "--- 🚀 !!!", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runner.Slug(tc.title); got != tc.want {
				t.Errorf("Slug(%q) = %q, want %q", tc.title, got, tc.want)
			}
		})
	}
}
