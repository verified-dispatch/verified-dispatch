//go:build darwin

package procs

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// checkOwnSession has no portable session id on macOS (ps prints a kernel
// pointer for sess), so it checks the process group, which Start sets
// through the same setsid call.
func checkOwnSession(t *testing.T, pid int) {
	t.Helper()
	out, err := exec.Command("ps", "-o", "pgid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	if strings.TrimSpace(string(out)) != strconv.Itoa(pid) {
		t.Fatalf("ps pgid = %q, want %d", out, pid)
	}
}

func isZombie(pid int) bool {
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
}

// checkAttachDisabled has nothing to read back on macOS; PT_DENY_ATTACH
// returning without error is the check.
func checkAttachDisabled() error { return nil }

func TestParsePsLineReadsStartTime(t *testing.T) {
	procs := parsePs([]byte("  501     1   501 Ss   Mon Nov 10 09:15:02 2026\n 4242   501  4242 Z    Mon Nov 10 09:15:03 2026\n"))
	a, ok := procs[501]
	if !ok || a.ppid != 1 || a.pgid != 501 || a.start != "Mon Nov 10 09:15:02 2026" || a.zombie {
		t.Fatalf("501 = %+v, %v", a, ok)
	}
	if z := procs[4242]; !z.zombie {
		t.Fatalf("4242 = %+v", z)
	}
}
