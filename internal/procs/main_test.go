package procs

import (
	"fmt"
	"os"
	"testing"
)

// helpers are modes the test binary runs in when re-executed by a test.
// Each prints what the test needs and exits. Platform files add theirs.
var helpers = map[string]func(){}

func init() {
	helpers["pdeathsig"] = func() {
		// Start a long-lived child, print its pid, then block. The test
		// kills this process and checks the child is gone.
		p, err := Start(Spec{Argv: []string{"bash", os.Getenv("PROCS_FIXTURE")}, Env: []string{"PATH=" + os.Getenv("PATH")}})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Println(p.Pid())
		select {}
	}
	helpers["attach"] = func() {
		if err := DisableAttach(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := checkAttachDisabled(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Println("ok")
	}
}

func TestMain(m *testing.M) {
	if mode := os.Getenv("PROCS_HELPER"); mode != "" {
		h, ok := helpers[mode]
		if !ok {
			fmt.Fprintln(os.Stderr, "unknown helper", mode)
			os.Exit(2)
		}
		h()
		os.Exit(0)
	}
	if err := EnableSubreaper(); err != nil {
		fmt.Fprintln(os.Stderr, "EnableSubreaper:", err)
		os.Exit(2)
	}
	os.Exit(m.Run())
}
