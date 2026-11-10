//go:build linux

package procs

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
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

func TestDescendantsFindsAProcessThatChangedSession(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("no setsid binary")
	}
	var out bytes.Buffer
	p := startFixture(t, "escape-setsid.sh", Spec{Stdout: &out})
	waitFor(t, p, 10*time.Second)
	escaped, err := strconv.Atoi(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatalf("stdout = %q", out.String())
	}
	// The escaped process is outside the child's group, so a group kill
	// misses it. Left alive, it stays parented here from a foreign session
	// and every later child would list it as a descendant.
	t.Cleanup(func() { _ = syscall.Kill(escaped, syscall.SIGKILL) })
	// The fixture can exit before its background job has called setsid,
	// so wait for the session to change instead of reading it once.
	selfSid := statOf(t, os.Getpid()).sid
	deadline := time.Now().Add(3 * time.Second)
	for {
		info := statOf(t, escaped)
		if info.sid != p.Pid() && info.sid != selfSid {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture did not change session: sid %d", info.sid)
		}
		time.Sleep(20 * time.Millisecond)
	}
	ds := descendantsSoon(t, p, escaped, 3*time.Second)
	if !contains(ds, escaped) {
		t.Fatalf("Descendants = %v, want the reparented process %d", ds, escaped)
	}
}

func TestParseStatHandlesSpacesAndParenthesesInTheName(t *testing.T) {
	line := []byte("123 (my prog) (x) S 45 67 89 0 -1 4194560 0 0 0 0\n")
	info, ok := parseStat(123, line)
	if !ok || info.pid != 123 || info.ppid != 45 || info.pgid != 67 || info.sid != 89 || info.zombie {
		t.Fatalf("parseStat = %+v, %v", info, ok)
	}
	z, ok := parseStat(5, []byte("5 (z) Z 1 1 1\n"))
	if !ok || !z.zombie {
		t.Fatalf("zombie: %+v, %v", z, ok)
	}
	if _, ok := parseStat(5, []byte("garbage")); ok {
		t.Fatal("garbage parsed")
	}
}

func TestDescendantsReapsAZombieTheToolNowParents(t *testing.T) {
	var out bytes.Buffer
	p := startFixture(t, "leave-background.sh", Spec{Stdout: &out})
	waitFor(t, p, 10*time.Second)
	left, _ := strconv.Atoi(strings.TrimSpace(out.String()))
	descendantsSoon(t, p, left, 3*time.Second)
	_ = syscall.Kill(left, syscall.SIGKILL)
	// Killed and reparented here, it is a zombie until reaped. Descendants
	// must reap it and stop listing it.
	deadline := time.Now().Add(3 * time.Second)
	for {
		ds, err := p.Descendants()
		if err != nil {
			t.Fatal(err)
		}
		if !contains(ds, left) && syscall.Kill(left, 0) != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("zombie %d still listed or still present: %v", left, ds)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
