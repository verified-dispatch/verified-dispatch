//go:build linux

package procs

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// needCgroup skips unless the test process can create child cgroups in
// its own cgroup. CI runs the suite once with and once without that.
func needCgroup(t *testing.T) string {
	t.Helper()
	own, err := ownCgroupDir()
	if err != nil {
		t.Skipf("no cgroup v2 for this process: %v", err)
	}
	probe := filepath.Join(own, "vdispatch-probe-"+strconv.Itoa(os.Getpid()))
	if err := os.Mkdir(probe, 0o755); err != nil {
		t.Skipf("own cgroup %s is not writable: %v", own, err)
	}
	_ = os.Remove(probe)
	return own
}

func TestOwnCgroupDirParsesTheUnifiedLine(t *testing.T) {
	dir, err := parseCgroupFile([]byte("0::/user.slice/user-1000.slice/session-3.scope\n"))
	if err != nil || dir != "/sys/fs/cgroup/user.slice/user-1000.slice/session-3.scope" {
		t.Fatalf("dir = %q, err = %v", dir, err)
	}
	if _, err := parseCgroupFile([]byte("12:memory:/foo\n1:name=systemd:/bar\n")); err == nil {
		t.Fatal("a v1-only file parsed as v2")
	}
	if _, err := parseCgroupFile([]byte("")); err == nil {
		t.Fatal("an empty file parsed")
	}
}

func TestChildStartsInsideItsOwnCgroup(t *testing.T) {
	own := needCgroup(t)
	p := startFixture(t, "sleep-long.sh", Spec{})
	if p.cg == nil {
		t.Fatal("no cgroup was used although the own cgroup is writable")
	}
	if !strings.HasPrefix(p.cg.dir, own+"/vdispatch-") {
		t.Fatalf("cgroup dir = %q, want under %q", p.cg.dir, own)
	}
	members, err := p.cg.members()
	if err != nil {
		t.Fatal(err)
	}
	if !contains(members, p.Pid()) {
		t.Fatalf("members = %v, want the child %d", members, p.Pid())
	}
	if !strings.Contains(p.Containment(), "cgroup") {
		t.Fatalf("Containment() = %q", p.Containment())
	}
	res := p.Teardown(quick)
	if !res.Complete {
		t.Fatalf("result = %+v", res)
	}
	if _, err := os.Stat(p.cg.dir); !os.IsNotExist(err) {
		t.Fatalf("cgroup dir %s still exists after teardown: %v", p.cg.dir, err)
	}
}

func TestCgroupScanFindsAProcessThatChangedSession(t *testing.T) {
	needCgroup(t)
	var out bytes.Buffer
	p := startFixture(t, "new-session-background.sh", Spec{Stdout: &out})
	waitFor(t, p, 10*time.Second)
	escaped, err := strconv.Atoi(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatalf("stdout = %q", out.String())
	}
	members, err := p.cg.members()
	if err != nil {
		t.Fatal(err)
	}
	if !contains(members, escaped) {
		t.Fatalf("cgroup members = %v, want %d", members, escaped)
	}
	ds := descendantsSoon(t, p, escaped, 3*time.Second)
	if !contains(ds, escaped) {
		t.Fatalf("Descendants = %v, want %d", ds, escaped)
	}
}

func TestCgroupTeardownEndsAProcessThatKeepsChangingItsPid(t *testing.T) {
	needCgroup(t)
	dir := t.TempDir()
	stop := filepath.Join(dir, "stop")
	pidfile := filepath.Join(dir, "pid")
	p := startFixture(t, "hopper.sh", Spec{Env: baseEnv("PROCS_HELPER=hopper", "PROCS_STOP="+stop, "PROCS_PIDFILE="+pidfile)})
	waitFor(t, p, 10*time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for readCounter(t, pidfile) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	res := p.Teardown(TeardownOptions{TermGrace: 100 * time.Millisecond, Bound: 5 * time.Second})
	if !res.Complete {
		t.Fatalf("result = %+v; with a cgroup the hopper must die", res)
	}
	time.Sleep(300 * time.Millisecond)
	last := readCounter(t, pidfile)
	time.Sleep(300 * time.Millisecond)
	if cur := readCounter(t, pidfile); cur != last || (cur != 0 && alive(cur)) {
		t.Fatalf("hopper still hopping after teardown: %d -> %d", last, cur)
	}
}

func TestCgroupTeardownCountsLeftovers(t *testing.T) {
	needCgroup(t)
	var out bytes.Buffer
	p := startFixture(t, "leave-background.sh", Spec{Stdout: &out})
	waitFor(t, p, 10*time.Second)
	left, _ := strconv.Atoi(strings.TrimSpace(out.String()))
	res := p.Teardown(quick)
	if !res.Complete || res.Leftovers != 1 {
		t.Fatalf("result = %+v, want one leftover", res)
	}
	if !gone(left, 2*time.Second) {
		t.Fatalf("leftover %d still alive", left)
	}
}

func TestWithoutACgroupTheContainmentLineSaysSo(t *testing.T) {
	p := startFixture(t, "exit-code.sh", Spec{})
	waitFor(t, p, 10*time.Second)
	c := p.Containment()
	if p.cg == nil && !strings.Contains(c, "no cgroup") {
		t.Fatalf("Containment() = %q, want it to say no cgroup was available", c)
	}
	if p.cg != nil && !strings.Contains(c, "cgroup") {
		t.Fatalf("Containment() = %q", c)
	}
	_ = syscall.Getpid()
}
