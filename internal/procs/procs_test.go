package procs

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fixture returns the absolute path of a script under testdata/builders.
func fixture(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "testdata", "builders", name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return p
}

// baseEnv is the smallest environment the fixtures need: a PATH so that
// /usr/bin/env can find bash and the scripts can find sleep, cat, and ps.
func baseEnv(extra ...string) []string {
	return append([]string{"PATH=" + os.Getenv("PATH")}, extra...)
}

func startFixture(t *testing.T, name string, spec Spec) *Process {
	t.Helper()
	spec.Argv = []string{"bash", fixture(t, name)}
	if spec.Env == nil {
		spec.Env = baseEnv()
	}
	p, err := Start(spec)
	if err != nil {
		t.Fatalf("Start(%s): %v", name, err)
	}
	t.Cleanup(func() { p.Teardown(TeardownOptions{TermGrace: 200 * time.Millisecond, Bound: 5 * time.Second}) })
	return p
}

func waitFor(t *testing.T, p *Process, d time.Duration) Status {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	st, err := p.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	return st
}

// alive reports whether a process with this pid exists and is not a zombie.
func alive(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	return !isZombie(pid)
}

func TestStartRejectsEmptyArgv(t *testing.T) {
	if _, err := Start(Spec{}); err == nil {
		t.Fatal("Start with no argv succeeded")
	}
}

func TestStartFeedsStdinThenEndOfFile(t *testing.T) {
	var out bytes.Buffer
	p := startFixture(t, "echo-stdin.sh", Spec{Stdin: strings.NewReader("the prompt\nline two\n"), Stdout: &out})
	if st := waitFor(t, p, 10*time.Second); st.ExitCode != 0 || st.Signaled {
		t.Fatalf("status = %v", st)
	}
	if out.String() != "the prompt\nline two\n" {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestStartWithNoStdinGivesEndOfFileAtOnce(t *testing.T) {
	var out bytes.Buffer
	p := startFixture(t, "echo-stdin.sh", Spec{Stdout: &out})
	if st := waitFor(t, p, 10*time.Second); st.ExitCode != 0 {
		t.Fatalf("status = %v", st)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want nothing from /dev/null", out.String())
	}
}

func TestStartPassesExactlyTheGivenEnvironment(t *testing.T) {
	t.Setenv("ABSENT", "leaked from the tool")
	var out bytes.Buffer
	p := startFixture(t, "env.sh", Spec{Env: baseEnv("PROBE=hello"), Stdout: &out})
	waitFor(t, p, 10*time.Second)
	if out.String() != "hello\nunset\n" {
		t.Fatalf("stdout = %q, want the probe and no inherited variable", out.String())
	}
}

func TestStartWithNilEnvGivesAnEmptyEnvironment(t *testing.T) {
	t.Setenv("ABSENT", "leaked from the tool")
	var out bytes.Buffer
	// env.sh is started through an absolute bash so no PATH is needed.
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	p, err := Start(Spec{Argv: []string{bash, fixture(t, "env.sh")}, Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Teardown(TeardownOptions{TermGrace: 200 * time.Millisecond, Bound: 5 * time.Second}) })
	waitFor(t, p, 10*time.Second)
	if out.String() != "unset\nunset\n" {
		t.Fatalf("stdout = %q, want nothing inherited", out.String())
	}
}

func TestStartRunsInTheGivenDirectory(t *testing.T) {
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	p := startFixture(t, "pwd.sh", Spec{Dir: dir, Stdout: &out})
	waitFor(t, p, 10*time.Second)
	if strings.TrimSpace(out.String()) != real {
		t.Fatalf("pwd = %q, want %q", out.String(), real)
	}
}

func TestStatusReportsExitCodeAndSignal(t *testing.T) {
	p := startFixture(t, "exit-code.sh", Spec{})
	if st := waitFor(t, p, 10*time.Second); st.ExitCode != 3 || st.Signaled || st.String() != "exit 3" {
		t.Fatalf("status = %+v (%s)", st, st)
	}
	p = startFixture(t, "self-term.sh", Spec{})
	if st := waitFor(t, p, 10*time.Second); !st.Signaled || st.Signal != syscall.SIGTERM || st.String() != "signal terminated" {
		t.Fatalf("status = %+v (%s)", st, st)
	}
}

func TestStartPutsTheChildInItsOwnSessionAndGroup(t *testing.T) {
	p := startFixture(t, "sleep-long.sh", Spec{})
	pgid, err := syscall.Getpgid(p.Pid())
	if err != nil {
		t.Fatal(err)
	}
	if pgid != p.Pid() {
		t.Fatalf("pgid = %d, want the child's own pid %d", pgid, p.Pid())
	}
	if pgid == syscall.Getpgrp() {
		t.Fatal("child shares the tool's process group")
	}
	checkOwnSession(t, p.Pid())
}

func TestWaitHonorsContext(t *testing.T) {
	p := startFixture(t, "sleep-long.sh", Spec{})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := p.Wait(ctx); err != context.DeadlineExceeded {
		t.Fatalf("Wait = %v, want DeadlineExceeded", err)
	}
	if p.Exited() {
		t.Fatal("Exited true while the child runs")
	}
}

func TestWaitReturnsWhenAGrandchildHoldsStdout(t *testing.T) {
	var out bytes.Buffer
	p := startFixture(t, "hold-stdout.sh", Spec{Stdout: &out})
	start := time.Now()
	st := waitFor(t, p, 20*time.Second)
	if st.ExitCode != 0 || st.Signaled {
		t.Fatalf("status = %v", st)
	}
	if d := time.Since(start); d > waitDelay+5*time.Second {
		t.Fatalf("Wait took %s; the held pipe must be cut after %s", d, waitDelay)
	}
	holder, err := strconv.Atoi(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatalf("stdout = %q, want the grandchild pid", out.String())
	}
	if !alive(holder) {
		t.Fatal("the grandchild should still be alive before teardown")
	}
}

func TestCappedOutputDoesNotBlockTheChild(t *testing.T) {
	var sink bytes.Buffer
	c := NewCappedWriter(&sink, 1000)
	p := startFixture(t, "flood.sh", Spec{Stdout: c})
	if st := waitFor(t, p, 20*time.Second); st.ExitCode != 0 {
		t.Fatalf("status = %v", st)
	}
	if sink.Len() != 1000 || c.Seen() != 1<<20 {
		t.Fatalf("stored %d, seen %d; want 1000 and 1048576", sink.Len(), c.Seen())
	}
	select {
	case <-c.Capped():
	default:
		t.Fatal("Capped not closed")
	}
}

// runHelper re-executes the test binary in a helper mode with the given
// fixture and returns the started command and its stdout pipe.
func runHelper(t *testing.T, mode, fixtureName string) (*exec.Cmd, *bufio.Reader) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "PROCS_HELPER="+mode, "PROCS_FIXTURE="+fixture(t, fixtureName))
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd, bufio.NewReader(out)
}

func readPid(t *testing.T, r *bufio.Reader) int {
	t.Helper()
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("reading pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("pid line %q", line)
	}
	return pid
}

// gone waits up to d for pid to disappear or become a zombie.
func gone(pid int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func TestContainmentNamesThePlatformMechanism(t *testing.T) {
	c := Containment()
	if !strings.HasPrefix(c, "containment: ") {
		t.Fatalf("Containment() = %q", c)
	}
	switch runtime.GOOS {
	case "linux":
		if !strings.Contains(c, "subreaper") || !strings.Contains(c, "parent-death") {
			t.Fatalf("Containment() = %q", c)
		}
	case "darwin":
		if !strings.Contains(c, "observed descendants") || !strings.Contains(c, "1 s poll") {
			t.Fatalf("Containment() = %q", c)
		}
	}
}

func TestDisableAttachSucceeds(t *testing.T) {
	// The flag is checked for real in a helper on Linux; here only that
	// the call works on this platform.
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "PROCS_HELPER=attach")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper: %v: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("helper printed %q, want ok", out)
	}
}
