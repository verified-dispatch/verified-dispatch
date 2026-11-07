package gitx

import (
	"context"
	"errors"
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

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in           string
		major, minor int
		ok           bool
	}{
		{"git version 2.44.0\n", 2, 44, true},
		{"git version 2.56.0.rc1", 2, 56, true},
		{"git version 2.39.5 (Apple Git-154)", 2, 39, true},
		{"git version 3.0.0", 3, 0, true},
		{"not git", 0, 0, false},
		{"git version 2", 0, 0, false},
	}
	for _, c := range cases {
		major, minor, err := ParseVersion(c.in)
		if (err == nil) != c.ok {
			t.Errorf("ParseVersion(%q) err = %v, want ok=%v", c.in, err, c.ok)
			continue
		}
		if major != c.major || minor != c.minor {
			t.Errorf("ParseVersion(%q) = %d.%d, want %d.%d", c.in, major, minor, c.major, c.minor)
		}
	}
}

func TestCheckVersionAgainstFloor(t *testing.T) {
	cases := []struct {
		major, minor int
		ok           bool
	}{
		{2, 44, true}, {2, 45, true}, {3, 0, true}, {2, 43, false}, {1, 99, false},
	}
	for _, c := range cases {
		err := checkFloor(c.major, c.minor)
		if (err == nil) != c.ok {
			t.Errorf("checkFloor(%d.%d) err = %v, want ok=%v", c.major, c.minor, err, c.ok)
		}
	}
}

func TestCheckVersionUsesInstalledGit(t *testing.T) {
	if err := (Runner{}).CheckVersion(context.Background()); err != nil {
		t.Fatalf("CheckVersion: %v (the installed git must be at least %d.%d for the suite)", err, FloorMajor, FloorMinor)
	}
}

func TestCheckVersionRefusesOldGit(t *testing.T) {
	fakeGitOnPath(t, `echo "git version 2.43.0"`)
	err := (Runner{}).CheckVersion(context.Background())
	if !errors.Is(err, ErrVersionTooOld) {
		t.Fatalf("err = %v, want ErrVersionTooOld", err)
	}
}
