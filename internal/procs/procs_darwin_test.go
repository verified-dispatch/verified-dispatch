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
