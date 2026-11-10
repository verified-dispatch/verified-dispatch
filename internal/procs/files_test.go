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

func TestCloseOnExecAllMarksInheritedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Open without close-on-exec, the way an inherited descriptor arrives.
	fd, err := syscall.Open(path, syscall.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	if flags, _ := fcntlGetfd(fd); flags&syscall.FD_CLOEXEC != 0 {
		t.Fatal("fixture descriptor already close-on-exec")
	}
	if err := CloseOnExecAll(); err != nil {
		t.Fatal(err)
	}
	if flags, _ := fcntlGetfd(fd); flags&syscall.FD_CLOEXEC == 0 {
		t.Fatal("descriptor still inherited after CloseOnExecAll")
	}
	for _, std := range []int{0, 1, 2} {
		if flags, err := fcntlGetfd(std); err == nil && flags&syscall.FD_CLOEXEC != 0 {
			t.Fatalf("standard descriptor %d was marked close-on-exec", std)
		}
	}
	var out bytes.Buffer
	p := startFixture(t, "fds.sh", Spec{Stdout: &out})
	waitFor(t, p, 10*time.Second)
	for _, f := range strings.Fields(out.String()) {
		if n, err := strconv.Atoi(f); err == nil && n == fd {
			t.Fatalf("child received descriptor %d: %q", fd, out.String())
		}
	}
}
