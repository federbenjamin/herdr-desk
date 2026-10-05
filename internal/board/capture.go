package board

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/federbenjamin/herdr-desk/internal/model"
)

const captureHint = "#thread  @project  ·  enter adds  ·  esc cancels"

// CaptureState is the capture popup: one line, and the last refusal.
type CaptureState struct {
	prompt  string
	in      textinput.Model
	refusal string
	width   int
	busy    bool // an AddTask is unanswered: the line is not edited or sent again until Added or Failed
	quit    bool // esc or ctrl+c came while busy: the answer ends the box
}

// NewCapture returns an empty add box whose line starts with prompt ("capture: " in the popup, "add: " on the board).
func NewCapture(prompt string) CaptureState {
	return CaptureState{prompt: prompt, in: newInput(""), width: 80}
}

// Update takes tea.KeyPressMsg, tea.PasteMsg, tea.WindowSizeMsg, Added, and Failed. Its effects are AddTask and
// Quit.
func (c CaptureState) Update(msg tea.Msg) (CaptureState, []Effect) {
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		k := m.String()
		if c.busy {
			// The task may still land, so a cancel waits for the home's answer.
			c.quit = c.quit || k == "esc" || k == "ctrl+c"
			return c, nil
		}
		switch k {
		case "ctrl+d":
		case "esc", "ctrl+c":
			return c, []Effect{Quit{}}
		case "enter":
			line := strings.TrimSpace(c.in.Value())
			if line == "" {
				return c, []Effect{Quit{}}
			}
			c.busy = true
			return c, []Effect{AddTask{Data: model.ParseCapture(line)}}
		default:
			c.in = typeInto(c.in, m)
		}
	case tea.PasteMsg:
		if !c.busy {
			c.in = typeInto(c.in, m)
		}
	case tea.WindowSizeMsg:
		c.width = max(m.Width, 1)
	case Added:
		c.busy = false
		return c, []Effect{Quit{}}
	case Failed:
		c.busy = false
		c.refusal = errText(m.Err)
		if c.quit {
			return c, []Effect{Quit{}}
		}
	}
	return c, nil
}

// Text is the popup as plain text; Render is the same with styles.
func (c CaptureState) Text() string { return strings.Join(c.lines(plain, c.width), "\n") }

// Render is the popup with styles.
func (c CaptureState) Render() string { return strings.Join(c.lines(colour, c.width), "\n") }

func (c CaptureState) lines(p palette, w int) []string {
	line := c.prompt + inputLine(p, c.in)
	if width(line) > w {
		// Keep the end of a long line in view, where the cursor is.
		line = c.prompt + "…" + ansi.TruncateLeft(inputLine(p, c.in), width(line)-w+1, "")
	}
	lines := []string{cut(line, w), cut(p.dim(captureHint), w)}
	if c.refusal != "" {
		for _, l := range strings.Split(ansi.Wrap(oneLine(c.refusal), max(w, 1), ""), "\n") {
			lines = append(lines, p.status(model.StatusBlocked, l))
		}
	}
	return lines
}
