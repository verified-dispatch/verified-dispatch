//go:build darwin

package procs

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
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

func TestProcTableFromKinfo(t *testing.T) {
	kp := []unix.KinfoProc{
		{Proc: unix.ExternProc{P_pid: 501, P_stat: 2, P_starttime: unix.Timeval{Sec: 1700000000, Usec: 250}}, Eproc: unix.Eproc{Ppid: 1, Pgid: 501}},
		{Proc: unix.ExternProc{P_pid: 4242, P_stat: 5, P_starttime: unix.Timeval{Sec: 1700000001, Usec: 0}}, Eproc: unix.Eproc{Ppid: 501, Pgid: 501}},
	}
	procs := fromKinfo(kp)
	a := procs[501]
	if a.ppid != 1 || a.pgid != 501 || a.zombie || a.start != 1700000000_000250 {
		t.Fatalf("501 = %+v", a)
	}
	if z := procs[4242]; !z.zombie || z.ppid != 501 {
		t.Fatalf("4242 = %+v", z)
	}
}

func TestSnapshotListsThisProcess(t *testing.T) {
	procs, err := snapshot()
	if err != nil {
		t.Fatal(err)
	}
	self, ok := procs[os.Getpid()]
	if !ok || self.ppid != os.Getppid() || self.start == 0 {
		t.Fatalf("own entry = %+v, %v", self, ok)
	}
}
