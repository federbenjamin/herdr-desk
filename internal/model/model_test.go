package model_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
)

func TestParseStatusAcceptsOnlyPublishedStatuses(t *testing.T) {
	t.Parallel()

	for _, want := range []model.Status{
		model.StatusOpen,
		model.StatusReady,
		model.StatusStarted,
		model.StatusBlocked,
		model.StatusReview,
		model.StatusDone,
	} {
		got, ok := model.ParseStatus(string(want))
		if !ok || got != want {
			t.Errorf("ParseStatus(%q) = %q, %t; want %q, true", want, got, ok, want)
		}
	}

	for _, input := range []string{"", "Open", "archived", "cancelled", " done"} {
		if got, ok := model.ParseStatus(input); ok || got != "" {
			t.Errorf("ParseStatus(%q) = %q, %t; want empty, false", input, got, ok)
		}
	}
}

func TestValidIsolationAcceptsOnlyPublishedValues(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "self", "worktree", "in-place"} {
		if !model.ValidIsolation(value) {
			t.Errorf("ValidIsolation(%q) = false; want true", value)
		}
	}
	for _, value := range []string{"Self", "container", " worktree", "in place"} {
		if model.ValidIsolation(value) {
			t.Errorf("ValidIsolation(%q) = true; want false", value)
		}
	}
}

func TestIsolationsListsTheValidOnesInOrderAndIsACopy(t *testing.T) {
	t.Parallel()

	got := model.Isolations()
	if want := []string{"", "self", "worktree", "in-place"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Isolations() = %q, want %q", got, want)
	}
	for _, value := range got {
		if !model.ValidIsolation(value) {
			t.Errorf("Isolations() holds %q, which ValidIsolation refuses", value)
		}
	}
	got[1] = "container"
	if model.ValidIsolation("container") || model.Isolations()[1] != "self" {
		t.Error("changing the returned slice changed the isolations")
	}
}

func TestOnMergedStatusMapsReviewAndDoneOnly(t *testing.T) {
	t.Parallel()

	for input, want := range map[string]model.Status{"": model.StatusReview, "review": model.StatusReview, "done": model.StatusDone} {
		if got, ok := model.OnMergedStatus(input); !ok || got != want {
			t.Errorf("OnMergedStatus(%q) = %q, %t; want %q, true", input, got, ok, want)
		}
	}
	for _, input := range []string{"open", "started", "Done", " review"} {
		if got, ok := model.OnMergedStatus(input); ok || got != "" {
			t.Errorf("OnMergedStatus(%q) = %q, %t; want empty, false", input, got, ok)
		}
	}
}

func TestAsRefusalFindsWrappedRefusalAndRejectsOtherErrors(t *testing.T) {
	t.Parallel()

	want := &model.Refusal{Code: model.CodeSecretDetected, Msg: "credential found"}
	got, ok := model.AsRefusal(fmt.Errorf("request failed: %w", want))
	if !ok || got != want {
		t.Fatalf("AsRefusal(wrapped refusal) = %#v, %t; want original refusal, true", got, ok)
	}
	if got.Error() != "secret-detected: credential found" {
		errorf := got.Error()
		t.Errorf("Refusal.Error() = %q", errorf)
	}

	if got, ok := model.AsRefusal(errors.New("ordinary failure")); ok || got != nil {
		t.Errorf("AsRefusal(ordinary error) = %#v, %t; want nil, false", got, ok)
	}
}

func TestValidSessionIDEnforcesAlphabetAndLengthBoundaries(t *testing.T) {
	t.Parallel()

	valid := []string{"a", "A9._-", strings.Repeat("a", 128)}
	for _, input := range valid {
		if !model.ValidSessionID(input) {
			t.Errorf("ValidSessionID(%q) = false; want true", input)
		}
	}

	invalid := []string{
		"", ".", "..", "has space", "has/slash", "has:colon", "é", strings.Repeat("a", 129),
	}
	for _, input := range invalid {
		if model.ValidSessionID(input) {
			t.Errorf("ValidSessionID(%q) = true; want false", input)
		}
	}
}

func TestBranchTagsRoundTripWithoutConfusingOtherTags(t *testing.T) {
	t.Parallel()

	tags := []string{"kind:task", model.BranchTag("feature/desk"), "branch:later"}
	if got := model.BranchOf(tags); got != "feature/desk" {
		t.Errorf("BranchOf(%q) = %q; want feature/desk", tags, got)
	}
	if got := model.BranchOf([]string{"kind:task", "branch:"}); got != "" {
		t.Errorf("BranchOf without a branch = %q; want empty", got)
	}
	if got := model.BranchOf([]string{"kind:task", "scope:session"}); got != "" {
		t.Errorf("BranchOf(tags without a branch tag) = %q; want empty", got)
	}
}

func TestMustDataMarshalsPayloadAndPanicsWhenJSONCannotRepresentIt(t *testing.T) {
	t.Parallel()

	got := model.MustData(model.NoteData{Text: "keep this"})
	want, err := json.Marshal(model.NoteData{Text: "keep this"})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("MustData() = %s; want %s", got, want)
	}

	defer func() {
		if recover() == nil {
			t.Error("MustData(value with a function) did not panic")
		}
	}()
	model.MustData(struct{ Fn func() }{})
}
