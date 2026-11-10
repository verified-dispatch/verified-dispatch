//go:build linux

package procs

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	return ok && info.exited()
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
	p := startFixture(t, "new-session-background.sh", Spec{Stdout: &out})
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
	line := []byte("123 (my prog) (x) S 45 67 89 0 -1 4194560 0 0 0 0 1 2 3 4 20 0 1 0 777 100\n")
	info, ok := parseStat(123, line)
	if !ok || info.pid != 123 || info.ppid != 45 || info.pgid != 67 || info.sid != 89 || info.exited() {
		t.Fatalf("parseStat = %+v, %v", info, ok)
	}
	z, ok := parseStat(5, []byte("5 (z) Z 1 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 50\n"))
	if !ok || !z.exited() {
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

func TestParseStatReadsThreadsAndStartTime(t *testing.T) {
	// pid (comm) state ppid pgrp session tty tpgid flags minflt cminflt majflt cmajflt utime stime cutime cstime prio nice threads itreal starttime
	line := []byte("123 (my prog) (x) Z 45 67 89 0 -1 4194560 0 0 0 0 1 2 3 4 20 0 2 0 987654 100\n")
	info, ok := parseStat(123, line)
	if !ok || info.threads != 2 || info.start != 987654 || info.state != 'Z' {
		t.Fatalf("parseStat = %+v, %v", info, ok)
	}
	if info.exited() {
		t.Fatal("a zombie leader with two threads is not exited")
	}
	one, _ := parseStat(5, []byte("5 (z) Z 1 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 50\n"))
	if !one.exited() {
		t.Fatal("a one-thread zombie is exited")
	}
}

func TestDescendantsSeesAProcessWhoseMainThreadExited(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "counter")
	var out bytes.Buffer
	p := startFixture(t, "thread-hider.sh", Spec{Stdout: &out, Env: baseEnv("PROCS_HELPER=threadexit", "PROCS_COUNTER="+counter)})
	waitFor(t, p, 10*time.Second)
	hider, err := strconv.Atoi(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatalf("stdout = %q", out.String())
	}
	// Wait until the helper has ended its main thread.
	deadline := time.Now().Add(5 * time.Second)
	for statOf(t, hider).state != 'Z' {
		if time.Now().After(deadline) {
			t.Fatalf("helper %d never showed state Z: %+v", hider, statOf(t, hider))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if statOf(t, hider).threads < 2 {
		t.Fatalf("helper %d has %d threads; the test needs a running worker", hider, statOf(t, hider).threads)
	}
	ds := descendantsSoon(t, p, hider, 3*time.Second)
	if !contains(ds, hider) {
		t.Fatalf("Descendants = %v, want the hidden process %d", ds, hider)
	}
	before := readCounter(t, counter)
	res := p.Teardown(quick)
	if !res.Complete || res.Leftovers != 1 {
		t.Fatalf("Teardown = %+v", res)
	}
	time.Sleep(200 * time.Millisecond)
	after := readCounter(t, counter)
	time.Sleep(200 * time.Millisecond)
	if readCounter(t, counter) != after || after < before {
		t.Fatalf("counter still moving after teardown: %d -> %d -> %d", before, after, readCounter(t, counter))
	}
}

func readCounter(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

func TestDescendantsIgnoresAReusedChildPid(t *testing.T) {
	// A Process whose recorded start time does not match the process now
	// holding its pid must treat the child as gone and seed nothing.
	stranger := startFixture(t, "sleep-long.sh", Spec{})
	fake := &Process{pid: stranger.Pid(), selfPid: os.Getpid(), done: make(chan struct{}), release: make(chan struct{})}
	close(fake.done)
	fake.tr = &tracker{pid: stranger.Pid(), start: statOf(t, stranger.Pid()).start + 1, known: true, tracked: map[int]uint64{}}
	ds, err := fake.Descendants()
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 0 {
		t.Fatalf("Descendants = %v for a reused pid, want none", ds)
	}
	res := fake.Teardown(quick)
	if res.Leftovers != 0 || !alive(stranger.Pid()) {
		t.Fatalf("Teardown on a reused pid touched the stranger: %+v alive=%v", res, alive(stranger.Pid()))
	}
}

func TestTeardownReportsAProcessThatKeepsChangingItsPid(t *testing.T) {
	dir := t.TempDir()
	stop := filepath.Join(dir, "stop")
	pidfile := filepath.Join(dir, "pid")
	p := startFixture(t, "hopper.sh", Spec{Env: baseEnv("PROCS_HELPER=hopper", "PROCS_STOP="+stop, "PROCS_PIDFILE="+pidfile)})
	waitFor(t, p, 10*time.Second)
	// Let it hop for a moment.
	deadline := time.Now().Add(5 * time.Second)
	for readCounter(t, pidfile) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	res := p.Teardown(TeardownOptions{TermGrace: 100 * time.Millisecond, Bound: 2 * time.Second})
	if res.Complete {
		// A hop that is caught alive is killed and the chain ends. That is
		// only honest if the hopper really stopped.
		if !hopperStopped(t, pidfile) {
			t.Fatalf("result = %+v but the hopper still runs; a pid-changing process must never be declared gone", res)
		}
	} else if !res.Unsettled {
		t.Fatalf("result = %+v; an incomplete teardown of a pid-changing process must be unsettled", res)
	}
	// Stop the hopper and let the last hop exit and be reaped.
	if err := os.WriteFile(stop, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, _ = p.Descendants() // reaps hops parented here
		if hopperStopped(t, pidfile) {
			return
		}
	}
	t.Fatal("hopper did not stop")
}

// hopperStopped reports whether no hop recorded itself for 300 ms and the
// last recorded hop is gone. Each hop writes its pid once as it starts; a
// hop killed during that write leaves the file empty, which reads as 0 and
// must not be passed to alive, since signal 0 to pid 0 reaches this
// process's own group.
func hopperStopped(t *testing.T, pidfile string) bool {
	t.Helper()
	last := readCounter(t, pidfile)
	time.Sleep(300 * time.Millisecond)
	cur := readCounter(t, pidfile)
	return cur == last && (cur == 0 || !alive(cur))
}

func TestKillThroughPidfdRefusesAMismatchedStartTime(t *testing.T) {
	p := startFixture(t, "sleep-long.sh", Spec{})
	p.tr.mu.Lock()
	p.tr.tracked[p.Pid()] = statOf(t, p.Pid()).start + 1 // not this process
	p.tr.mu.Unlock()
	if err := p.tr.kill(p.Pid(), syscall.SIGKILL); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("kill with a mismatched start = %v, want ESRCH", err)
	}
	if !alive(p.Pid()) {
		t.Fatal("the child was signalled despite the mismatch")
	}
	p.tr.mu.Lock()
	p.tr.tracked[p.Pid()] = statOf(t, p.Pid()).start
	p.tr.mu.Unlock()
	if err := p.tr.kill(p.Pid(), syscall.SIGKILL); err != nil {
		t.Fatalf("kill = %v", err)
	}
	if st := waitFor(t, p, 5*time.Second); !st.Signaled || st.Signal != syscall.SIGKILL {
		t.Fatalf("status = %s", st)
	}
}
