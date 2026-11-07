// Command vdispatch dispatches a builder agent on a plan slice, verifies
// the result from git facts, gates it in an isolated worktree, and prints a
// receipt an orchestrator can act on without reading the builder's output.
package main

import (
	"fmt"
	"io"
	"os"
)

// version is set at build time by goreleaser through -ldflags.
var version = "dev"

const usage = `usage: vdispatch <command> [flags]

commands:
  run      dispatch a builder on a slice
  wait     wait for a run and print its receipt
  show     print a run's receipt
  log      print a run's stored output
  list     list runs for a repository
  check    verify the current tree against the last baseline
  prune    delete runs past retention
  unlock   clear a lock nobody holds
  version  print the version
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes one command and returns the process exit code. Commands that
// print a receipt return the receipt's code; every other command returns 0
// on success and 1 on failure.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 1
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, "vdispatch "+version)
		return 0
	case "run", "wait", "show", "log", "list", "check", "prune", "unlock":
		fmt.Fprintf(stderr, "vdispatch: %s is not implemented yet\n", args[0])
		return 1
	default:
		fmt.Fprintf(stderr, "vdispatch: unknown command %q\n", args[0])
		fmt.Fprint(stderr, usage)
		return 1
	}
}
