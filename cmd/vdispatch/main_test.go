package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCommandPrintsVersionAndExitsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "vdispatch ") {
		t.Fatalf("stdout = %q, want prefix %q", stdout.String(), "vdispatch ")
	}
}

func TestUnknownCommandPrintsUsageAndExitsOne(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"bogus"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), `unknown command "bogus"`) {
		t.Fatalf("stderr = %q, want it to name the unknown command", stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: vdispatch") {
		t.Fatalf("stderr = %q, want usage text", stderr.String())
	}
}

func TestNoArgumentsPrintsUsageAndExitsOne(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "usage: vdispatch") {
		t.Fatalf("stderr = %q, want usage text", stderr.String())
	}
}

func TestKnownCommandsAreStubsThatExitOne(t *testing.T) {
	for _, name := range []string{"run", "wait", "show", "log", "list", "check", "prune", "unlock"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{name}, &stdout, &stderr); code != 1 {
			t.Errorf("%s: exit code = %d, want 1", name, code)
		}
		if !strings.Contains(stderr.String(), "not implemented") {
			t.Errorf("%s: stderr = %q, want a not-implemented message", name, stderr.String())
		}
	}
}
