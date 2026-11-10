package procs

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
