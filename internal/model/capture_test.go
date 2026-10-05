package model_test

import (
	"reflect"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
)

func TestW1ParseCaptureSeparatesTagsAndKeepsBareMarkersInTheTitle(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
		want model.TaskData
	}{
		{
			name: "normal capture with whitespace",
			line: "  repair the board  #ops  @/work/desk ",
			want: model.TaskData{Title: "repair the board", Thread: "ops", Project: "/work/desk"},
		},
		{
			name: "last tag of each kind wins",
			line: "write the release notes #draft @old #release @desk",
			want: model.TaskData{Title: "write the release notes", Thread: "release", Project: "desk"},
		},
		{
			name: "bare markers are title words",
			line: "# @ #ops @project",
			want: model.TaskData{Title: "# @", Thread: "ops", Project: "project"},
		},
		{
			name: "only tags leave an empty title",
			line: "#ops @desk",
			want: model.TaskData{Thread: "ops", Project: "desk"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := model.ParseCapture(tc.line); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseCapture(%q) = %#v, want %#v", tc.line, got, tc.want)
			}
		})
	}
}
