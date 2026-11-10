//go:build linux

package procs

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
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

// checkAttachDisabled reads the dumpable flag back; 0 means a same-user
// debugger cannot attach and no core is written.
func checkAttachDisabled() error {
	v, err := dumpable()
	if err != nil {
		return err
	}
	if v != 0 {
		return fmt.Errorf("dumpable = %d, want 0", v)
	}
	return nil
}

func TestChildDiesWithTheTool(t *testing.T) {
	helper, out := runHelper(t, "pdeathsig", "sleep-long.sh")
	child := readPid(t, out)
	if !alive(child) {
		t.Fatal("child not running")
	}
	if err := helper.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = helper.Wait()
	if !gone(child, 3*time.Second) {
		_ = syscall.Kill(child, syscall.SIGKILL)
		t.Fatal("child outlived the tool; the parent-death signal did not fire")
	}
}

func TestOrphansAreReparentedToTheTool(t *testing.T) {
	var out bytes.Buffer
	p := startFixture(t, "leave-background.sh", Spec{Stdout: &out})
	waitFor(t, p, 10*time.Second)
	orphan, err := strconv.Atoi(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatalf("stdout = %q", out.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if statOf(t, orphan).ppid == os.Getpid() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("orphan %d has ppid %d, want this process %d", orphan, statOf(t, orphan).ppid, os.Getpid())
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(orphan, syscall.SIGKILL)
}
