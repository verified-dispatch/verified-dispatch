package gitx

import (
	"reflect"
	"testing"
)

func TestParseZSplitsOnNulAndDropsTrailingEmpty(t *testing.T) {
	got := ParseZ([]byte("a/b.txt\x00dir/with space\x00\x00"))
	want := []string{"a/b.txt", "dir/with space", ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseZ = %q, want %q", got, want)
	}
}

func TestParseZEmptyInputGivesNoEntries(t *testing.T) {
	if got := ParseZ(nil); len(got) != 0 {
		t.Fatalf("ParseZ(nil) = %q, want empty", got)
	}
	if got := ParseZ([]byte("\x00")); !reflect.DeepEqual(got, []string{""}) {
		t.Fatalf("ParseZ(NUL) = %q, want one empty entry", got)
	}
}

func TestParseZKeepsNewlinesInsidePaths(t *testing.T) {
	got := ParseZ([]byte("odd\nname\x00"))
	if !reflect.DeepEqual(got, []string{"odd\nname"}) {
		t.Fatalf("ParseZ = %q, want the newline kept", got)
	}
}
