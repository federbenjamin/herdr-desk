package board_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
)

// Every field of model.Patch a set event can carry is named in the board's history: a patch that sets only that
// field renders its own text. A field added to Patch without a rendering fails here until it gets one.
func TestTaskPageHistoryNamesEveryPatchField(t *testing.T) {
	want := map[string]string{
		"status":        "blocked [mark]",
		"title":         "title [mark]",
		"notes":         "notes [mark]",
		"thread":        "thread #v-thread [mark]",
		"root":          "root v-root [mark]",
		"isolation":     "isolation v-isolation [mark]",
		"model":         "model v-model [mark]",
		"first_message": "first_message v-first_message [mark]",
		"archived":      "archived [mark]",
		"ref":           "[v-ref]",
		"merged":        "merged [mark]",
	}
	pt := reflect.TypeFor[model.Patch]()
	for i := range pt.NumField() {
		field := pt.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "notes_were" || name == "question" {
			continue // a precondition or a note's text, never stored in a set event
		}
		text, ok := want[name]
		if !ok {
			t.Errorf("Patch field %s has no history text here: name it in setText and in this test", name)
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
		task := model.Task{Number: 5, Title: "Patch", Status: model.StatusOpen}
		history := []model.Event{{TS: w4Now, Who: model.WhoUser, Kind: model.KindSet, Data: model.MustData(p)}}
		if got := w4TaskPage(t, 90, task, history).Text(); !strings.Contains(got, text) {
			t.Errorf("a set of %s alone: history lacks %q:\n%s", name, text, got)
		}
	}
}

// Clearing a task's first_message is named, not rendered as an empty value.
func TestTaskPageHistoryNamesAClearedFirstMessage(t *testing.T) {
	empty := ""
	task := model.Task{Number: 5, Title: "Patch", Status: model.StatusOpen}
	history := []model.Event{{TS: w4Now, Who: model.WhoUser, Kind: model.KindSet, Data: model.MustData(model.Patch{FirstMessage: &empty})}}
	if got := w4TaskPage(t, 90, task, history).Text(); !strings.Contains(got, "first_message cleared") {
		t.Errorf("history lacks %q:\n%s", "first_message cleared", got)
	}
}
