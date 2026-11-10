//go:build linux

package procs

import (
	"os"
	"strconv"
	"testing"
)

// statOf reads the fields this package needs from /proc/<pid>/stat.
func statOf(t *testing.T, pid int) procInfo {
	t.Helper()
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		t.Fatalf("stat %d: %v", pid, err)
	}
	info, ok := parseStat(pid, b)
	if !ok {
		t.Fatalf("stat %d: cannot parse %q", pid, b)
	}
	return info
}

func checkOwnSession(t *testing.T, pid int) {
	t.Helper()
	info := statOf(t, pid)
	if info.sid != pid {
		t.Fatalf("sid = %d, want the child's own pid %d", info.sid, pid)
	}
	if info.sid == statOf(t, os.Getpid()).sid {
		t.Fatal("child shares the tool's session")
	}
}

func isZombie(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	info, ok := parseStat(pid, b)
	return ok && info.zombie
}
