package herdrconf_test

import (
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/herdrconf"
)

var fence = herdrconf.Fence{Open: "# >>> x", Close: "# <<< x"}

func place(text, body string) string {
	before, after, found := fence.Split(text)
	return fence.Place(before, body, after, found)
}

func TestPlaceAppendsTheBlockAfterABlankLineAndRewritesItInPlace(t *testing.T) {
	for _, c := range []struct{ name, text, body, want string }{
		{"empty config", "", "a = 1\n", "# >>> x\na = 1\n# <<< x\n"},
		{"no final newline", "t = 1", "a = 1\n", "t = 1\n\n# >>> x\na = 1\n# <<< x\n"},
		{"after the last line", "t = 1\n", "a = 1\n", "t = 1\n\n# >>> x\na = 1\n# <<< x\n"},
		{"in place", "t = 1\n  # >>> x\nold\n# <<< x\nu = 2\n", "a = 1\n", "t = 1\n# >>> x\na = 1\n# <<< x\nu = 2\n"},
		{"an empty body takes the block out", "t = 1\n# >>> x\nold\n# <<< x\nu = 2\n", "", "t = 1\nu = 2\n"},
		{"an open marker alone is not a block", "# >>> x\nt = 1\n", "a = 1\n", "# >>> x\nt = 1\n\n# >>> x\na = 1\n# <<< x\n"},
	} {
		if got := place(c.text, c.body); got != c.want {
			t.Errorf("%s: place(%q, %q) = %q, want %q", c.name, c.text, c.body, got, c.want)
		}
		if again := place(place(c.text, c.body), c.body); again != place(c.text, c.body) {
			t.Errorf("%s: a second place changed the text to %q", c.name, again)
		}
	}
}
