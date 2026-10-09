package worker_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/worker"
)

// Every field of model.Patch a set event can carry is named in the first message's history, as the board names it:
// a patch that sets only that field renders its own line. A field added to Patch without a rendering fails here.
func TestFirstMessageHistoryNamesEveryPatchField(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"status":        "user: status blocked (mark)",
		"title":         "user: changed title (mark)",
		"notes":         "user: changed notes (mark)",
		"thread":        "user: changed thread (mark)",
		"root":          "user: changed root (mark)",
		"isolation":     "user: changed isolation (mark)",
		"model":         "user: changed model (mark)",
		"first_message": "user: changed first_message (mark)",
		"archived":      "user: changed archived (mark)",
		"ref":           "user: changed (v-ref)",
		"merged":        "user: changed merged (mark)",
	}
	pt := reflect.TypeFor[model.Patch]()
	for i := range pt.NumField() {
		field := pt.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "notes_were" || name == "question" {
			continue // a precondition or a note's text, never stored in a set event
		}
		line, ok := want[name]
		if !ok {
			t.Errorf("Patch field %s has no history line here: name it in patchFields and in this test", name)
			continue
		}
		p := model.Patch{Ref: "mark"}
		v := reflect.ValueOf(&p).Elem().Field(i)
		switch {
		case name == "status":
			st := model.StatusBlocked
			v.Set(reflect.ValueOf(&st))
		case name == "ref":
			p.Ref = "v-ref"
		case v.Kind() == reflect.Bool:
			v.SetBool(true)
		case field.Type == reflect.TypeFor[*string]():
			s := "v-" + name
			v.Set(reflect.ValueOf(&s))
		case field.Type == reflect.TypeFor[*bool]():
			b := true
			v.Set(reflect.ValueOf(&b))
		default:
			t.Fatalf("Patch field %s has type %s, which this test cannot set", name, field.Type)
		}
		ts := time.Date(2026, time.October, 4, 14, 2, 0, 0, time.UTC)
		d := store.TaskDetail{Task: model.Task{Number: 3, Title: "Patch"},
			History: []model.Event{{TS: ts, Kind: model.KindSet, Data: model.MustData(p)}}}
		if got := worker.FirstMessage(d, false); !strings.Contains(got, "- 2026-10-04 14:02Z "+line+"\n") {
			t.Errorf("a set of %s alone: message lacks the line %q:\n%s", name, line, got)
		}
	}
}
