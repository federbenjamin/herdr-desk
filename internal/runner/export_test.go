package runner

import (
	"github.com/federbenjamin/herdr-desk/internal/herdr"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

func Outcome(run model.Run, pane herdr.Pane, found, wrote bool) (store.HandBack, bool) {
	return outcome(run, pane, found, wrote)
}
