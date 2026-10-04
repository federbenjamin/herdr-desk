package model_test

import (
	"testing"

	"github.com/federbenjamin/desk/internal/model"
)

func TestResolveProjectTurnsABareNameIntoTheOneKnownPath(t *testing.T) {
	known := []string{"", "/work/alpha", "/work/alpha", "/old/beta", "/new/beta"}
	for _, test := range []struct {
		project string
		want    string
		code    string
	}{
		{"", "", ""},
		{"/any/path", "/any/path", ""},
		{"alpha", "/work/alpha", ""},
		{"beta", "", model.CodeUnknownProject},
		{"gamma", "", model.CodeUnknownProject},
		{"work/alpha", "", model.CodeUnknownProject},
	} {
		got, err := model.ResolveProject(test.project, known)
		r, _ := model.AsRefusal(err)
		code := ""
		if r != nil {
			code = r.Code
		}
		if got != test.want || code != test.code || (err != nil && r == nil) {
			t.Errorf("ResolveProject(%q) = (%q, %v), want (%q, code %q)", test.project, got, err, test.want, test.code)
		}
	}
}
