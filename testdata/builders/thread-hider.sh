#!/usr/bin/env bash
# Leaves behind a process whose main thread has exited while other threads
# keep running. The process table shows it as a zombie. The test binary
# supplies that process in a helper mode.
set -u
"$PROCS_TESTBIN" -test.run='^$' >/dev/null 2>&1 </dev/null &
echo "$!"
exit 0
